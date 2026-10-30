# Bulk Wire Transfer — Temporal reference implementation

A commercial banking customer uploads a CSV of up to 10,000 outgoing wires. One Temporal workflow per batch
(`BulkWireBatch`) drafts every valid wire into the financial institution's core system ("Core") through stored
procedures with Table-Valued Parameters, waits durably for Core's approval decision (determination, MFA/TAC, dual
control), then authorizes every wire. It survives infrastructure failures without losing or duplicating a wire,
parks anything that needs a human inside the workflow, and shows live progress throughout. It is both a teaching
codebase and a live demo of the recommended Temporal design; all institutions, people, and companies are fictional.

## Prerequisites

- Docker Desktop with **12 GB RAM** allocated (two SQL Servers plus the stack). On Apple Silicon, enable
  **Rosetta** (Settings → General → "Use Rosetta for x86_64/amd64 emulation"): SQL Server ships amd64 only.
- `make`, `curl`, `jq`. No local Go, .NET, or Node is required: everything builds in pinned containers.

## Quick start

```bash
make up            # BACKEND=redis (default) | s3 | gcs
make urls
```

| What | URL |
|---|---|
| Lakeshore Community Bank portal | http://localhost:3001 |
| Prairie Valley Credit Union portal | http://localhost:3002 |
| Demo Control Center (presenter) | http://localhost:3100 |
| Temporal Web UI (namespace `bulkwire`) | http://localhost:8233 |
| Codec Server | http://localhost:8089 |
| Worker metrics | http://localhost:9464/metrics, http://localhost:9465/metrics |

## Demo secrets: replace before any real use

Everything secret in this repository is a **public, throwaway demo value**, committed on purpose so the demo runs
with no setup. Anyone can read it, so treat it as already compromised:

- `config/keyring.json`: the AES-256 key that encrypts every Temporal payload. Anyone with this file can decrypt
  any history produced with it. In production, keys come from a KMS or HSM (envelope encryption), are never
  committed, and are rotated (the Codec Server and workers read the keyring; see `go/internal/codec`). The
  AES-256-GCM DataConverter and Codec Server here are an example: an organization that already runs its own
  Payload Codec and Codec Server configures that on the workers and clients instead.
- `.env`: the portal token signing key, the per-FI service-to-service tokens, the SQL Server `sa` password, and the
  S3 credentials. In production these come from a secret manager, and service-to-service calls use mTLS or
  workload identity instead of shared tokens.
- The demo sign-in (password `demo`, security code `246810`) stands in for the bank's identity provider and MFA.

This is a reference implementation for learning and demos, not a production system. Each place a real
deployment would differ is marked `PRODUCTION NOTE:` in the code.

## Users

Password for everyone: **`demo`**. Security code (TAC) for approvals: **`246810`**.

| Portal | Company ID | Users |
|---|---|---|
| Lakeshore :3001 | `SUMMIT` | `jordan` (initiator), `alex` (approver), `sam` (approver) |
| Lakeshore :3001 | `NORTHLINE` | `taylor` (initiator), `quinn` (approver), `avery` (approver) |
| Lakeshore :3001 | `BANK` | `morgan` (bank operations) |
| Prairie :3002 | `HARVEST` | `riley` (initiator), `casey` (approver), `jamie` (approver) |
| Prairie :3002 | `BANK` | `devon` (member operations) |

## Demo scenarios

Reset first (Control Center → Reset, or `make demo-reset`). Step-by-step in [docs/RUNBOOK-DEMO.md](docs/RUNBOOK-DEMO.md);
every failure condition F01–F49 in [docs/FAILURE-CATALOG.md](docs/FAILURE-CATALOG.md).

| | Scenario | Shows |
|---|---|---|
| A | Core goes down mid-draft | Retries with no attempt cap; nothing turns red; exact totals |
| B | A human has to fix it, then dual approval | Park-and-resolve (Resumable Activity); Core-owned approvals; durable wait |
| C | Payroll day: two people, one Core | A Core task queue per FI with its own rate limit (Prairie unaffected when Lakeshore is throttled); Fairness by batch ID within one FI's queue |
| D | Chaos tour | Worker crash, no workers, Temporal restart, frozen database, store outage |
| E | Encryption and history tour | Ciphertext at rest, key rotation, readable history in the Web UI |
| F | We shipped a bug | Worker Versioning (Pinned) and a targeted versioning override |

## Task queues

The workflow and the claim-check-only Activity run on the shared task queue `bulkwire`. Every Activity that calls
Core runs on its FI's own Core task queue (`bulkwire-core-lakeshore`, `bulkwire-core-prairie`), mapped in
[config/routing.yaml](config/routing.yaml) together with each queue's rate limit and per-worker cap.
bulkwire-service resolves a batch's queue when its workflow starts and passes it in the workflow input, so a batch
keeps its queue even if the file changes. The rate limit caps how fast Core Activities start on that queue; how
many run at once is bounded by each batch's chunk concurrency (default 4) and each worker's cap. Within a queue,
Fairness is keyed by batch ID, so when the queue backs up the Temporal Service takes turns across that FI's
batches. Several FIs whose Cores share a database cluster can share one queue; its fairness key is then the FI.
Override a queue's rate limit without a deployment with `make ratelimit FI=lakeshore RPS=2` (`RPS=default`
clears it) or from the Control Center. Details in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

Chunk size (default 250 wires) can be anything from 1 to the maximum batch size. What is limited is the chunk
count: a batch that would need more than `MAX_CHUNKS_PER_BATCH` (1,300) chunks is rejected at staging, because one
workflow history should not grow past about 25,000 events.

## Claim-check backend

`make up BACKEND=redis|s3|gcs` selects Redis (AOF, noeviction), S3 (RustFS, an S3-compatible server; MinIO no
longer publishes images), or GCS (fake-gcs-server). The backend is used only as an immutable store for staged rows.

## Tests

```bash
make test          # Go (unit + workflow W1–W20), .NET, and UI tests, each in its pinned container
make test-integration  # Core queue isolation against a real Temporal dev server (downloads the pinned CLI)
make e2e           # end-to-end checks against a running stack (~15 min)
make replay        # replay captured histories against the current workflow code
make replay-nde    # shows a non-determinism error from a reordered workflow (fails on purpose)
make test-claimcheck   # claim-check contract against Redis, S3, and GCS
```

## Temporal Cloud mode

All Go binaries load their connection with `go.temporal.io/sdk/contrib/envconfig`, so Cloud differs only in
configuration (see `.env.cloud.example`):

1. Set `TEMPORAL_ADDRESS=<namespace>.<account>.tmprl.cloud:7233`, `TEMPORAL_NAMESPACE=<namespace>.<account>`, and
   `TEMPORAL_API_KEY`, and drop `local-temporal` from `COMPOSE_PROFILES` (the dev server, `temporal-init`, and
   `worker-activate` then do not start).
2. Create the five Search Attributes on the Cloud namespace, e.g.
   `temporal operator search-attribute create --namespace <ns> --name BulkWireStatus --type Keyword` (repeat for
   `FiId`, `CustomerId`, `InitiatedBy` as Keyword and `NeedsAttention` as Bool), with `--address`, `--namespace`,
   and `--api-key` for your namespace. `scripts/temporal-init.sh` contains the full list and its verification.
3. Optional: enable Fairness on the namespace (a paid feature: +0.1 Action per Action). Without it, each Core
   queue's rate limit still applies, and the batches on one queue are dispatched first come, first served. An
   exact queue rate limit needs a single-partition task queue, which on Cloud requires a support request.
4. In the Cloud UI, set the Codec Server endpoint for the namespace to a URL your browser can reach (this
   repository's `codec-server`); the Temporal Service never sees the keys.
5. After the v1 workers are polling, activate the version:
   `temporal worker deployment set-current-version --deployment-name bulkwire --build-id v1 --yes` with the same
   connection flags (`--address`, `--namespace`, `--api-key`).

Check the current Temporal Cloud documentation for each step before relying on it.

## Repository layout

`go/` (one module: worker, bulkwire-service, codec-server, control-service, samplegen), `core/` (the .NET core-banking API
and its tests), `sql/` (schema, TVP types, procedures, seeds), `ui/` (FI portal), `control-ui/` (Control Center),
`gateway/` (nginx), `scripts/` (init, e2e, history capture), `docs/` (below).

## Documentation

- [docs/DEMO-GUIDE-HAPPY-PATH.md](docs/DEMO-GUIDE-HAPPY-PATH.md) — step-by-step presenter guide, starting from a fresh laptop (happy path)
- [docs/DEMO-GUIDE-FAILURES.md](docs/DEMO-GUIDE-FAILURES.md) — part 2: Core outage mid-draft, then an account hold fixed by bank operations
- [docs/DEMO-GUIDE-PAYROLL-DAY.md](docs/DEMO-GUIDE-PAYROLL-DAY.md) — part 3: three big batches at once; a Core task queue per bank with its own rate limit; Fairness across one bank's batches
- [docs/DEMO-GUIDE-CHAOS.md](docs/DEMO-GUIDE-CHAOS.md) — part 4: crash, stop, restart, and freeze every moving part during a 10,000-wire draft
- [docs/DEMO-GUIDE-PRODUCTION.md](docs/DEMO-GUIDE-PRODUCTION.md) — part 5: payload encryption and key rotation; shipping (and fixing) a workflow bug
- [docs/DEMO-GUIDE-CATALOG.md](docs/DEMO-GUIDE-CATALOG.md) — part 6: one-minute recipes for the rest of the failure catalog, and an F01–F49 index
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — topology, services, the life of a batch, what is deliberately absent
- [docs/TRACEABILITY.md](docs/TRACEABILITY.md) — every pattern and design rule mapped to code
- [docs/RUNBOOK-DEMO.md](docs/RUNBOOK-DEMO.md) — cheat sheet and scenarios A–F
- [docs/FAILURE-CATALOG.md](docs/FAILURE-CATALOG.md) — F01–F49 with click paths
- [docs/VERSIONS.md](docs/VERSIONS.md) — every pinned version

## License

[MIT](LICENSE). All institutions, people, companies, and data in this repository are fictional.
