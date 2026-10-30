# Demo guide, part 3: payroll day

This guide follows [part 1](DEMO-GUIDE-HAPPY-PATH.md) and [part 2](DEMO-GUIDE-FAILURES.md). It answers the
question every bank's architect asks about a shared platform: **"If one customer submits a huge batch, does it
swamp our core system, or slow everyone else down?"**

It's payroll day. Two customers of Lakeshore Community Bank (Summit Logistics and Northline Medical) and one
member of Prairie Valley Credit Union (Harvest Growers) all submit big payroll files at the same moment: 26,000
wires in total. Each bank's core system has its own task queue in Temporal, with its own rate limit. You run
payroll day twice: once at the configured limit, then with Lakeshore's limit turned down at runtime, to show that
one bank's limit is enforced by Temporal and never touches the other bank. Then you show what happens to a small
batch that arrives while one bank's queue is busy.

Allow about **5 minutes** in front of an audience.

| Phase | What you do | When |
|---|---|---|
| [Before you start](#before-you-start) | Sign in three customers; open the load charts | Before the meeting |
| [1. Payroll day at the configured limit](#1-payroll-day-at-the-configured-limit) | Launch three batches; watch each bank's queue | Live |
| [2. Turn Lakeshore down to 3 calls/s](#2-turn-lakeshore-down-to-3-callss) | Override Lakeshore's queue; launch again | Live |
| [2b. A small batch on a busy queue](#2b-a-small-batch-on-a-busy-queue) | Three big batches, then a small one | Live, optional |
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

3. **Open the Control Center on Core load & queues** (http://localhost:3100/#load). This is the one scenario
   where you **put the Control Center on the shared screen**: its two charts are the evidence. Introduce it
   honestly: "This is the demo's control panel. These two charts count the calls arriving at each bank's core
   system."

   What's on the page, top to bottom:
   - A short explanation of what the queues and their limits do.
   - Two cards, **Core lakeshore** and **Core prairie**. Each starts with its queue's line, for example
     `bulkwire-core-lakeshore · routing.yaml 10/s, cap 8 per worker · override none · enforced 10/s (WORKER) ·
     4 pollers`, then a box **rps** with the buttons **Set override** and **Clear override**, then a chart with
     three lines: **calls/s** (yellow), **in-flight** (blue), and **failures/s** (red). Under each chart: "max
     in-flight · total · failed · 10 s avg".
   - **Payroll day launcher**, with **concurrency 4** and the button **Launch payroll day**.
   - **Fairness comparison (F48)**, with **Lakeshore queue 2/s** and **Launch comparison**.

   The charts show the last 60 seconds and only fill while this page is open, so keep it open.

**Say:** "Two banks, each with its own core system. Lakeshore has two big business customers; Prairie has one. All
three are about to submit payroll at the same time, through the same Temporal namespace and the same workers.
What differs is where the calls to each core system wait: every bank's core calls go through that bank's own task
queue, and each queue has its own rate limit."

---

## 1. Payroll day at the configured limit

1. **Control Center → Reset → Reset demo.** All eight steps say **ok**. Go back to **Core load & queues**.
2. Check both queue lines say **override none** and **enforced 10/s**.
3. Click **Launch payroll day.** Within a second, a result box lists three batches (Jordan's
   `summit-payroll-10000`, Taylor's `northline-payroll-10000`, Riley's `harvest-6000`), each with
   `"draftStatus": 202`, which means accepted.

   The launcher does what the three customers would do by hand: upload the file and click **Save as draft**,
   through the same API the portal uses. It's quicker than clicking through three windows live.

4. **Watch the charts** (in a test run):
   - **Core lakeshore** climbs to about **10 calls/s** with about **6 in flight** and stays there: two batches
     want more than that (four chunks each), and the queue's limit holds them to 10 a second.
   - **Core prairie** runs on its own queue, with **4 in flight**: one batch.
5. **Glance at the portal windows.** Each Home page shows its batch **Drafting** with a progress bar, then
   **Awaiting approval**. Riley's is done in about **6 seconds**, Jordan's and Taylor's in about **10 seconds**.

**Say:** "Every bank's core calls go through that bank's own queue, and the queue has a limit we set in
configuration: 10 calls a second here. Two customers at Lakeshore both want more than that, and Temporal hands
out Lakeshore's work at 10 a second, across both batches and every worker. Prairie's calls don't go through that
queue at all."

---

## 2. Turn Lakeshore down to 3 calls/s

1. **Control Center → Reset → Reset demo.** (Reset clears any override, so always set it *after* resetting.) Go
   back to **Core load & queues**.
2. On the **Core lakeshore** card, type **`3`** in **rps** and click **Set override**. The queue line changes to
   **override 3/s · enforced 3/s (API)**; Prairie's still says **10/s (WORKER)**.

   **Say:** "Lakeshore's core team asked us to go easy today. This is one setting on the Temporal Service,
   changed at runtime on Lakeshore's queue only. No code change and no redeploy." (The same thing from a terminal
   is `make ratelimit FI=lakeshore RPS=3`.)

3. Click **Launch payroll day** again.
4. **Watch the charts** (in a test run):
   - **Core lakeshore** goes flat at **3 calls/s** with about **2 in flight** (3 calls a second × 0.6 seconds
     each). It stays there until both batches are done.
   - **Core prairie** runs as before, on its own queue at its own limit.
5. **Watch the portal windows:**
   - **Riley (Window 3)** is done first, in about **8 seconds**.
   - **Jordan and Taylor (Windows 1 and 2)** move forward side by side, sharing Lakeshore's 3 calls a second, and
     reach **Awaiting approval** at about the same time, roughly **33 seconds** after launch.

**Say:** "Lakeshore's core system now sees a steady three calls a second, however many customers submit at once.
Both Lakeshore customers keep moving. And Prairie isn't stuck behind Lakeshore's backlog: its work is on a
different queue. The workflow code didn't change at all. The Temporal Service enforces this when it hands out
work."

> **What the limit is, precisely.** The rate limit caps how fast the Temporal Service *starts* Core calls on that
> queue, across every batch and every worker. It limits rate, not concurrency, which is why the in-flight count
> settles at about rate × call time. How many run at once is bounded separately: each batch keeps at most 4 chunks
> in flight, and each worker runs at most 8 of a queue's calls.

---

## 2b. A small batch on a busy queue

This answers the follow-up question: "If one bank's queue is busy with big batches, is a small one stuck behind
them?"

1. **Control Center → Reset → Reset demo.** Go back to **Core load & queues**.
2. In **Fairness comparison (F48)**, click **Lakeshore queue 2/s**. Lakeshore's line says **override 2/s**.
3. Click **Launch comparison.** It starts three of Jordan's `summit-payroll-5000` batches, then, 3 seconds later,
   Taylor's `northline-bonus-500`: 500 wires.
4. **Window 2 (Taylor):** the small batch reaches **Awaiting approval** in about **9 seconds**. **Window 1
   (Jordan):** the three big ones take about **47 seconds**.

**Say:** "Lakeshore's queue is backed up on purpose: the big batches have about a dozen calls waiting. Inside one
bank's queue, Temporal takes turns across batches (each batch is its own *fairness key*), so Taylor's small batch
gets its turn right away instead of waiting behind everything already queued. We measured it the other way too,
with every Lakeshore batch on one key: the small batch took about 22 seconds instead of 9."

Keep it honest if asked: Fairness doesn't make the queue faster; the big batches take the same time either way.
It only decides whose call goes next when there's a backlog. With no backlog it changes nothing.

**Say (close):** "Two big batches from two customers of one bank shared that bank's core capacity, a small batch at
the same bank wasn't stuck behind them, and the other bank never noticed. That's a task queue per bank with a rate
limit, and Fairness by batch inside it, all enforced by Temporal. What you don't need: custom throttling code, or a
coordinator that knows about every batch in flight."

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

- **Control Center → Reset → Reset demo.** It clears any queue override, ends any outage, and terminates running
  batches.
- If you skip the reset, at least click **Clear override** on the Core lakeshore card; otherwise the next scenario
  you run will be slow.

---

## If something goes wrong

| What you see | What to do |
|---|---|
| The charts are empty or start mid-run | They only record while the **Core load & queues** page is open. Open it before you click **Launch payroll day**. |
| Run 2 looks exactly like run 1 | The override is off: Reset clears it. Check the Core lakeshore line says **override 3/s**, then launch again. |
| A queue line says **0 pollers** | The workers aren't running. Check the worker chips are green; if not, **Reset demo**. |
| The launcher result shows an error instead of three batches | Check every chip in the status strip is green. If one isn't, **Reset demo** and try again. |
| A portal window doesn't show the new batch | Home refreshes every 3 seconds. Make sure the window is signed in as the right customer (Jordan, Taylor, or Riley) and is on **Home**. |
| Riley's sign-in fails | Riley is on the **Prairie** portal: http://localhost:**3002**, Company ID `HARVEST`. |
| Anything else | See part 1, [If something goes wrong](DEMO-GUIDE-HAPPY-PATH.md#if-something-goes-wrong). |
