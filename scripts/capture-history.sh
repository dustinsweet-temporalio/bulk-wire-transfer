#!/usr/bin/env bash
# capture-history.sh <workflowId> <name> — exports a BulkWireBatch history as JSON into go/testdata/histories/
# for replay tests (`make replay`). The history is exported exactly as stored, i.e. ENCRYPTED: the replay test
# decodes it with config/keyring.json, so capture only batches encrypted with a key in the committed keyring.
set -euo pipefail
cd "$(dirname "$0")/.."
WF="${1:?usage: capture-history.sh <workflowId> <name>}"
NAME="${2:?usage: capture-history.sh <workflowId> <name>}"
OUT="go/testdata/histories/${NAME}.json"
docker compose exec -T temporal temporal workflow show --namespace bulkwire --address 127.0.0.1:7233 \
  --workflow-id "$WF" --output json > "$OUT"
echo "wrote $OUT ($(jq '.events | length' "$OUT") events)"
