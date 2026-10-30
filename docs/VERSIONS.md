# Pinned versions

Every version below is pinned exactly in `go/go.mod`, `docker-compose.yml`, the Dockerfiles, or the Makefile.
No `latest` tags are used anywhere. All lookups were made on **2026-10-28** against the sources listed.

## Toolchains and runtimes

| Component | Pinned | Where | Source checked |
|---|---|---|---|
| Go toolchain | `go1.27.3` (`go 1.27` in go.mod, `toolchain go1.27.3`) | `go/go.mod` | go.dev/dl JSON feed |
| Go build image | `golang:1.27.3-alpine3.24` | `docker/go.Dockerfile` | Docker Hub |
| Go runtime base | `alpine:3.24` | `docker/go.Dockerfile` | Docker Hub |
| .NET (LTS) SDK | `mcr.microsoft.com/dotnet/sdk:10.0.403` | `core/Dockerfile`, `Makefile` (`test-dotnet`) | dotnet release-metadata `releases-index.json` (10.0 = LTS, active) + MCR tag list |
| ASP.NET Core runtime | `mcr.microsoft.com/dotnet/aspnet:10.0.14` | `core/Dockerfile` | same |
| Node.js (Active LTS "Lithium") | `node:26.13.0-alpine3.24` | `gateway/Dockerfile` | nodejs.org/dist/index.json |
| nginx (stable line) | `nginx:1.30.6-alpine` | `gateway/Dockerfile` | Docker Hub + nginx GitHub releases (1.31.x is mainline) |

## Temporal

| Component | Pinned | Notes |
|---|---|---|
| Temporal CLI / dev server image | `temporalio/temporal:1.9.1` | Official CLI image (Docker Hub). Bundles Temporal Server 1.32.0 and UI 2.54.1 (`temporal --version`). Used by `temporal`, `temporal-init`, `worker-activate`. |
| `go.temporal.io/sdk` | `v1.50.0` | pkg.go.dev / `go list -m -versions` |
| `go.temporal.io/api` | `v1.64.0` | same |
| `go.temporal.io/sdk/contrib/envconfig` | `v1.0.2` | same |
| `go.temporal.io/sdk/contrib/tally` | `v0.2.0` | same |
| `github.com/uber-go/tally/v4` (Prometheus reporter) | `v4.1.17` | same |
| Temporal Server source consulted for dynamic-config keys | `go.temporal.io/server v1.32.0` | read only (not a dependency) |

Checked again on **2026-10-30** for the Core task queue per FI: Server 1.32.0 supports Fairness
(`matching.enableFairness=true`, already set on the dev server) and task-queue rate limits; CLI 1.9.1 has
`temporal task-queue config set --queue-rps-limit` / `config get` (`--help` in the pinned image), and
`DescribeTaskQueue` reports the effective rate limit and its source. Go SDK v1.50.0 has `worker.Options`
`TaskQueueActivitiesPerSecond`, `DisableWorkflowWorker`, and `ActivityOptions.Priority`. No upgrade was needed.

## Data stores and emulators

| Component | Pinned | Notes |
|---|---|---|
| SQL Server 2022 | `mcr.microsoft.com/mssql/server:2022-CU27-GDR1-ubuntu-22.04` | Same digest as `2022-latest` on 2026-10-28 (`sha256:cf8f0ab2…`). amd64 only. |
| Redis | `redis:8.10.3-alpine` | Latest stable release (GitHub `redis/redis` 8.10.3, 2026-10-26). |
| S3-compatible server | `rustfs/rustfs:1.0.4` | Replaces MinIO, which no longer publishes container images. GitHub release 1.0.4 (2026-10-25), not a prerelease. |
| S3 init client | `amazon/aws-cli:2.39.1` | Replaces MinIO's `mc` client for bucket setup. |
| fake-gcs-server | `fsouza/fake-gcs-server:1.56.1` | GitHub release v1.56.1 (2026-08-24). |

## Go libraries

| Module | Pinned | Notes |
|---|---|---|
| `github.com/redis/go-redis/v9` | `v9.23.0` |
| `github.com/aws/aws-sdk-go-v2` | `v1.47.5` |
| `github.com/aws/aws-sdk-go-v2/service/s3` | `v1.114.6` |
| `github.com/aws/aws-sdk-go-v2/credentials` | `v1.20.12` |
| `cloud.google.com/go/storage` | `v1.70.0` |
| `google.golang.org/api` | `v0.310.0` |
| `github.com/oklog/ulid/v2` | `v2.1.2` |
| `github.com/golang-jwt/jwt/v5` | `v5.3.1` |
| `github.com/stretchr/testify` | `v1.12.1` |
| `github.com/prometheus/client_golang` | `v1.25.0` | (explicit pin; tally's Prometheus reporter only requires ≥ v1.11) |
| `github.com/prometheus/common` | `v0.72.0` | |
| `go.yaml.in/yaml/v3` | `v3.0.5` | `config/routing.yaml` parser. Already in the module graph (indirect); made direct on 2026-10-30, same version. |
| `google.golang.org/grpc` | `v1.84.0` | Already in the module graph (indirect); direct since 2026-10-30 for the control-service's `TaskQueueConfigAPI` interface. Same version. |

## Docker

| Component | Version | Notes |
|---|---|---|
| Docker Engine (development machine) | 29.9.2, API 1.56 (minimum 1.40) | Docker Desktop 4.96.3 |
| Docker Compose | v5.6.1 | `required: false` in `depends_on` and `docker compose wait` are used |
| Docker Engine API used by control-service | `v1.47` | Pinned in the request path; supported by Engine 27.0 and later |

## .NET packages (`core/CoreApi/CoreApi.csproj`, `core/CoreApi.Tests/CoreApi.Tests.csproj`)

| Package | Pinned | Notes |
|---|---|---|
| `Microsoft.Data.SqlClient` | `7.1.1` | No ORM. `SqlDataRecord`/`SqlMetaData` live in `Microsoft.Data.SqlClient.Server` in 7.x |
| `Microsoft.Extensions.Http.Resilience` | `10.11.0` | Standard resilience handler on the approval callback client |
| `Microsoft.IdentityModel.JsonWebTokens` | `8.23.0` | HS256 portal-token validation |
| `System.Security.Cryptography.Pkcs` | `10.0.14` | Direct pin over SqlClient's transitive 9.0.18, which has published advisories (GHSA-2fmv-rv2c-59x7, GHSA-846f-f764-76mx) |
| `xunit.v3` | `4.0.2` | Runs on Microsoft.Testing.Platform (`core/global.json`) |
| .NET SDK selection | `core/global.json` → `10.0.403`, `rollForward: disable` | |

Looked up 2026-10-28 on api.nuget.org.

## UI packages (`ui/package.json`, `control-ui/package.json`; exact versions, lockfiles committed)

| Package | Pinned |
|---|---|
| `react`, `react-dom` | `19.3.1` |
| `react-router` (portal only) | `8.5.1` |
| `@tanstack/react-query` | `5.105.0` |
| `lucide-react` | `1.63.0` |
| `@fontsource/source-sans-3` (portal) | `5.3.1` |
| `@fontsource/ibm-plex-sans` (Control Center) | `5.3.0` |
| `vite` | `8.3.5` |
| `@vitejs/plugin-react` | `6.1.2` |
| `typescript` | `7.0.2` |
| `vitest` | `5.0.5` |
| `@types/react`, `@types/react-dom` | `19.3.0` |

Looked up 2026-10-28 with `npm view`. Builds and tests run on `node:26.13.0-alpine3.24`.
