# Demo guide, part 4: the chaos tour

This guide follows parts [1](DEMO-GUIDE-HAPPY-PATH.md), [2](DEMO-GUIDE-FAILURES.md), and
[3](DEMO-GUIDE-PAYROLL-DAY.md). It answers the skeptic in the room: **"Fine, but what if the orchestration itself
breaks?"**

While one 10,000-wire batch is being drafted, you break almost every moving part in turn: crash a worker, stop
all the workers, restart the Temporal Service, freeze the bank's database, and take down the store that holds the
uploaded rows. At the end, the bank's records are exact: every wire written once, none lost, none doubled.

Allow about **5 minutes** in front of an audience. The steps run back to back: don't wait for each one to recover
before starting the next, or the batch will finish before the tour does.

| Phase | What you do | Roughly when |
|---|---|---|
| [Before you start](#before-you-start) | Windows, and the Control Center on the catalog | Before the meeting |
| [0. Start a slow, big draft](#0-start-a-slow-big-draft) | 10,000 wires, 5 s per chunk | 0:00 |
| [1. Crash a worker](#1-crash-a-worker) | Kill worker-a | 0:10 |
| [2. Stop all workers](#2-stop-all-the-workers-for-60-seconds) | Both workers gone for 60 s | 0:30 |
| [3. Restart Temporal](#3-restart-the-temporal-service) | The Temporal Service restarts | 2:00 |
| [4. Freeze the database](#4-freeze-the-banks-database-for-40-seconds) | Real 30-second SQL timeouts | 2:20 |
| [5. Lose the claim-check store](#5-take-the-claim-check-store-down-for-45-seconds) | The uploaded rows are unreachable | 3:10 |
| [6. Count the wires](#6-count-the-wires) | Exactly 9,994 drafted, 6 rejected | about 5:00 |
| [Afterwards](#afterwards) | Reset | After |

---

## Before you start

1. **Do part 1, Phases 0–2.** You need **Window 1 (Jordan)** and **Window 4 (Temporal UI)**. Optional: turn on
   engineering annotations in Window 1 (part 1, Phase 7), so you can open the batch in the Temporal UI with one
   click.
2. **Download `summit-payroll-10000.csv`:** Window 1 → **Bulk Wire Upload** → **Sample files** → **Download** on
   that row.
3. **Control Center:** open **Failure catalog**. You'll type a catalog ID (F12, F13, …) into the **filter** box at
   each step to bring up that card's button.

> **Important:** the timed actions in this tour ("stop for 60 s", "pause for 40 s") are undone *by the Control Center
> browser tab*, which shows a countdown such as **"worker-a + worker-b: start in 60 s"**. Moving between Control
> Center sections is fine, but **don't reload or close that tab** while a countdown is running. If you do,
> **Reset demo** starts and unpauses everything.

**Say:** "Parts 1 to 3 broke things *around* the workflow: the bank's systems and the bank's customers. Now let's
break the platform itself, while it's in the middle of 10,000 wires."

---

## 0. Start a slow, big draft

1. **Control Center → Reset → Reset demo.** All eight steps **ok**.
2. **Control Center → Core load & queues → Core lakeshore:** type **`5000`** in **latency ms**, click **set**.
   Each chunk now takes 5 seconds in Core, so the draft has about two minutes of real work: enough to break things
   in the middle of it.
3. **Window 1 (Jordan):** **Bulk Wire Upload** → drop `summit-payroll-10000.csv`. **Ready to draft**: 10,000
   wires, $26,662,899.53.
4. Click **Save as draft.** Two tiles turn blue at a time; every 5 seconds two more turn green.

**Say:** "One batch, one workflow, 40 chunks, two at a time. Watch the tiles. Everything I do next happens while
they're moving."

## 1. Crash a worker

**Control Center:** filter **`F12`** → **Kill worker-a**. In the status strip, the **worker-a** chip goes red.
worker-a is killed, not shut down: it gets no chance to finish anything or say goodbye.

**Window 1 (Jordan):**
- Any tile that was running *on worker-a* stays blue with no progress. Nobody is working on it any more, but
  Temporal doesn't know that yet.
- If worker-b had the other chunk, that tile carries on.
- After **60 seconds** with no answer, Temporal gives the stuck chunks to another worker: they turn amber
  (**retry #2**) and then green. Hover over one: "last failure: activity StartToClose timeout". (You'll be in
  step 2 by then, so you'll see this when the workers come back.)

**Say:** "A crashed worker can't report that it crashed. So every chunk has a deadline: 60 seconds to finish,
because the bank's database gives up after 30. When that passes with no answer, Temporal hands the chunk to another
worker. The bank's procedures are idempotent by wire ID, so even if the dead worker had half-written a chunk, the
retry can't double anything."

Don't wait for the 60 seconds. Move on after about 15 seconds of talking.

## 2. Stop all the workers for 60 seconds

**Control Center:** filter **`F13`** → **Stop both workers 60 s**. A countdown appears: **"worker-a + worker-b:
start in 60 s"**. (This also brings worker-a back at the end.)

**Window 1 (Jordan):** within a few seconds:
- A banner: **"Live progress is temporarily unavailable. Your batch is safe and will continue automatically.
  Current status: DRAFTING."**
- The page dims, and the **Live** dot disappears. Nothing moves.

**Say:** "There's no code running anywhere for this batch right now. Zero workers. Look at what the portal still
knows: the batch, its status, that it's safe. That comes from Temporal, not from a worker, because the batch's
state lives in Temporal. The live progress view needs a worker to answer, so it's honestly marked unavailable."

This was a clean shutdown, unlike step 1: each worker stopped taking new work and finished the chunks it already
had (it allows up to 8 seconds) before exiting. So no chunk is left hanging this time.

When the countdown ends, the workers start. Within a few seconds the banner goes, the page brightens, and the
tiles pick up exactly where they stopped. The chunk worker-a was running when you killed it in step 1 timed out
while the workers were away, so it comes back as **retry #2** and finishes like the rest.

**Say:** "Nothing was lost and nothing restarted from zero. The workers came back and carried on from the last
completed step. That's also what a deployment looks like: workers drain and restart, and batches don't notice."

## 3. Restart the Temporal Service

**Control Center:** filter **`F15`** → **Restart temporal**.

The whole Temporal Service (the server, its database, the Web UI) restarts. In Window 1 you may see the banner for
a moment, then progress continues. **Window 4 (Temporal UI)** needs a page reload once the service is back.

**Say:** "That was the orchestrator itself, restarting mid-batch. Every batch resumed from its history. In this
demo it's a single dev server that saves to disk; Temporal Cloud or a production cluster is replicated, so you'd
never even see the blip."

## 4. Freeze the bank's database for 40 seconds

**Control Center:** filter **`F10`** → **Pause sql-lakeshore 40 s**. Countdown: **"sql-lakeshore: unpause in
40 s"**.

The database process is frozen, not stopped: connections stay open, but nothing answers. That's the nastiest kind
of failure, because nobody gets an error.

**Window 1 (Jordan):**
- For about **30 seconds**, the running tiles just sit there. The calls are waiting on a frozen database.
- Then they turn amber. Hover: **"SQL_TIMEOUT: Core database did not answer within the SQL command timeout
  (HTTP 504)"**. That's a real SQL Server timeout, not a simulated one.
- They keep retrying until the countdown ends and the database wakes up, then continue.

**Say:** "A hang is worse than a crash. The bank's API gives up on the database after 30 seconds and says so. The
chunk's 60-second deadline sits just above that, so a slow statement gets retried, not abandoned and not left
hanging forever."

## 5. Take the claim-check store down for 45 seconds

**Control Center:** filter **`F16`** → **Stop claim-check backend 45 s**. Countdown: **"redis: start in 45 s"**.

The claim-check store holds the 10,000 uploaded rows. The workflow never carries the rows themselves, only
references such as "chunk g01-c02: these 250 rows". Each chunk reads its rows from this store when it runs.

**Window 1 (Jordan):** within seconds, the running tiles turn amber and the retry count climbs (**retry #5**,
**#6**, **#7**…). Hover: **"claim-check store unavailable: …"**. When the store is back, the next retry succeeds,
within about 20 seconds, and the tiles turn green again.

**Say:** "The rows were unreachable for 45 seconds, and each chunk just waited. The store is read-only after upload
and saves to disk, so when it came back, every row was still there, unchanged."

## 6. Count the wires

**Window 1 (Jordan):** about **5 minutes** after you clicked Save as draft, the pill says **Awaiting approval**:

- **Drafted 9,994 / 10,000 · Rejected 6.** The 6 are the bank's screening rejections, the same as any run of this
  file.
- **Wires (bank records)** tab: **"9,994 drafted, 6 rejected"**, straight from the bank's database. Not one extra,
  not one missing.

Then open the batch in the Temporal UI (**Open in Temporal UI**, or Window 4 → reload → the top row):
- It is **one execution**, about **270 events**, about **70 KB** of history. Nothing was restarted, and there's no
  second copy.
- The **Timeline** shows the chunks that were retried, with their attempt counts.

**Say (close):** "We crashed, stopped, froze, and restarted every moving part during a 10,000-wire draft, and the
bank's records came out exact. That's durable execution: the state lives in Temporal, every write to the bank is
idempotent, and timeouts turn hangs into retries. What you don't need: recovery state kept outside the orchestrator,
and a runbook for cleaning up after a crash."

---

## Afterwards

- **Control Center → Reset → Reset demo.** It removes the 5-second latency, starts or unpauses anything still down,
  and terminates the batch.

---

## If something goes wrong

| What you see | What to do |
|---|---|
| The batch finished before you got to step 5 | You waited between steps. Keep moving: each step's recovery happens in the background while you start the next. You can also raise the latency to `8000` for more slack. |
| A countdown never finished, and something is still stopped or paused | The Control Center tab was reloaded during the countdown. **Overview** → **start** or **unpause** that container, or just **Reset demo**. |
| The worker-a chip stays red after step 1 | That's expected until step 2's countdown ends: step 2 starts both workers. To bring it back sooner: **Overview** → worker-a → **start**. |
| The Temporal UI shows an error after step 3 | Reload the page once the service is back (a few seconds). |
| Window 1 shows the banner long after the workers came back | Give it a few seconds; it clears on the next successful update. If it stays more than 30 seconds, check both worker chips are green. |
| The totals aren't 9,994 / 6 | Check the file: these numbers are for `summit-payroll-10000.csv`. With `summit-payroll-5000.csv` they're 4,997 / 3. |
| Anything else | See part 1, [If something goes wrong](DEMO-GUIDE-HAPPY-PATH.md#if-something-goes-wrong). |
