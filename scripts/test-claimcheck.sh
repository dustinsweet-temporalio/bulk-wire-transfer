#!/usr/bin/env bash
# test-claimcheck.sh — runs the claim-check contract test (internal/claimcheck/claimchecktest) against every
# real backend: Redis, S3 (RustFS), and GCS (fake-gcs-server). Each backend is started with its compose
# profile, then the Go test runs in a container on the compose network so it reaches the backend by name.
set -euo pipefail
cd "$(dirname "$0")/.."

GO_IMAGE=golang:1.27.3-alpine3.24
NETWORK="${COMPOSE_PROJECT_NAME:-bulkwire}_default"
set -a; source .env; set +a

run_contract() {
  local backend="$1"
  echo "=== claim-check contract: $backend"
  docker run --rm --network "$NETWORK" \
    -v "$PWD/go:/src" -w /src \
    -v bulkwire-gomod:/go/pkg/mod -v bulkwire-gobuild:/root/.cache/go-build \
    -e CGO_ENABLED=0 -e CLAIMCHECK_INTEGRATION=1 -e CLAIMCHECK_BACKEND="$backend" \
    -e REDIS_ADDR -e CLAIMCHECK_TTL \
    -e S3_ENDPOINT -e S3_BUCKET -e S3_ACCESS_KEY -e S3_SECRET_KEY \
    -e GCS_ENDPOINT -e GCS_BUCKET \
    "$GO_IMAGE" go test ./internal/claimcheck/ -run TestBackendContract -count=1 -v
}

docker compose --profile claimcheck-redis up -d --wait redis
run_contract redis

docker compose --profile claimcheck-s3 up -d rustfs
docker compose --profile claimcheck-s3 run --rm rustfs-init >/dev/null
run_contract s3

docker compose --profile claimcheck-gcs up -d fake-gcs
sleep 2
run_contract gcs

echo "=== all claim-check backends pass the contract"
