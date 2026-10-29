#!/usr/bin/env bash
# wait-for.sh URL [SECONDS]: poll an HTTP URL until it answers 2xx/3xx, or fail after SECONDS (default 120).
# Used by `make up` to wait for the gateway before printing URLs.
set -euo pipefail
url="$1"; limit="${2:-120}"
for ((i = 0; i < limit; i++)); do
  if curl -fsS -o /dev/null "$url"; then exit 0; fi
  sleep 1
done
echo "timed out after ${limit}s waiting for $url" >&2
exit 1
