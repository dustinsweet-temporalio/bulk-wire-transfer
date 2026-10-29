# Demo guide, part 1: the happy path

This guide walks through running the demo from scratch, one click at a time. It covers **only the happy path**:
a customer uploads a payroll file, it is drafted into the bank's system, two approvers sign, and the wires are
released. No failures, no chaos. [Part 2](DEMO-GUIDE-FAILURES.md) builds on this one: a Core outage, then an
account hold that a bank operator has to fix.

The whole happy path takes about **5 minutes** in front of an audience. The system does its part in under
30 seconds; the rest is you talking.

| Phase | What you do | When |
|---|---|---|
| [0. One-time setup](#phase-0-one-time-setup) | Install and configure Docker | Once per laptop |
| [1. Start the system](#phase-1-start-the-system) | `make up`, then check it is healthy | Before the meeting |
| [2. Set the stage](#phase-2-set-the-stage) | Reset the demo and open your windows | 5 minutes before |
| [3. Upload the file](#phase-3-upload-the-file-jordan) | Jordan uploads a payroll file | Live |
| [4. Save as draft](#phase-4-save-as-draft-and-watch-it-run-jordan) | Watch 5,000 wires go into the bank | Live |
| [5. First approval](#phase-5-first-approval-alex) | Alex approves | Live |
| [6. Second approval](#phase-6-second-approval-sam) | Sam approves; the wires are released | Live |
| [7. Look under the hood](#phase-7-look-under-the-hood-temporal-ui) | Open the same batch in the Temporal UI | Live |
| [8. Afterwards](#phase-8-afterwards) | Reset for the next run, or shut down | After |

---

## Phase 0: One-time setup

You only do this once per laptop.

1. **Install Docker Desktop.**
2. **Give Docker enough memory.** Docker Desktop → Settings → Resources → Memory: **12 GB** or more. The demo
   runs two SQL Servers plus about a dozen other containers.
3. **Apple Silicon Macs only: turn on Rosetta.** Docker Desktop → Settings → General → check
   **"Use Rosetta for x86_64/amd64 emulation on Apple Silicon"**. SQL Server only ships for Intel CPUs.
4. **Check you have `make`, `curl`, and `jq`.** In a terminal:
   ```bash
   make --version && curl --version && jq --version
   ```
   On a Mac, `make` and `curl` are already there; install `jq` with `brew install jq` if it is missing.
   You do **not** need Go, .NET, or Node: everything builds inside containers.
5. **Get the code** and open a terminal in the repository folder (the one with `docker-compose.yml`).

---

## Phase 1: Start the system

Do this before the meeting, not live. The very first start downloads several GB of images and builds every
service, so it takes a while. After that, starting takes a minute or two.

1. In the repository folder, run:
   ```bash
   make up
   ```
   `make up` is a wrapper around Docker Compose: it picks the Redis claim-check store, runs
   `docker compose up -d --build`, prepares (but does not start) the two extra workers used in a later demo, waits
   for the portals to answer, and prints the URLs.

2. **Wait for it to finish.** The last lines list the URLs:
   ```
   Lakeshore portal   http://localhost:3001
   Prairie portal     http://localhost:3002
   Control Center     http://localhost:3100
   Temporal UI        http://localhost:8233/namespaces/bulkwire/workflows
   ...
   ```

3. **Check that everything is up.** Open the **Control Center** at http://localhost:3100.
   - The strip across the top shows a chip for each part of the system. Every chip should have a **green dot**,
     except **worker-v2** and **worker-v3**, which are grey on purpose (they are for the "we shipped a bug" demo).
   - The **Overview** page lists every container. All should say **running**, except worker-v2 and worker-v3,
     which say **created**.

   If something is not green, see [If something goes wrong](#if-something-goes-wrong).

---

## Phase 2: Set the stage

Do this about 5 minutes before you present.

1. **Reset the demo.** Control Center → **Reset** (left menu) → click **Reset demo**. A table lists eight steps;
   each should say **ok**. This clears the bank's data and any batches from earlier runs, so you start clean.

2. **Open one browser window per person.** You will play three people. Each window keeps its own sign-in, so
   use a separate window (or a fresh tab) for each. Do not use "Duplicate tab": a duplicated tab copies the
   sign-in.

   | Window | Open | Sign in as | Plays |
   |---|---|---|---|
   | 1 | http://localhost:3001 | Company ID `SUMMIT`, User ID `jordan` | Jordan Ellis, who prepares payroll |
   | 2 | http://localhost:3001 | Company ID `SUMMIT`, User ID `alex` | Alex Rivera, Controller (approver 1) |
   | 3 | http://localhost:3001 | Company ID `SUMMIT`, User ID `sam` | Sam Patel, Treasurer (approver 2) |
   | 4 | http://localhost:8233/namespaces/bulkwire/workflows | (no sign-in) | The Temporal UI, for Phase 7 |

   **Password for everyone: `demo`.** Shortcut: on the sign-in page, open **Demo users** and click a user ID;
   it fills in all three fields for you.

   When you sign in, you land on **Home**: "Welcome, Jordan Ellis", the company's account balances, and
   **Recent bulk wire batches**. Batches from before the reset may appear there marked **Terminated**; that is
   expected.

3. **Keep the Control Center off the shared screen.** It is your backstage panel, not part of the bank's product.

---

## Phase 3: Upload the file (Jordan)

**Window 1 (Jordan).**

1. In the left menu, under **Payments**, click **Bulk Wire Upload**.
2. Scroll to **Sample files**. On the row **summit-payroll-5000.csv**, click **Download**. The file lands in
   your Downloads folder.
3. Drag that file onto the box that says **"Drop a CSV file here, or choose a file"** (or click the box and pick
   the file).
4. Within about a second, the review page opens:
   - Title **summit-payroll-5000.csv**, status **Ready to draft**.
   - **Total wires 5,000 · Total amount $13,121,518.99 · Groups (account + date) 6 · Errors 0.**
   - A **Groups** table: one row per originating account and date. Point at the row **••0033 · 2026-10-31 ·
     251 wires · 2 chunks**: 251 wires are split into one chunk of 250 and one of 1.
   - A **Records** table you can page through.

**Say:** "Every row was checked before anything was sent to the bank: routing numbers, amounts, dates, accounts.
Nothing has touched the bank's system yet, and there is no workflow yet either."

---

## Phase 4: Save as draft and watch it run (Jordan)

**Window 1 (Jordan).**

1. Click **Save as draft** (top right).
2. The page switches to the live view. The status pill says **Drafting**, with a green **Live** dot next to it.
3. Watch the **Chunks** grid: 23 tiles, one per chunk of up to 250 wires, at most 2 running at a time
   ("2 in flight at most"). Tiles turn from dashed (pending) to blue (running) to green (drafted). The
   **Drafted** bar fills up toward 5,000.
4. About **10 seconds** later, the status pill changes to **Awaiting approval**. You will see:
   - **Drafted 4,997 / 5,000** and **Rejected 3**. The three rejections are listed under **Rejections**: each is
     `BENEFICIARY_BLOCKED` ("Beneficiary matches the bank's screening list"). That is the bank's screening
     doing its job, not an error.
   - The **Timeline** card: started → draft requested → plan ready → gentx batch ready (the batch record in
     the bank's Core) → drafted → awaiting authorization.
   - **Approvals:** "Awaiting approval · two approvers required".
5. Optional: click the **Wires (bank records)** tab. This reads straight from the bank's database: **5,000 wires**,
   "4,997 drafted, 3 rejected". Click **Overview** to go back.

**Say:** "One Temporal workflow now owns this batch. It drafted 5,000 wires in 23 chunks and is now waiting for
the bank's approval decision. That wait is durable: it can last up to 24 hours, through restarts and
deployments, and costs nothing while it waits."

> **Want it slower so you can talk over it?** Before clicking **Save as draft**, open Control Center → **Core load &
> fairness** → card **Core lakeshore** → type `1500` in the latency box → **set**. Each chunk then takes
> 1.5 seconds and the draft lasts about 20 seconds. **Reset demo** puts it back to normal.

---

## Phase 5: First approval (Alex)

**Window 2 (Alex).** (If Alex was already signed in, click **Home** or **Approvals** to refresh.)

1. In the left menu, click **Approvals**. A badge shows **1**. The batch **summit-payroll-5000.csv** is listed
   as **Awaiting approval**.
2. Click the row. The same live batch page opens, with a **Review and approve** button at the top.
3. Click **Review and approve**. A dialog, **Approve and release**, shows: Wires 5,000 · Total amount
   $13,121,518.99 · Process dates · Rejected wires 3 · "This batch requires two approvers."
4. In **Enter the 6-digit security code**, type **`246810`** (the dialog shows the demo code, too).
5. Click **Approve**.
6. The dialog says: **"Approval recorded: 1 of 2 approvals. A second approver must sign."** Click **Close**.

**Say:** "The approval rules live in the bank's core system, not in the workflow: the security code, the
amount limits, and dual control. One approval is not enough for $13 million."

---

## Phase 6: Second approval (Sam)

**Window 3 (Sam).**

1. Click **Approvals**, then click the batch, then **Review and approve**.
2. Type **`246810`** and click **Approve**.
3. The dialog says: **"Approved (APR-…). Release started."** Click **Close**.

**Now switch to Window 1 (Jordan)** and click **Overview** if needed.

4. The status pill shows **Releasing**, and the tiles turn dark green one by one as chunks are authorized.
5. About **10 seconds** later, the pill says **Authorized**:
   - **Authorized 4,997 / 5,000.** (The 3 screened wires were never drafted, so they are never released.)
   - **Approvals:** "Approved APR-… — Alex Rivera, Controller signed …; Sam Patel, Treasurer signed …"
   - **Timeline:** … authorization received APR-… · approved by Alex Rivera, Controller and Sam Patel, Treasurer
     → authorizing → authorized.

**Say:** "When the second approval landed, the bank told the workflow, and the same workflow that drafted the
wires released them. One business process, one workflow, one ID from upload to release."

---

## Phase 7: Look under the hood (Temporal UI)

**Window 1 (Jordan).**

1. Click your name at the top right (**Jordan Ellis · Summit Logistics, Inc.**) and check
   **Show engineering annotations**. Small engineering notes appear next to parts of the page (for example
   "Query `status` · 1 s poll"), and an **Open in Temporal UI** button appears next to the batch title.
2. Click **Open in Temporal UI**. The Temporal UI opens on this batch's workflow,
   `bulkwire/lakeshore/<batch id>`, marked **COMPLETED**.

Things to point at, top to bottom:

| Where | What it shows | Say |
|---|---|---|
| Header: **Deployment** `bulkwire`, **Build ID** `v1`, **Versioning Behavior** `Pinned` | Worker Versioning | "This batch runs start to finish on the code it started with." |
| Header: **Fairness** `lakeshore` | The fairness key | "Every bank gets its fair share of the workers." |
| Header: **History Size** about 84 KB | The whole batch's history | "5,000 wires, but the rows themselves never enter Temporal; only references do." |
| **Input** and **Result** panels | The batch input and the final totals (4,997 authorized, 3 rejected) | "Readable here, but stored encrypted. The UI decrypts through our Codec Server." |
| **Timeline** (scroll down) | Every chunk as a labeled Activity, and the **Approval window (24h)** timer | "The approval wait is a durable timer: no polling, no cron job." |
| **Event History** tab | Every step, in order (about 320 events) | "This is the audit trail, for free." |

**Say:** "Everything you just watched in the bank's portal came from this one workflow: the progress bars are a
live Query of its state, and the lists come from its Search Attributes. There is no separate status database."

To turn annotations off again, use the same menu.

---

## Phase 8: Afterwards

- **Run it again:** Control Center → **Reset** → **Reset demo**, then start again at Phase 3. The windows can
  stay signed in.
- **Stop the system but keep its data:** `make down` (start again with `make up`).
- **Stop the system and wipe everything** (bank data, Temporal history, stored files): `make reset`.

---

## If something goes wrong

| What you see | What to do |
|---|---|
| A chip in the Control Center is red or grey (other than worker-v2/v3) | Control Center → **Reset** → **Reset demo**. It restarts stopped services. If that does not help, run `make up` again. |
| "We couldn't sign you in." | Check that you are on the right portal (Summit users are on :3001), the Company ID is `SUMMIT`, and the password is `demo`. |
| "Live progress is temporarily unavailable. Your batch is safe…" | No worker is running. **Reset demo** restarts them; the batch picks up where it stopped. |
| The batch does not appear in **Approvals** yet | Lists catch up a second or two after the live page. Click **Approvals** again. |
| The **Approve** button stays grey | It needs exactly 6 digits: `246810`. |
| The page shows an old batch or looks stale | Reload the browser page. You stay signed in. |
| `make up` fails with "port is already allocated" | Something else on your laptop uses one of the demo's ports (3001, 3002, 3100, 7233, 8233, 8089, 6379, 9464, 9465, 14331, 14332). Stop it and run `make up` again. |
| `sql-lakeshore` or `sql-prairie` stops shortly after `make up` (Mac) | Rosetta is off, or Docker has less than 12 GB of memory (Phase 0, steps 2–3). Fix it, then `make up` again. |
