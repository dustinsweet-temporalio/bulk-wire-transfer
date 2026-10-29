# Demo guide, part 5: running it in production

This guide follows parts [1](DEMO-GUIDE-HAPPY-PATH.md) to [4](DEMO-GUIDE-CHAOS.md). It's for the platform,
security, and engineering people in the room, and it answers two questions they always ask:

- **Scenario E: "Can Temporal see our customers' data?"** No. Every payload is encrypted before it leaves your
  process. You'll show the ciphertext Temporal stores, decode it on your side, and rotate the key live.
- **Scenario F: "What happens when we ship a bug?"** A broken release stops only the new batch that ran into it,
  safely, with all its progress kept. A fix moves just that batch forward, and batches on the old version never
  notice.

Allow about **3 minutes** for E and **4 minutes** for F.

| Phase | What you do | When |
|---|---|---|
| [Before you start](#before-you-start) | Windows; optionally the Temporal CLI | Before the meeting |
| **Scenario E** | | |
| [E1. One batch, encrypted](#e1-draft-a-batch) | Draft, then look at what Temporal stores | Live |
| [E2. Rotate the key](#e2-rotate-the-key) | New key; new batch; both still readable | Live |
| [E3. The CLI view](#e3-the-same-thing-from-the-cli-optional) | Ciphertext vs decoded, side by side | Live, optional |
| [E4. The whole story in one history](#e4-the-whole-story-in-one-history) | The Temporal UI, decoded | Live |
| [E5. Put the key back](#e5-put-the-committed-key-back) | Restore the committed keyring | After |
| **Scenario F** | | |
| [F1. A batch on today's release](#f1-a-batch-on-todays-release) | Batch A, drafted on v1 | Live |
| [F2. Ship the bug](#f2-ship-a-release-with-a-bug) | Start v2, make it current | Live |
| [F3. Hit the bug](#f3-a-new-batch-runs-into-the-bug) | Batch B, approved, stops safely | Live |
| [F4. Old batches don't care](#f4-the-old-batch-is-fine) | Batch A authorizes on v1 | Live |
| [F5. Ship the fix](#f5-ship-the-fix-and-move-only-the-broken-batch) | v3, then move B onto it | Live |
| [Afterwards](#afterwards) | Reset | After |

---

## Before you start

1. **Do part 1, Phases 0–2:** you need **Window 1 (Jordan)**, **Window 2 (Alex)**, **Window 3 (Sam)**, and
   **Window 4 (Temporal UI)**. Turn on engineering annotations in Window 1 (part 1, Phase 7) for the **Open in
   Temporal UI** button.
2. **Have `summit-payroll-5000.csv`** in your Downloads folder (from part 1).
3. **Optional, for E3:** the Temporal CLI on your laptop (`brew install temporal` on a Mac). If you don't have it,
   E3 shows how to run it inside the demo's Temporal container instead.

---

# Scenario E: Temporal never sees the data

**The story:** a wire batch is full of account numbers, names, and amounts. Before any of it leaves the worker or
the API, it's encrypted with AES-256-GCM, using a key the bank holds. Temporal stores and moves ciphertext. When an
engineer needs to read a batch, their browser asks the bank's own *Codec Server* to decrypt it.

## E1. Draft a batch

1. **Control Center → Reset → Reset demo.**
2. **Window 1 (Jordan):** **Bulk Wire Upload** → drop `summit-payroll-5000.csv` → **Save as draft**. About 10
   seconds later: **Awaiting approval**. Leave it there.
3. **Control Center → Encryption.** The **Keyring** card lists one key: **`k2026a`**, status **active**.
4. In **Payload inspector: stored ciphertext vs decoded**, open the **pick a batch…** list and choose the top entry
   (`bulkwire/lakeshore/<batch id> · DRAFTED`). Click **Inspect**.

Each row is one payload from the batch's history, shown twice:
- **Left, "As stored by the Temporal Service":** for example **WorkflowExecutionStarted input**, then
  **encoding binary/encrypted · key k2026a · 516 bytes**, and a block of base64 ciphertext.
- **Right, "Decoded with the keyring":** the same payload as readable JSON: batch ID, customer, file name, row
  count, total amount.

**Say:** "On the left is exactly what Temporal has: ciphertext, plus the ID of the key that encrypted it, and
nothing else. On the right is the same bytes, decrypted with a key that never left our side. And this isn't one
special field: every input, every result, and every failure detail is encrypted this way."

## E2. Rotate the key

1. **Control Center → Encryption → Keyring → Rotate key.** A second key appears, named after the current time (for
   example **`k20261029-201737`**), marked **active**. **`k2026a`** changes to **decrypt only**.

   Every worker and service picks up the new keyring within about 2 seconds, without a restart.

2. **Window 1 (Jordan):** upload `summit-payroll-5000.csv` again and **Save as draft**. Wait for **Awaiting
   approval**.
3. **Control Center → Encryption:** reload the page, pick the **new** top batch, **Inspect**. Its payloads say
   **key k2026…** with the new key's ID.
4. Pick the **first** batch again (the second entry in the list), **Inspect**. It still says **key k2026a**, and it
   still decodes.

**Say:** "New work uses the new key; old histories keep the key they were written with, and they're still
readable, because old keys stay in the keyring for decryption only. That's key rotation without re-encrypting
anything and without a maintenance window."

## E3. The same thing from the CLI (optional)

Copy the first batch's workflow ID from the inspector's list (it looks like `bulkwire/lakeshore/01M6…`). In a
terminal:

```bash
# What Temporal stores: base64 ciphertext and the key ID
temporal workflow show -n bulkwire -w <workflow id> --detailed | head -20

# Decoded through the bank's Codec Server
temporal workflow show -n bulkwire -w <workflow id> --detailed --codec-endpoint http://localhost:8089 | head -20
```

The first shows lines like `input.payloads[0].data: ZJPWH9Ov…` and `metadata.encoding: YmluYXJ5L2VuY3J5cHRlZA==`
(that's "binary/encrypted", base64-encoded). The second shows `input[0].BatchID: …`, `input[0].RowCount: 5000`, and
so on.

Don't have the CLI installed? Run the same commands inside the demo's Temporal container:

```bash
docker compose exec temporal temporal workflow show -n bulkwire --address 127.0.0.1:7233 \
  -w <workflow id> --detailed | head -20
docker compose exec temporal temporal workflow show -n bulkwire --address 127.0.0.1:7233 \
  -w <workflow id> --detailed --codec-endpoint http://codec-server:8089 | head -20
```

`--detailed` matters: without it, the CLI lists event types only and shows no payloads at all.

## E4. The whole story in one history

**Window 1 (Jordan):** **Open in Temporal UI** on either batch.

| Where | What it shows | Say |
|---|---|---|
| **Input** panel | Readable JSON | "The Web UI decodes through the same Codec Server. Your browser calls it directly, so the key never goes near Temporal." |
| **Timeline** | Every chunk as a labeled Activity ("Draft chunk g01-c02 · 250 wires · ••0017 · 2026-10-31"), the **Approval window (24h)** timer | "The labels are encrypted too, and decoded here." |
| **Event History** | The `draft` Update with who sent it; each Activity with its input and result | "One batch, one readable history, from upload to release." |
| Header: **History Size** | About 60 KB for this drafted batch | "5,000 wires, but the 2 MB of uploaded rows never enter Temporal. Only references and counts do. That's the claim-check pattern." |

## E5. Put the committed key back

Rotating wrote a new key into `config/keyring.json` in your copy of the repository. Put the committed keyring back
so you don't commit a demo key by accident. **Order matters:**

1. **Control Center → Reset → Reset demo** *first*. It terminates the running batches, including the one encrypted
   with the temporary key.
2. Then, in the repository folder:
   ```bash
   git checkout config/keyring.json
   ```
3. Within about 2 seconds, **Control Center → Encryption** shows only **`k2026a`**, **active**.

Batches drafted with the temporary key can no longer be decoded afterwards, which is expected: that key is gone.

**Say (close):** "The orchestrator only ever saw ciphertext, yet engineers can read every batch end to end. That's
an encrypting data converter plus a Codec Server, and a claim check for the bulk data. What you don't need:
plaintext customer data in your orchestration platform, or workarounds for history size limits."

---

# Scenario F: we shipped a bug

**The story:** your team deploys a new worker release, and it has a bug in the authorization step. In many
systems, every in-flight batch would pick up the new code and fail halfway. With Temporal's Worker Versioning, each
batch stays *pinned* to the release it started on. Only new batches run the new code, and when one of them hits the
bug, it stops safely and waits for a fix.

The Control Center's **Versioning** page has the steps as numbered buttons. Step 3 is the portal.

## F1. A batch on today's release

1. **Control Center → Reset → Reset demo.** (This makes **v1** the current release and stops the v2 and v3
   workers.)
2. **Window 1 (Jordan):** upload `summit-payroll-5000.csv` → **Save as draft** → **Awaiting approval**. Call this
   **batch A**. Don't approve it yet.
3. **Control Center → Versioning.** The card **Deployment bulkwire · current v1** shows:

   | Build | Container | Pollers | Running batches pinned |
   |---|---|---|---|
   | v1 ★ | running/running | 2 | 1 |
   | v2 | created | 0 | 0 |
   | v3 | created | 0 | 0 |

   The ★ marks the current release. Batch A is the **1** pinned to v1.

**Say:** "Batch A started on v1, today's release. It's waiting for approval, and it will finish on v1 whatever we
deploy next."

## F2. Ship a release with a bug

1. Click **1. Start worker-v2 (buggy build).** A few seconds later the v2 row says **running**, **1** poller.
   worker-v2 is a real build of the same code with one deliberate bug: it crashes at the start of the
   authorization step.
2. Click **2. Make v2 current.** Within a couple of seconds the card title says **current v2**, and the ★ moves to
   v2.

**Say:** "We've just deployed v2 and made it the release for all *new* batches. Batch A is still pinned to v1."

## F3. A new batch runs into the bug

1. **Window 1 (Jordan):** upload `summit-payroll-5000.csv` again → **Save as draft** → **Awaiting approval**. This
   is **batch B**. The Versioning table now shows **1** batch pinned to v2. B's draft ran on v2 without trouble:
   the bug is only in the authorization step.
2. **Window 2 (Alex)** and **Window 3 (Sam):** approve **batch B** as in part 1 (**Approvals** → the *top* row, the newest
   batch → **Review and approve** → `246810` → **Approve**). Sam's dialog says **"Approved (APR-…). Release started."**
3. **Window 1 (Jordan):** open batch B. Within a few seconds the page shows a banner: **"Live progress is
   temporarily unavailable. Your batch is safe and nothing has been lost. Current status: DRAFTED."** (If the page
   was already open, the last known view stays underneath, dimmed.)
4. **Window 4 (Temporal UI):** open batch B (**Open in Temporal UI**, or the workflow list → the newest
   **Running** row). Point at:
   - Header: **Build ID v2**, **Versioning Behavior Pinned**.
   - **Workflow Task Failed** · Failure: **"demo bug: authorize step crashed (worker-v2)"**, with a **Pending
     Workflow Task** below it. The failure carries the Go stack trace, pointing at the line that crashed.

**Say:** "The bug crashed the workflow *code*, not the batch. Temporal treats a crash in workflow code as a failed
step, not a failed business process: the approval is recorded, all 4,997 drafted wires are in the bank's system,
and the batch is parked right before the authorization step. Temporal retries that step with growing pauses, so a
bad release doesn't hammer anything. Even the live view is honest: it says progress is unavailable rather than
showing a stale picture, because the code that answers that question is the code that's broken."

## F4. The old batch is fine

1. **Window 2 (Alex)** and **Window 3 (Sam):** approve **batch A**: in **Approvals** it's the
   *second* row. Batch B is still listed above it, because B hasn't been released yet.
2. **Window 1 (Jordan):** batch A goes **Releasing**, then **Authorized** about 10 seconds later. In the Temporal
   UI, its header says **Build ID v1**.

**Say:** "Same code path, same moment, but batch A is pinned to v1, so it never touched the broken release. A bad
deploy can't corrupt work that was already in flight."

## F5. Ship the fix, and move only the broken batch

1. **Control Center → Versioning:** click **4. Start worker-v3 (the fix).** v3 shows **running**, **1** poller.
2. Click **5. Make v3 current.** The card title changes to **current v3** within a couple of seconds.
3. Click **6. Move pinned v2 batches to v3.** This sets a *versioning override* on every batch pinned to v2: in
   this demo, just batch B.
4. **Window 1 (Jordan):** within about **a minute** (in a test run, 40 seconds), batch B comes back: **Releasing**,
   then **Authorized**, 4,997 / 5,000. The wait is Temporal's next scheduled retry of the failed step; the longer a
   batch has been stuck, the longer the pause between retries.
5. **Window 4 (Temporal UI):** reload batch B. The header says **Build ID v3**. **Event History** shows the
   options update (the override) followed by the authorization chunks.
6. Click **7. Stop worker-v2.** The broken release is gone.

**Say (close):** "A broken release stopped one new batch, safely, and the fix moved only that batch forward. Pinned
workflows never run code they didn't start with, and a targeted override moves just the affected ones to the fix.
What you don't need: patching workflows in flight, or restarting batches by hand and hoping nothing gets paid
twice."

---

## Afterwards

- **Control Center → Reset → Reset demo.** It stops worker-v2 and worker-v3 and makes v1 current again.
- If you ran Scenario E and haven't restored the keyring yet: do [E5](#e5-put-the-committed-key-back) now.

---

## If something goes wrong

| What you see | What to do |
|---|---|
| E: the inspector's list is empty | It lists batches that exist in Temporal. Draft a batch first (E1). |
| E: after rotating, a new batch still shows `k2026a` | Wait 2 seconds after **Rotate key** before drafting; the services reload the keyring on a short timer. |
| E: `git checkout config/keyring.json` says nothing changed | You never rotated, or you've already restored it. Nothing to do. |
| E: the Temporal UI shows "Failed to decode" for some batches | They were encrypted with a temporary key you've since removed (E5). Expected; reset and draft new ones. |
| F: step 2 seems to do nothing | The table refreshes every 2 seconds. Wait a moment for the ★ to move. |
| F: batch B authorized straight away | v2 wasn't current when B started. Check that the Versioning card said **current v2** before you drafted B. Reset and start again at F1. |
| F: batch B is still stuck a few minutes after step 6 | Check v3 has **1** poller, then click **6. Move pinned v2 batches to v3** again (it's safe to repeat). |
| F: you ran `make up` or `docker compose up` in the middle | That re-runs the job that makes v1 current. Reset and start again at F1. |
| Anything else | See part 1, [If something goes wrong](DEMO-GUIDE-HAPPY-PATH.md#if-something-goes-wrong). |
