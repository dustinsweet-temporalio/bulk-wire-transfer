# Demo guide, part 2: when things go wrong

This guide picks up where [part 1 (the happy path)](DEMO-GUIDE-HAPPY-PATH.md) leaves off. It walks through two
failure scenarios, one click at a time:

- **Scenario A: Core goes down mid-draft.** The bank's core system is unreachable for 45 seconds while 5,000
  wires are being written. Temporal retries on its own, and the batch finishes whole. Nobody does anything.
- **Scenario B: a person has to fix it.** An account is put on hold, so some chunks *cannot* succeed no matter how
  often they are retried. Those chunks wait inside the workflow until a bank operator fixes the account and
  says "try again". Then two approvers sign and the wires are released.

Together they show the two kinds of failure every batch system has: the kind that fixes itself with time, and the
kind that needs a human decision. Temporal handles both without the batch ever leaving the workflow.

Allow about **3 minutes** for Scenario A and **5 minutes** for Scenario B in front of an audience.

| Phase | What you do | When |
|---|---|---|
| [Before you start](#before-you-start) | Part 1 setup, plus one more window | Before the meeting |
| **Scenario A** | | |
| [A1. Set the stage](#a1-set-the-stage) | Reset, upload, slow Core down | Live |
| [A2. Take Core down](#a2-start-the-draft-and-take-core-down) | Draft, then cut Core off for 45 s | Live |
| [A3. Watch it retry](#a3-watch-it-retry) | Amber tiles, retry counts, the alert | Live |
| [A4. Look under the hood](#a4-look-under-the-hood-optional) | The retry in the Temporal UI | Live, optional |
| [A5. Core comes back](#a5-core-comes-back) | The batch finishes whole | Live |
| **Scenario B** | | |
| [B1. Set the stage](#b1-set-the-stage) | Reset, upload, but do *not* draft | Live |
| [B2. Place a hold](#b2-morgan-places-a-hold-on-an-account) | Morgan freezes one account | Live |
| [B3. Draft](#b3-jordan-drafts-the-batch) | 7 chunks stop and wait | Live |
| [B4. Fix it](#b4-morgan-fixes-it) | Morgan releases the hold, retries | Live |
| [B5. Dual approval](#b5-dual-approval-alex-then-sam) | Alex and Sam sign; wires are released | Live |
| [B6. Look under the hood](#b6-look-under-the-hood) | The whole story in one history | Live |
| [Afterwards](#afterwards) | Reset | After |

---

## Before you start

1. **Do part 1, Phases 0–2:** the system is running, every chip in the Control Center is green (worker-v2 and
   worker-v3 grey), and you have these windows open and signed in:

   | Window | Open | Sign in as | Plays |
   |---|---|---|---|
   | 1 | http://localhost:3001 | `SUMMIT` / `jordan` | Jordan Ellis, who prepares payroll |
   | 2 | http://localhost:3001 | `SUMMIT` / `alex` | Alex Rivera, Controller (approver 1) |
   | 3 | http://localhost:3001 | `SUMMIT` / `sam` | Sam Patel, Treasurer (approver 2) |
   | 4 | http://localhost:8233/namespaces/bulkwire/workflows | (no sign-in) | The Temporal UI |
   | **5 (new)** | http://localhost:3001 | Company ID **`BANK`**, User ID **`morgan`** | Morgan Lee, the bank's wire operations team |
   | Control Center | http://localhost:3100 | (no sign-in) | You, backstage. Keep it off the shared screen. |

   Password for everyone: **`demo`**.

   Morgan works *for the bank*, not for Summit, so Morgan's portal looks different: the left menu has only
   **Bulk wire exceptions** and **Accounts**, and Morgan lands on **Bulk wire exceptions**, which should say
   "No batches need attention."

2. **Optional: turn on engineering annotations in Window 1.** Click **Jordan Ellis · Summit Logistics, Inc.** at the
   top right and check **Show engineering annotations**. That puts an **Open in Temporal UI** button on the batch
   page, which is the quickest way into the Temporal UI in A4 and B6. Leave it off for a business audience; you
   can use Window 4 instead.

3. **Download the sample file once** (if you still have `summit-payroll-5000.csv` in Downloads from part 1, skip
   this): Window 1 → **Bulk Wire Upload** → **Sample files** → **summit-payroll-5000.csv** → **Download**. Both
   scenarios use it.

---

# Scenario A: Core goes down mid-draft

**The story:** halfway through writing 5,000 wires into the bank's core system, the core system goes down for
45 seconds. In most batch systems that means failed records, a dead-letter queue, and someone reconciling
afterwards. Here, the batch simply waits it out.

## A1. Set the stage

1. **Control Center → Reset → Reset demo.** All eight steps say **ok**.
2. **Window 1 (Jordan):** **Bulk Wire Upload** → drag `summit-payroll-5000.csv` onto the drop box. The review page
   opens: **Ready to draft**, 5,000 wires, $13,121,518.99, 6 groups. **Do not click Save as draft yet.**
3. **Control Center → Core load & queues.** On the card **Core lakeshore**, type **`1500`** in the **latency ms**
   box and click **set**. "done" appears next to the button.

   This makes every chunk take 1.5 seconds in Core, so the draft lasts long enough to break it in the middle.
   It is a fixed delay per call: it does not pretend that more load makes Core slower.

4. **Control Center → Failure catalog.** Type **`F01`** in the filter box. One card is left: **F01 Core outage
   (503)**, with a button **Outage 45 s (Lakeshore)**. Leave this page open; in the next step you need that button
   within a few seconds.

**Say:** "Same payroll file as before: 5,000 wires. This time we're going to pull the plug on the bank's core
system in the middle of the draft."

## A2. Start the draft and take Core down

Timing matters here: the whole draft takes about 20 seconds at this speed, so have the Control Center's F01 card
on screen (on your private display) before you start.

1. **Window 1 (Jordan):** click **Save as draft**. Tiles start turning green, two at a time.
2. When **about 5 tiles are green** (3 to 5 seconds after you clicked), go to the Control Center and click
   **Outage 45 s (Lakeshore)**. "done" appears next to it.

From this moment, every call to Lakeshore's Core gets an HTTP 503 "Core is unavailable" for 45 seconds. The status
strip at the top of the Control Center shows **Core Lakeshore · outage · 1500 ms**.

> **Missed the moment?** If the draft finishes (status **Awaiting approval**) before you click Outage, nothing
> visible will happen. Go back to A1 and start again; it takes under a minute.

## A3. Watch it retry

**Window 1 (Jordan).** Within a couple of seconds:

- The **two tiles that were in flight turn amber** and show the attempt number with a countdown to the next
  attempt, for example **`retry #4 · in 8s`**. The count climbs while Core is down: about **#7** by the time it
  comes back.
- **The other tiles stay dashed (pending).** At most two chunks run at a time ("2 in flight at most"), and the two
  in flight are the ones being retried. Nothing piles up on a broken system.
- **No tile turns red.** The status pill stays **Drafting** and the **Live** dot stays green. **Drafted** stops
  climbing (in a test run it paused at 1,948 / 5,000).
- **Hover over an amber tile** for the details: "Retrying · attempt 6 · last failure: CORE_UNAVAILABLE: Core is
  unavailable (DEMO-ONLY injected outage) (HTTP 503)".

**Say:** "Core is down. Temporal is retrying those two chunks for us, with backoff: one second, two, four, eight,
sixteen, then every twenty seconds. There's no limit on the number of attempts, only on total time: thirty minutes
by default. That's the line at the bottom of the page: 'retry budget 30 min'."

**Then show the alert. Control Center → Alerts & metrics.** The card **bulkwire_core_attempts_over_threshold** has
a row **lakeshore · InsertDraftChunk** with a count that rises (6 in a test run, then 8 by the end). It counts
attempts past the fifth.

**Say:** "The workflow code never sees these retries. That's on purpose. So how does on-call find out Core is down?
From this metric: it fires long before the thirty-minute budget runs out."

## A4. Look under the hood (optional)

Do this while Core is still down, or skip it if time is short.

1. Open the batch in the Temporal UI: **Open in Temporal UI** in Window 1 (if annotations are on), or Window 4 →
   click the top row (status **Running**).
2. Point at the **Timeline** chart: the two retrying chunks show **"Attempt 6 / ∞ · InsertDraftChunk · Draft chunk
   g01-c04 · 200 wires · ••0017 · 2026-10-31"** (your chunk IDs and attempt number will differ).
3. The tab **Pending Activities** shows **2**. Click it to see each retrying chunk's attempt count and last
   failure.

**Say:** "That infinity sign is the point. These attempts aren't capped at three or five; they keep going for as
long as the retry budget allows. And the Temporal Service holds the retry timer, not our worker, so we could
restart every worker right now and the retries would carry on."

## A5. Core comes back

**Window 1 (Jordan).** The outage ends 45 seconds after you clicked. Then:

1. The amber tiles keep counting down ("in 11s", "in 9s"…) for a few more seconds. Because of the backoff, the
   first retry after Core recovers can take up to 20 seconds. That's normal.
2. The tiles turn green, the rest of the batch drafts, and about **one minute after the outage started** the pill
   says **Awaiting approval**:
   - **Drafted 4,997 / 5,000 · Rejected 3**: exactly the same as the happy path.
   - **Wires (bank records)** tab: "4,997 drafted, 3 rejected". No wire was lost, and none was written twice.
3. Optional: **Control Center → Core load & queues → Core lakeshore** shows "failed 12" (or similar): those are
   the 503s Core sent back.

**Say (close):** "Core was down for 45 seconds in the middle of a 5,000-wire draft, and the batch finished whole.
What did that take? An Activity retry policy with no attempt cap, bounded by time instead. What did we *not* need?
A dead-letter queue, a reconciliation job, or anyone on call clicking 'replay'."

You can approve this batch (part 1, Phases 5–6) if you want to finish the story, but most audiences are happy to
move on. Scenario B ends with the approvals anyway.

---

# Scenario B: a person has to fix it

**The story:** the bank's operations team puts a fraud hold on one of Summit's accounts just as payroll goes in.
Retrying can't fix that; a person has to look at it. The affected chunks stop and wait *inside the workflow*,
everything else carries on, and when the hold is cleared the batch picks up exactly where it stopped.

## B1. Set the stage

1. **Control Center → Reset → Reset demo.** All eight steps say **ok**. (This also removes the latency and the
   outage from Scenario A, and releases any account holds.)
2. **Window 1 (Jordan):** **Bulk Wire Upload** → drag `summit-payroll-5000.csv` onto the drop box. The review page
   opens: **Ready to draft**.
3. **Do not click Save as draft yet.** Point at the **Groups** table: account **••0033** appears twice, once per
   date (1,049 wires on 2026-10-30 and 251 on 2026-10-31). That account is about to get a hold.

> **Order matters.** Upload *before* Morgan places the hold. If the hold is already on when the file is uploaded,
> the upload check catches it: the file shows **Needs corrections** with 1,300 errors, and **Save as draft** is
> greyed out. That's correct behavior, but it's not this scenario. If it happens, **Reset demo** and start B1
> again.

## B2. Morgan places a hold on an account

**Window 5 (Morgan).**

1. In the left menu, click **Accounts**.
2. The **Customer** dropdown at the top starts on **Northline Medical Group**. Change it to
   **Summit Logistics, Inc.** Three accounts appear, all **Active**.
3. On the row **••0033 · Summit Cold Chain Operating**, click **Place hold**.
4. The dialog **"Place a hold on ••0033"** opens, with the reason already filled in: **Fraud review**. Click
   **Place hold**.
5. A message confirms "Hold placed on ••0033", and the row now says **On hold · Fraud review**.

**Say:** "Morgan works in the bank's wire operations. Fraud review just froze one of Summit's accounts. Nobody told
Jordan, which is realistic."

## B3. Jordan drafts the batch

**Window 1 (Jordan).**

1. Click **Save as draft**.
2. Within about 5 seconds, the **7 tiles for account ••0033 turn red** with a warning icon (g02-c00 to g02-c04,
   and g03-c00 and g03-c01). The other 16 tiles turn green as usual and are done about 10 seconds after you
   clicked.
3. Look at the top of the page:
   - The status pill still says **Drafting**, next to a red **Needs attention** pill and the green **Live** dot.
   - **Drafted 3,698 / 5,000.** The 1,300 wires on the held account are waiting.
   - **Rejected 2**, not 3: the third blocked beneficiary is in one of the waiting chunks. It shows up once
     that chunk runs.
4. Hover over a red tile: "Needs attention: ACCOUNT_ON_HOLD — account ending 0033 is on hold (Fraud review)".
5. The **Timeline** card lists seven lines: **chunk parked g02-c00: ACCOUNT_ON_HOLD (••0033)**, and so on.

**Say:** "Retrying can't fix a hold, so the system didn't try. Core said 'this account is on hold'. That's a
permanent answer, not a temporary failure, so those seven chunks parked. Parked means they're waiting
*inside this workflow*, with everything they need to resume. Nothing went to a dead-letter queue. The other sixteen
chunks finished normally. And this batch will wait like this for as long as it takes: an hour, or over a weekend."

## B4. Morgan fixes it

**Window 5 (Morgan).**

1. Click **Bulk wire exceptions** in the left menu. Within a second or two, a row appears:
   **Summit Logistics, Inc. · summit-payroll-5000.csv · DRAFTING · 7 parked items · just now**.

   **Say:** "This list is the bank's exception queue. There's no separate database behind it: it's a search over
   the running workflows, using a flag the workflow sets when it parks something."

2. Click the row. A panel opens on the right with one card per parked chunk. Each card shows the chunk,
   **ACCOUNT_ON_HOLD**, **••0033**, the message "account ending 0033 is on hold (Fraud review)", and four buttons:
   **Release hold**, **Retry chunk**, **Reject chunk wires**, **Cancel batch**.
3. **First fix the cause:** click **Release hold** on any card (once is enough: the hold is on the account, not the
   chunk). A message confirms "Hold released on ••0033", and every card's button changes to **Hold released**.
4. **Then resume the work:** click **Retry chunk** on the first card. The card disappears. Do the same for the
   remaining six. In Window 1, each retried tile turns blue, then green.
5. After the seventh, the panel closes and the list says **No batches need attention.**

**Window 1 (Jordan).** Within a few seconds of the last retry, the pill says **Awaiting approval**:

- **Drafted 4,997 / 5,000 · Rejected 3**: the same result as if nothing had happened.
- **Timeline:** seven lines **chunk resolved g02-c00: RETRY by morgan**, and so on, then **drafted** and
  **awaiting authorization**. Who decided, and when, is part of the batch's own record.

**Say:** "Morgan fixed the cause in the bank's system, then told the workflow to try again. That 'try again' is a
Temporal Update: the workflow checks it's valid, records who sent it, and re-runs exactly those chunks. Nothing
was replayed from outside, and nothing ran twice."

> **Optional variations** (both fit into the same run):
> - **Retry before releasing the hold.** Before step 3, click **Retry chunk** on one card. In Window 1 that tile
>   is red again within a second or two, and the timeline shows **chunk resolved … RETRY by morgan** followed by
>   a new **chunk parked** line. The retry was real; it met the same hold. "Retrying can't fix a business
>   problem; a person has to." Then carry on with step 3.
> - **Reject one chunk instead of retrying it.** On the card **Chunk g03-c01** (it holds a single wire), click
>   **Reject chunk wires** → **Confirm**. Its tile changes to **Rejected**, and that wire is recorded in Core as
>   Rejected. Retry the other six as usual. The batch ends with **4,996 drafted and 4 rejected** instead of
>   4,997 and 3. "The operator decides what happens to the money; the workflow carries out the decision."

## B5. Dual approval (Alex, then Sam)

This is the same as part 1, Phases 5 and 6.

1. **Window 2 (Alex):** **Approvals** (badge **1**) → click the batch → **Review and approve** → security code
   **`246810`** → **Approve**. The dialog says **"Approval recorded: 1 of 2 approvals. A second approver must
   sign."** Click **Close**.
2. **Window 3 (Sam):** **Approvals** → click the batch → **Review and approve** → **`246810`** → **Approve**. The
   dialog says **"Approved (APR-…). Release started."** Click **Close**.
3. **Window 1 (Jordan):** **Releasing**, then about 10 seconds later **Authorized**:
   - **Authorized 4,997 / 5,000.**
   - **Timeline:** **authorization received APR-… · approved by Alex Rivera, Controller and Sam Patel, Treasurer**
     → **authorizing** → **authorized**.

**Say:** "The approval rules live in the bank's core system: the security code, the limits, the two signatures.
The workflow waited, for free, for Core's answer, then released the wires, including the ones that were stuck
on the held account a minute ago."

## B6. Look under the hood

Open the batch in the Temporal UI (**Open in Temporal UI** in Window 1, or Window 4 → the top row, now
**Completed**).

| Where | What it shows | Say |
|---|---|---|
| **Result** panel | `"phase": "AUTHORIZED"`, `"drafted": 4997`, `"authorized": 4997`, `"rejected": 3` | "One batch, one result, despite the hold." |
| Header: **History Size** (about 170 KB) and **Event History** (about 380 events) | The whole story, park and resolve included | "The parked chunks, Morgan's decisions, the approval wait, and the release, all in one history." |
| **Timeline** chart | The **Approval window (24h)** timer, and every chunk as a labeled Activity | "The approval deadline is a durable timer inside the batch. No cron job." |
| **Event History** tab | The seven `resolve` Updates from Morgan, each followed by the retried chunk | "Every human decision is in the audit trail, next to the work it caused." |

**Say (close):** "A hold stopped exactly the chunks it affected. A banker fixed it, and the batch resumed where it
stopped. Then Core ran dual control. Under the hood: a permanent error parks the chunk inside the workflow until an
operator's Update resumes it, and the approval wait is a durable timer. What we didn't build: dead-letter replays
that hit the same hold again, and a separate approval workflow."

---

## Afterwards

- **Run either scenario again:** Control Center → **Reset** → **Reset demo**, then start again at A1 or B1. Reset
  removes the latency, any outage, and any account holds.
- **Stop the system but keep its data:** `make down`. **Wipe everything:** `make reset`.

---

## If something goes wrong

| What you see | What to do |
|---|---|
| A: the draft finished before you clicked **Outage 45 s** | Reset and start again at A1. Have the F01 card on screen before you click Save as draft. |
| A: some tiles turned **red** | A real failure, not the outage: hover over a red tile to read why. Most likely a hold is still on from Scenario B. **Reset demo** clears it. |
| A: the tiles stay amber long after 45 seconds | The first retry after Core recovers can take up to 20 seconds (the backoff cap). If it's much longer, check the Control Center status strip: **Core Lakeshore** should no longer say "outage". |
| B: the file shows **Needs corrections** with 1,300 errors | The hold was placed before the upload. **Reset demo** and do B1 before B2. |
| B: no tiles turn red | The hold wasn't on when you drafted. Check Morgan's **Accounts** page (customer **Summit Logistics, Inc.**): ••0033 should say **On hold**. |
| B: a tile you retried turned red again | The hold is still on: click **Release hold**, then **Retry chunk** again. |
| B: the batch ended with 4,996 / 4 instead of 4,997 / 3 | Someone clicked **Reject chunk wires** on a card. That's the reject variation working; Reset to run it clean. |
| B: Morgan's exception list is empty but Jordan sees red tiles | The list lags the batch by a second or two. Wait, or click **Bulk wire exceptions** again. |
| B: the **Release hold** button is grey | The hold is already released (it then reads **Hold released**). Go straight to **Retry chunk**. |
| Anything else | See part 1, [If something goes wrong](DEMO-GUIDE-HAPPY-PATH.md#if-something-goes-wrong). |
