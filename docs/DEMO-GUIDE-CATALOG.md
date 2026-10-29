# Demo guide, part 6: quick answers from the failure catalog

Parts [1](DEMO-GUIDE-HAPPY-PATH.md) to [5](DEMO-GUIDE-PRODUCTION.md) are scripted stories. This part is a set of
short recipes for the questions an audience asks *after* the story: "What if the file is wrong?", "What if nobody
approves?", "What if the bank's database is down for longer?". Each one takes a minute or two, and each matches a
card in the Control Center's **Failure catalog** (the card has the button, what you'll see, and a line to say).
The [index](#index-every-catalog-entry) at the end lists all 44 catalog entries and where each one is covered.

**Every recipe starts the same way** unless it says otherwise:

1. **Control Center → Reset → Reset demo.**
2. **Window 1 (Jordan, `SUMMIT` / `jordan`):** **Bulk Wire Upload** → drop `summit-payroll-5000.csv` (download it
   from **Sample files** if you need to). The review page says **Ready to draft**.

That's called **the standard start** below. Windows and sign-ins are as in part 1 (Jordan, Alex, Sam, Temporal UI)
and part 2 (Morgan, `BANK` / `morgan`). To find a catalog card, type its ID (for example `F28`) into the
**filter** box on **Control Center → Failure catalog**.

---

## "What if the file is wrong?"

### F25: an invalid file is caught before anything runs

1. **Control Center → Reset → Reset demo.**
2. **Window 1 (Jordan):** **Bulk Wire Upload** → **Sample files** → download **summit-vendors-with-errors.csv** →
   drop it on the upload box.
3. The review page says **Needs corrections**, **Errors 14**, with the banner "14 rows need attention before this
   batch can be drafted. Correct the file and upload it again." and an **Errors** table listing each row and what's
   wrong with it. **Download error report** gives the same list as a CSV. **Save as draft** is greyed out.

**Say:** "Bad files are caught at upload, synchronously, before anything is orchestrated. There's no workflow for
this batch, and there never will be: the customer fixes the file and uploads it again."

### F23: over the daily limit is an answer, not an error

1. **Control Center → Reset → Reset demo.**
2. **Window 1 (Jordan):** upload **summit-cold-chain-over-limit.csv** (from **Sample files**): 1,500 wires in 3
   groups. **Save as draft.**
3. A few seconds later: **Awaiting approval**, with **Drafted 300 / 1,500** and **Rejected 1,200**.
   - **Groups** table: account **••0033** is **Denied**: "Daily limit exceeded: $16.3M requested, $15.0M limit,
     $0.0M already used today". The other two groups are **Allowed**.
   - No tile is red and nothing needs attention: those 1,200 wires are recorded in the bank's system as Rejected.

**Say:** "The bank's core system said no to one group: that's a business answer, so the workflow records it and
moves on. Nothing retries, nothing parks, nobody gets paged. A policy denial is a result, not a failure."

### F17: the uploaded rows have disappeared

1. The standard start. Copy the batch ID: the last part of Window 1's address (26 letters and digits).
2. **Control Center → Failure catalog**, filter **`F17`**. Paste the ID into the **Batch ID (F17 expire, F35
   storm)** box at the top, then click **Expire staged rows**. This deletes the uploaded rows from the claim-check
   store, as if a retention rule had removed them.
3. **Window 1 (Jordan):** **Save as draft.** All 23 tiles turn red within seconds, and the pill shows **Needs
   attention**. Hover: "Needs attention: CLAIM_CHECK_NOT_FOUND — staged rows are no longer available (expired or
   never staged); cancel and re-upload".
4. **Window 5 (Morgan):** **Bulk wire exceptions** → the batch → **Cancel batch** on any card → **Confirm**. The
   batch is **Cancelled**; Jordan uploads the file again.

**Say:** "Retrying can't bring back data that's gone, so nothing retries forever. Every chunk stops and waits, and a
person decides. Here the right answer is to cancel and re-upload."

---

## "What if people change their minds?"

### F28: the customer cancels mid-draft

1. The standard start. Then **Control Center → Core load & fairness → Core lakeshore**: latency **`1500`** →
   **set**, so the draft lasts long enough.
2. **Window 1 (Jordan):** **Save as draft.** After a few tiles turn green, click **Cancel batch** (top right).
3. In **Cancel this batch?**, type a reason (for example "Wrong file") and click **Cancel batch**.
4. Chunks already in flight finish; nothing new starts. The pill says **Cancelled**, with the banner **"Cancelled
   by jordan: Wrong file"**. In a test run, about 1,950 wires had been drafted; the **Wires** tab shows them as
   **Cancelled** in the bank's system.

**Say:** "Cancel is an Update, and the workflow checks it's allowed before accepting it: you can cancel while
drafting or waiting for approval, never once release has started. In-flight work finishes cleanly, and the bank's
records say exactly which wires were cancelled."

### F30: an approver says no

1. The standard start, then **Save as draft** and wait for **Awaiting approval**.
2. **Window 2 (Alex):** open the batch → **Review and approve** → **Deny…** → type a reason (for example "Not
   expected this week") → **Deny batch**. The dialog says **"Batch denied."**
3. **Window 1 (Jordan):** the pill says **Denied**, with the banner **"Denied by alex: Not expected this week"**.
   **Wires** tab: 4,997 wires **Denied**, 3 Rejected.

**Say:** "One denial closes the batch everywhere: in the bank's system, in the workflow, and on the customer's
screen. The bank's core system made the decision; the workflow carried it out."

### F31: nobody approves in time

1. **Control Center → Reset → Reset demo.** Then **Control Center → Draft defaults → lakeshore**: set
   **approvalWindow** to **`2m`** → **Save**. (Defaults apply to the *next* batch that starts; running batches
   never change.)
2. The standard start (without the reset), then **Save as draft.**
3. At **Awaiting approval**, the line under the title shows **Expires in 1 m 59 s**, counting down.
4. Wait. Two minutes after the draft finished, the pill says **Expired**, and the **Timeline** ends with
   **expired**. **Wires** tab: 4,997 **Expired**, 3 Rejected.

**Say:** "The approval deadline is a durable timer inside the batch. There's no cron job and no sweeper looking for
stale batches: the workflow itself wakes up at the deadline, tells the bank's core system, and closes."

**Reset demo** afterwards: it puts the approval window back to 24 hours.

### F32: a late approval can't bring it back

Do this together with F31.

1. Before the two minutes are up, **Window 2 (Alex)** opens the batch → **Review and approve**, types `246810`, and
   **waits** with the dialog open.
2. After the batch expires, Alex clicks **Approve**. The dialog shows: **"batch is closed (Expired); it can no
   longer be approved"**.

The same happens after a denial: "batch is closed (Denied); it can no longer be approved". (Outside the dialog, the
portal stops offering **Review and approve** once a batch is closed.)

**Say:** "The bank's core system refuses a late approval, and even if a stray approval message reached the
workflow service, it couldn't restart a closed batch: a batch's workflow ID can never be reused once it has
closed."

### F33: approval arrives before the draft

1. The standard start. **Don't** click Save as draft.
2. **Window 2 (Alex):** copy the address from Jordan's window into Alex's window. (**Approvals** can't list this
   batch yet: there's no workflow for it until someone acts.) Alex sees the same review page with an **Approve &
   release** button: click it → `246810` → **Approve**: "1 of 2 approvals".
3. **Window 3 (Sam):** the same → **"Approved (APR-…). Release started."**
4. **Window 1 (Jordan):** the batch drafts *and* releases in one go: **Drafting**, then **Releasing**, then
   **Authorized**, 4,997 / 5,000. The **Timeline** starts with **authorization received**, before **plan ready**.

**Say:** "Approval and draft can arrive in either order. Whichever arrives first starts the one workflow for this
batch, and the other joins it. It's the same workflow ID either way, so there can't be two."

### F22: a hold placed after approval

1. The standard start, then **Save as draft** and wait for **Awaiting approval**.
2. **Window 5 (Morgan):** **Accounts** → customer **Summit Logistics, Inc.** → **••0033** → **Place hold**.
3. **Windows 2 and 3:** Alex and Sam approve.
4. **Window 1:** the pill says **Releasing**, and the 7 ••0033 tiles turn red; the rest release. In Morgan's
   **Bulk wire exceptions**, the cards offer **Retry chunk** and **Reject chunk wires**, but no **Cancel batch**:
   the batch is already approved.
5. **Morgan:** **Release hold**, then **Retry chunk** on each card → **Authorized**, 4,997 / 5,000.

**Say:** "Same park-and-resolve as during the draft, now after approval. The only difference: once money is
approved for release, cancelling the whole batch is off the table, and the workflow enforces that."

### F27: the operator rejects a parked chunk

See [part 2, B4](DEMO-GUIDE-FAILURES.md#b4-morgan-fixes-it), "Reject one chunk instead of retrying it".

---

## "What about duplicates?"

### F34: two tabs, two clicks

1. The standard start. In Window 1, right-click the tab → **Duplicate**. (A duplicated tab keeps the sign-in; a
   fresh tab would ask you to sign in again.)
2. Click **Save as draft** in both tabs, quickly.
3. Both tabs show the same live batch. **Control Center → Batches** lists one execution for it.

**Say:** "Two clicks, one workflow. The workflow ID is the batch ID, so a second click can only join the batch
that's already running."

### F35: twenty requests at once

1. **Control Center → Core load & fairness → Retry storm (F35) → Fire retry storm.** It uploads a fresh
   `summit-payroll-5000` batch as Jordan, then sends 20 **Save as draft** requests for it at the same moment, each
   with a different idempotency key.
2. The result box shows `"statusCounts": {"202": 20}` (all twenty accepted) and `"executions": 1`.

**Say:** "Twenty requests, each with a different idempotency key, as if twenty retries leaked out of a load
balancer. Still exactly one workflow."

### F19: the approval reaches the bank, but the message to the workflow is lost

1. The standard start, then **Save as draft** and wait for **Awaiting approval**.
2. **Control Center → Failure catalog**, filter **`F19`** → **Callback fail ×1**. The bank's next message to the
   workflow service will fail.
3. **Window 2 (Alex)** approves as usual. **Window 3 (Sam)** approves: the dialog says **"Approval recorded.
   Release notification failed —"** with a **Retry** link. Jordan's batch is still **Awaiting approval**.
4. Sam clicks **Retry**. The dialog changes to **"Approved (APR-…). Release started."**, and the batch releases.

**Say:** "The approval was safe in the bank's system the whole time; only the notification was lost. Re-sending is
safe because the approval reference *is* the Update ID: the workflow accepts it once, whatever happens."

### F20: the same approval delivered twice

This is F19's **Retry** seen from the other side. When the bank re-sends an approval, it can't know whether the
first message got through. In a test, re-sending an approval that *had* already been delivered got **"Approved"**
back again, and the batch's **Timeline** still had exactly one **authorization received**. One approval, one
release, however many times it's delivered.

---

## "What if the outage is long?"

### F26: the retry budget runs out

1. **Control Center → Reset → Reset demo.** Then **Failure catalog**, filter **`F26`** → **Lakeshore retry
   budget 60 s**. (The batch's retry budget is fixed when it starts; the default is 30 minutes.)
2. **Core load & fairness → Core lakeshore:** latency **`1500`** → **set**.
3. The standard start (without the reset), then **Save as draft.**
4. When a few tiles are green: **Failure catalog → F26 → Outage 120 s.**
5. The two in-flight tiles turn amber and retry for a minute. Then they turn **red**: hover shows
   "RETRY_BUDGET_EXHAUSTED … retry budget of 1m0s exhausted; last error: CORE_UNAVAILABLE: Core is unavailable…".
   The rest of the tiles wait, dashed.
6. After the outage ends (two minutes after step 4), **Window 5 (Morgan):** **Bulk wire exceptions** → the batch →
   **Retry chunk** on each card. The batch reaches **Awaiting approval**, 4,997 / 5,000.

**Say:** "The retry budget is measured in time, not attempts. When it runs out, the chunk doesn't fail and it
doesn't go to a dead-letter queue: it parks, with its last error, and waits for someone who knows the outage is
over."

If the outage starts *before* the draft, the batch's very first call to the bank's system is the one that runs out
of budget. Then Jordan's page shows **"Waiting for bank operations: gentxbatch — RETRY_BUDGET_EXHAUSTED: …"**, and
Morgan's card is **Batch step (gentxbatch)** with a **Retry step** button. Same idea, one level up.

### F44: an operator pauses the retries

Narrated. During a long outage, an operator can pause one Activity's retries from the CLI without touching the
rest of the batch:

```bash
temporal activity pause   --namespace bulkwire -w <workflow id> --activity-id <activity id>
temporal activity unpause --namespace bulkwire -w <workflow id> --activity-id <activity id>
```

The Activity ID is on each tile's tooltip (for example `draft/g01-c03/r0`). The pause takes effect after the
current attempt, because these Activities don't heartbeat. The CLI marks this command as experimental.

### F42: on-call finds out from a metric

See [part 2, A3](DEMO-GUIDE-FAILURES.md#a3-watch-it-retry) (**Alerts & metrics**).

---

## "What about other infrastructure failures?"

All of these use the standard start. The faults that count calls (F02, F05, F06) are armed *before* you click
**Save as draft**; the rest are clicked while the draft runs, with Lakeshore latency at **`1500`** (**Core load &
fairness**) so it lasts long enough. Every one ends at **Awaiting approval** with **4,997 drafted and 3 rejected**:
the same as a run with no failure.

| ID | Button (Control Center → Failure catalog) | What you'll see | Say |
|---|---|---|---|
| **F02** SQL timeout | **SQL timeout ×3** (click before Save as draft) | Three calls each wait **30 seconds** for a real SQL Server timeout, then fail "SQL_TIMEOUT … (HTTP 504)" and are retried. Drafted in about a minute. | "That was a real SqlClient timeout. The chunk's 60-second deadline sits just above the database's 30, so a slow statement is retried, not abandoned." |
| **F03** Core hangs | **Pause core-lakeshore 90 s** | The running tiles sit for 60 seconds, then retry: "activity StartToClose timeout". They succeed after the unpause. | "A frozen process never says it failed. The 60-second deadline turns a hang into an ordinary retry." |
| **F04** Core crashes | **Kill core-lakeshore**, then **Start core-lakeshore** about 10 s later | Calls fail and retry; the draft finishes about 10 seconds after the start. The **Wires** tab still says 4,997 drafted, 3 rejected: no duplicates. | "Core may have committed part of a chunk before it died. The retry is safe because every write is idempotent by wire ID." |
| **F05** Response lost | **Lose responses ×2** (click before Save as draft) | Two chunks are written, but their answers are thrown away; each is retried, and the retry finds the rows already there. Totals exact. | "Core committed but the answer was lost. The retry saw the rows already present and reported the true totals." |
| **F06** Generic Core bug | **Error 500 ×3** (click before Save as draft) | A few retries; drafted in about 15 seconds. | "A transient server error is just another retry." |
| **F07** Core slow | **Latency 2000 ms**, then **Back to 600 ms** | Tiles take longer. Nothing fails. | "Slow isn't broken. The timeouts are sized to the bank's system, so a slow core just takes longer." |
| **F09** Database down | **Stop sql-lakeshore 60 s** | Tiles retry with "CORE_UNAVAILABLE: Core database is temporarily unavailable (HTTP 503)". The **SQL Lakeshore** chip goes red. The draft finishes within about half a minute of the database coming back. | "The bank's database went away and came back; the batch waited and carried on." |
| **F14** Rolling restart | **Restart worker-a**, then **Restart worker-b** | Nothing visible: each worker finishes its in-flight chunks before it restarts. | "Deploying workers is a non-event." |
| **F18** API down | **Stop bulkwire-service 30 s** | The batch page says "Batch details are temporarily unavailable. Try again in a moment." When the service is back, the batch is much further along: in a test, 499 wires drafted before, 4,997 after. | "The API is just a door. The work carried on without it, because the work lives in Temporal." |

---

## "How do we tune it and test it?"

### F37: parallelism is a setting

1. **Control Center → Reset → Reset demo.** Then **Draft defaults → lakeshore**: **concurrency** **`1`** →
   **Save**. **Core load & fairness → Core lakeshore → reset stats.**
2. The standard start (without the reset), then **Save as draft.** In a test run it took **16 seconds**, and the
   Lakeshore card said **max in-flight 1**.
3. Reset, set concurrency **`4`**, reset stats, and draft again: **6 seconds**, **max in-flight 4**.

**Say:** "How many chunks run at once is one number in the batch's settings, enforced by a semaphore inside the
workflow. It's not an architecture decision. And note the demo's Core latency is fixed per call: whether more
parallelism helps a real core system is something you measure, not assume."

### F39: catching a breaking change before it ships

Narrated, from a terminal in the repository folder:

```bash
make replay       # replays two captured production-like histories against the current code: passes
make replay-nde   # replays them against a deliberately reordered workflow: fails with a non-determinism error
```

**Say:** "Every pull request replays real histories against the new code. If a change would break batches already
in flight, the build fails before anyone deploys it."

### F11: a SQL deadlock

Narrated. SQL Server error 1205 (deadlock victim) is classified as a temporary failure, so the chunk is retried
like an outage. The unit test `Transport_and_transient_errors_are_503_and_retryable` in
`core/CoreApi.Tests/SqlErrorMapperTests.cs` covers it.

---

## Index: every catalog entry

| ID | Catalog entry | Where |
|---|---|---|
| F01 | Core outage (503) | [Part 2, Scenario A](DEMO-GUIDE-FAILURES.md#scenario-a-core-goes-down-mid-draft) |
| F02 | SQL timeout (real 30 s) | [Part 6, other infrastructure failures](#what-about-other-infrastructure-failures) |
| F03 | Core hangs (process frozen) | [Part 6, other infrastructure failures](#what-about-other-infrastructure-failures) |
| F04 | Core crashes mid-request | [Part 6, other infrastructure failures](#what-about-other-infrastructure-failures) |
| F05 | Response lost after commit | [Part 6, other infrastructure failures](#what-about-other-infrastructure-failures) |
| F06 | Generic Core bug (500) | [Part 6, other infrastructure failures](#what-about-other-infrastructure-failures) |
| F07 | Core slow | [Part 6, other infrastructure failures](#what-about-other-infrastructure-failures) |
| F08 | One FI's Core down, other FI busy | [Part 3, step 3](DEMO-GUIDE-PAYROLL-DAY.md#3-optional-one-bank-down-the-other-unaffected) |
| F09 | Database down | [Part 6, other infrastructure failures](#what-about-other-infrastructure-failures) |
| F10 | Database hang | [Part 4, step 4](DEMO-GUIDE-CHAOS.md#4-freeze-the-banks-database-for-40-seconds) |
| F11 | Deadlock victim (1205) | [Part 6, F11](#f11-a-sql-deadlock) |
| F12 | Worker crashes mid-batch | [Part 4, step 1](DEMO-GUIDE-CHAOS.md#1-crash-a-worker) |
| F13 | All workers down | [Part 4, step 2](DEMO-GUIDE-CHAOS.md#2-stop-all-the-workers-for-60-seconds) |
| F14 | Rolling restart | [Part 6, other infrastructure failures](#what-about-other-infrastructure-failures) |
| F15 | Temporal Service restart | [Part 4, step 3](DEMO-GUIDE-CHAOS.md#3-restart-the-temporal-service) |
| F16 | Claim-check store down | [Part 4, step 5](DEMO-GUIDE-CHAOS.md#5-take-the-claim-check-store-down-for-45-seconds) |
| F17 | Staged data expired | [Part 6, F17](#f17-the-uploaded-rows-have-disappeared) |
| F18 | bulkwire-service down | [Part 6, other infrastructure failures](#what-about-other-infrastructure-failures) |
| F19 | Approval notification lost | [Part 6, F19](#f19-the-approval-reaches-the-bank-but-the-message-to-the-workflow-is-lost) |
| F20 | Duplicate approval callback | [Part 6, F20](#f20-the-same-approval-delivered-twice) |
| F21 | Account hold during draft | [Part 2, Scenario B](DEMO-GUIDE-FAILURES.md#scenario-b-a-person-has-to-fix-it) |
| F22 | Hold after draft, before authorization | [Part 6, F22](#f22-a-hold-placed-after-approval) |
| F23 | Daily limit exceeded | [Part 6, F23](#f23-over-the-daily-limit-is-an-answer-not-an-error) |
| F24 | Blocked beneficiary | [Part 1, Phase 4 (every payroll run)](DEMO-GUIDE-HAPPY-PATH.md#phase-4-save-as-draft-and-watch-it-run-jordan) |
| F25 | Invalid file | [Part 6, F25](#f25-an-invalid-file-is-caught-before-anything-runs) |
| F26 | Retry budget exhausted | [Part 6, F26](#f26-the-retry-budget-runs-out) |
| F27 | Operator rejects a parked chunk | [Part 2, B4 (variation)](DEMO-GUIDE-FAILURES.md#b4-morgan-fixes-it) |
| F28 | Cancel mid-draft | [Part 6, F28](#f28-the-customer-cancels-mid-draft) |
| F29 | Dual control | [Part 1, Phases 5–6; Part 2, B5](DEMO-GUIDE-HAPPY-PATH.md#phase-5-first-approval-alex) |
| F30 | Denied | [Part 6, F30](#f30-an-approver-says-no) |
| F31 | Approval never arrives | [Part 6, F31](#f31-nobody-approves-in-time) |
| F32 | Late approval after close | [Part 6, F32](#f32-a-late-approval-cant-bring-it-back) |
| F33 | Approve before draft | [Part 6, F33](#f33-approval-arrives-before-the-draft) |
| F34 | Double click / two tabs | [Part 6, F34](#f34-two-tabs-two-clicks) |
| F35 | Retry storm | [Part 6, F35](#f35-twenty-requests-at-once) |
| F36 | Payroll day, two users at one FI | [Part 3, steps 1–2](DEMO-GUIDE-PAYROLL-DAY.md#2-payroll-day-with-a-per-bank-limit) |
| F37 | Concurrency is a setting | [Part 6, F37](#f37-parallelism-is-a-setting) |
| F38 | Shipped a workflow bug | [Part 5, Scenario F](DEMO-GUIDE-PRODUCTION.md#scenario-f-we-shipped-a-bug) |
| F39 | Non-determinism before deploy | [Part 6, F39](#f39-catching-a-breaking-change-before-it-ships) |
| F40 | Data at rest | [Part 5, Scenario E](DEMO-GUIDE-PRODUCTION.md#scenario-e-temporal-never-sees-the-data) |
| F41 | See the whole wire flow | [Part 5, E4](DEMO-GUIDE-PRODUCTION.md#e4-the-whole-story-in-one-history) |
| F42 | Retry alerting | [Part 2, A3](DEMO-GUIDE-FAILURES.md#a3-watch-it-retry) |
| F43 | Payload and history limits | [Part 5, E4](DEMO-GUIDE-PRODUCTION.md#e4-the-whole-story-in-one-history) |
| F44 | Operator pauses retries during a long outage | [Part 6, F44](#f44-an-operator-pauses-the-retries) |
