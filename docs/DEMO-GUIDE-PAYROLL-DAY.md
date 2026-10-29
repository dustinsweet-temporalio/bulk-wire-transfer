# Demo guide, part 3: payroll day

This guide follows [part 1](DEMO-GUIDE-HAPPY-PATH.md) and [part 2](DEMO-GUIDE-FAILURES.md). It answers the
question every bank's architect asks about a shared platform: **"If one customer submits a huge batch, does it
swamp our core system, or slow everyone else down?"**

It's payroll day. Two customers of Lakeshore Community Bank (Summit Logistics and Northline Medical) and one
member of Prairie Valley Credit Union (Harvest Growers) all submit big payroll files at the same moment: 26,000
wires in total. You run it twice: once with no limit, to show the problem, then with a per-bank limit that Temporal
enforces, to show the fix.

Allow about **4 minutes** in front of an audience.

| Phase | What you do | When |
|---|---|---|
| [Before you start](#before-you-start) | Sign in three customers; open the load charts | Before the meeting |
| [1. Payroll day, no limit](#1-payroll-day-with-no-limit) | Launch three batches; watch Lakeshore's Core take the load | Live |
| [2. Payroll day, with a limit](#2-payroll-day-with-a-per-bank-limit) | Turn on 3 calls/s per bank; launch again | Live |
| [3. One bank down, the other fine](#3-optional-one-bank-down-the-other-unaffected) | Lakeshore's Core fails; Prairie keeps working | Live, optional |
| [Afterwards](#afterwards) | Reset | After |

---

## Before you start

1. **Do part 1, Phases 0–2:** the system is running and every Control Center chip is green (worker-v2 and
   worker-v3 grey).
2. **Open three portal windows**, one per customer. Password for everyone: **`demo`**.

   | Window | Open | Company ID | User ID | Who |
   |---|---|---|---|---|
   | 1 | http://localhost:3001 | `SUMMIT` | `jordan` | Jordan Ellis, Summit Logistics (a Lakeshore customer) |
   | 2 | http://localhost:3001 | `NORTHLINE` | `taylor` | Taylor Brooks, Northline Medical (another Lakeshore customer) |
   | 3 | http://localhost:**3002** | `HARVEST` | `riley` | Riley Chen, Harvest Growers (a Prairie Valley member) |

   Window 3 is a different bank: note the port, **3002**, and the Prairie Valley branding. Each window lands on
   **Home**, where **Recent bulk wire batches** will show that customer's batch with a live progress bar.

3. **Open the Control Center on Core load & fairness** (http://localhost:3100/#load). This is the one scenario
   where you **put the Control Center on the shared screen**: its two charts are the evidence. Introduce it
   honestly: "This is the demo's control panel. These two charts count the calls arriving at each bank's core
   system."

   What's on the page, top to bottom:
   - **Per-FI fairness rate limit (Activity task queue)**, with a pill that says **OFF**, a box **rps per FI**
     (already set to 3), and the buttons **Enable limit** and **Remove limit**.
   - Two chart cards, **Core lakeshore** and **Core prairie**. Each chart has three lines: **calls/s** (yellow),
     **in-flight** (blue), and **failures/s** (red). Under each chart: "max in-flight · total · failed · 10 s avg".
   - **Payroll day launcher**, with **concurrency 4** and the button **Launch payroll day**.

   The charts show the last 60 seconds and only fill while this page is open, so keep it open.

**Say:** "Two banks, each with its own core system. Lakeshore has two big business customers; Prairie has one. All
three are about to submit payroll at the same time, through the same Temporal namespace and the same pool of
workers."

---

## 1. Payroll day with no limit

1. **Control Center → Reset → Reset demo.** All eight steps say **ok**. Go back to **Core load & fairness**.
2. Check the limit pill says **OFF**.
3. Click **Launch payroll day.** Within a second, a result box lists three batches (Jordan's
   `summit-payroll-10000`, Taylor's `northline-payroll-10000`, Riley's `harvest-6000`), each with
   `"draftStatus": 202`, which means accepted.

   The launcher does what the three customers would do by hand: upload the file and click **Save as draft**,
   through the same API the portal uses. It's quicker than clicking through three windows live.

4. **Watch the charts** (in a test run):
   - **Core lakeshore** jumps to **12–15 calls/s** with **8 in flight**: two batches, four chunks each.
   - **Core prairie** runs at about **5–9 calls/s** with **4 in flight**: one batch.
5. **Glance at the portal windows.** Each Home page shows its batch **Drafting** with a progress bar, then
   **Awaiting approval**. Everything is done in about **10 seconds**.

**Say:** "With no limit, every batch runs as fast as its own settings allow, so Lakeshore's core system takes the
sum of everyone's load. Two customers means twice the calls, and on a real payroll day it's twenty customers, not
two. That's exactly how a core system gets swamped."

---

## 2. Payroll day with a per-bank limit

1. **Control Center → Reset → Reset demo.** (Reset also removes any limit, so always enable it *after* resetting.)
   Go back to **Core load & fairness**.
2. In **rps per FI**, leave **3**. Click **Enable limit**. The pill changes to **ON · 3/s per FI**.

   **Say:** "This is one setting on the Temporal Service, changed at runtime. No code change and no redeploy. It
   says: dispatch at most three Core calls per second *per bank*, across every batch and every worker."

3. Click **Launch payroll day** again.
4. **Watch the charts** (in a test run):
   - **Core lakeshore** goes flat at **3 calls/s** with about **2 in flight** (3 calls a second × 0.6 seconds
     each). It stays there until both batches are done.
   - **Core prairie** also runs at up to **3 calls/s**: that's Prairie's own allowance, separate from Lakeshore's.
5. **Watch the portal windows:**
   - **Riley (Window 3)** is done first, in about **10 seconds**.
   - **Jordan and Taylor (Windows 1 and 2)** move forward side by side, sharing Lakeshore's 3 calls a second, and
     reach **Awaiting approval** at about the same time, roughly **35 seconds** after launch.

**Say:** "Lakeshore's core system now sees a steady three calls a second, however many customers submit at once.
Both Lakeshore customers keep moving; neither one starves the other. And Prairie isn't stuck behind Lakeshore's
backlog, even though all three batches share one task queue: each bank gets its own allowance. The workflow code
didn't change at all. The Temporal Service enforces this when it hands out work."

> **What the limit is, precisely.** Every chunk's Activity carries a *fairness key*: its bank. The limit caps how
> many of those Activities the Temporal Service dispatches per second for each key. It limits rate, not
> concurrency, which is why the in-flight count settles at about rate × call time. Stricter isolation (separate
> worker capacity per bank) would need a task queue per bank.

**Say (close):** "Two big batches from two customers of one bank shared that bank's core capacity fairly, and the
other bank never noticed. That's Fairness keyed by bank, plus a per-key rate limit, enforced by Temporal. What you
don't need: custom throttling code, or a coordinator that knows about every batch in flight."

---

## 3. Optional: one bank down, the other unaffected

This shows the other half of tenant isolation: each bank has its own core system, so one bank's outage stays
with that bank.

1. **Control Center → Reset → Reset demo.**
2. **Window 3 (Riley):** **Bulk Wire Upload** → **Sample files** → **harvest-2000.csv** → **Download**, then drop
   it on the upload box. The review says **Ready to draft**, 2,000 wires, $4,773,105.97. **Don't draft yet.**
3. **Window 1 (Jordan):** upload `summit-payroll-5000.csv` (from part 1). **Don't draft yet.**
4. **Control Center → Failure catalog**, filter **`F08`**, click **Outage 120 s (Lakeshore)**. Lakeshore's core
   system now answers every call with "unavailable" for two minutes.
5. **Window 1 (Jordan):** click **Save as draft**. The first tiles turn amber and count retries, just like part 2,
   Scenario A.
6. **Window 3 (Riley):** click **Save as draft**. Riley's batch drafts normally and reaches **Awaiting approval** in
   about **5 seconds**: 2,000 / 2,000 drafted.

**Say:** "Lakeshore's core system is down, and Lakeshore's batches are waiting it out. Prairie's batch went straight
through: it runs against Prairie's own core system, and which bank a batch belongs to travels with the batch
itself. One bank's outage never touches another's."

To finish early, **Reset demo**: it ends the outage (and terminates Jordan's batch). Otherwise Lakeshore recovers
by itself two minutes after you clicked, and Jordan's batch completes.

---

## Afterwards

- **Control Center → Reset → Reset demo.** It removes the limit, ends any outage, and terminates running batches.
- If you skip the reset, at least click **Remove limit**; otherwise the next scenario you run will be slow.

---

## If something goes wrong

| What you see | What to do |
|---|---|
| The charts are empty or start mid-run | They only record while the **Core load & fairness** page is open. Open it before you click **Launch payroll day**. |
| Run 2 looks exactly like run 1 | The limit is off: Reset removes it. Check the pill says **ON · 3/s per FI**, then launch again. |
| The launcher result shows an error instead of three batches | Check every chip in the status strip is green. If one isn't, **Reset demo** and try again. |
| A portal window doesn't show the new batch | Home refreshes every 3 seconds. Make sure the window is signed in as the right customer (Jordan, Taylor, or Riley) and is on **Home**. |
| Riley's sign-in fails | Riley is on the **Prairie** portal: http://localhost:**3002**, Company ID `HARVEST`. |
| Anything else | See part 1, [If something goes wrong](DEMO-GUIDE-HAPPY-PATH.md#if-something-goes-wrong). |
