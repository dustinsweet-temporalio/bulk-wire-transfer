#!/usr/bin/env bash
# ratelimit.sh <fiId> <rps|default> — overrides the rate limit of one FI's Core Activity task queue, server-side,
# without a deployment (`make ratelimit FI=lakeshore RPS=2`). RPS=default clears the override, so the queue returns
# to the TaskQueueActivitiesPerSecond its workers set from config/routing.yaml.
#
# The queue is looked up in config/routing.yaml (never derived from the FI ID). A rate limit caps how fast Core
# Activities START on that queue; the number in flight is bounded by each batch's semaphore and each worker's cap.
# PRODUCTION NOTE: against Temporal Cloud, run the same `temporal task-queue config set` with your own CLI profile.
set -euo pipefail
cd "$(dirname "$0")/.."
FI="${1:?usage: make ratelimit FI=<fiId> RPS=<requests per second|default>}"
RPS="${2:?usage: make ratelimit FI=<fiId> RPS=<requests per second|default>}"

# The routing file's queues are two-space-indented keys under coreQueues; each has a `fis: [a, b]` line.
QUEUE="$(awk -v fi="$FI" '
  /^coreQueues:/ { in_q = 1; next }
  /^[^ #]/       { in_q = 0 }
  in_q && /^  [A-Za-z0-9._-]+:[ \t]*$/ { q = $1; sub(":", "", q) }
  in_q && /^    fis:/ {
    line = $0; sub(/^[^[]*\[/, "", line); sub(/\].*$/, "", line)
    n = split(line, fis, /[ ,]+/)
    for (i = 1; i <= n; i++) if (fis[i] == fi) { print q; exit }
  }' config/routing.yaml)"
if [ -z "$QUEUE" ]; then
  echo "no Core queue for FI '$FI' in config/routing.yaml" >&2
  exit 1
fi

docker compose exec -T temporal temporal task-queue config set --address 127.0.0.1:7233 --namespace bulkwire \
  --task-queue "$QUEUE" --task-queue-type activity \
  --queue-rps-limit "$RPS" --queue-rps-limit-reason "make ratelimit (FI=$FI)"
echo "$QUEUE: queue rate limit override = $RPS"
docker compose exec -T temporal temporal task-queue config get --address 127.0.0.1:7233 --namespace bulkwire \
  --task-queue "$QUEUE" --task-queue-type activity
