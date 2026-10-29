#!/bin/sh
# worker-activate: one-shot job that makes build v1 the Current Version of worker deployment "bulkwire".
#
# PATTERN: Worker Versioning (Pinned) —
#   https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning
# New workflow executions are routed to the Current Version and stay pinned to it for their whole life.
# The command fails until v1 workers are polling, so retry every 2 s for up to 2 minutes.
# In production this step belongs to CI/CD or the Temporal Worker Controller; a gradual rollout uses
# `temporal worker deployment set-ramping-version`.
set -u

DEPLOYMENT="${WORKER_DEPLOYMENT_NAME:-bulkwire}"
BUILD_ID="${WORKER_BUILD_ID:-v1}"

for i in $(seq 1 60); do
  if temporal worker deployment set-current-version \
      --deployment-name "$DEPLOYMENT" --build-id "$BUILD_ID" --yes; then
    echo "Current Version of deployment $DEPLOYMENT is now $BUILD_ID"
    exit 0
  fi
  echo "waiting for $DEPLOYMENT/$BUILD_ID pollers (attempt $i)"
  sleep 2
done
echo "gave up: $DEPLOYMENT/$BUILD_ID never became current" >&2
exit 1
