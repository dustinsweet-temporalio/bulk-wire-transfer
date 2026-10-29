#!/usr/bin/env bash
# e2e.sh — end-to-end checks against a running stack (`make up`).
#
# Signs in through each portal's gateway (http://localhost:3001 / :3002) and uses the Control Center API
# (http://localhost:3100/api/control) for faults, chaos, load, fairness, and encryption. Exits non-zero on any
# failed check. Takes ~15 minutes (it waits on real Core calls and real outages).
#
# Note: step 13 rotates the encryption key, which rewrites config/keyring.json. The script restores the committed
# keyring at the end (batches encrypted with the temporary key can then no longer be decoded; `make demo-reset`
# terminates them anyway).
set -uo pipefail
cd "$(dirname "$0")/.."
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

C=http://localhost:3100/api/control
PASS=0; FAIL=0
step() { echo; echo "== $*"; }
check() { # name got want (an empty "got" always fails, so a dead stack cannot pass)
  if [ -n "$2" ] && [ "$2" = "$3" ]; then echo "  PASS $1 ($2)"; PASS=$((PASS + 1))
  else echo "  FAIL $1: got '$2', want '$3'"; FAIL=$((FAIL + 1)); fi
}
cpost() { curl -s -X POST -H 'Content-Type: application/json' -d "${2:-{\}}" "$C$1"; }
cput() { curl -s -X PUT -H 'Content-Type: application/json' -d "$2" "$C$1"; }
port() { [ "$1" = prairie ] && echo 3002 || echo 3001; }
login() { # fi company user -> token
  curl -s -X POST "http://localhost:$(port "$1")/api/bulkwire/auth/login" -H 'Content-Type: application/json' \
    -d "{\"fiId\":\"$1\",\"companyId\":\"$2\",\"userId\":\"$3\",\"password\":\"demo\"}" | jq -r '.token // empty'
}
bw() { local p=$1 tok=$2 path=$3; shift 3; curl -s -H "Authorization: Bearer $tok" "$@" "http://localhost:$p/api/bulkwire$path"; }
core() { local p=$1 tok=$2 path=$3; shift 3; curl -s -H "Authorization: Bearer $tok" "$@" "http://localhost:$p/api/core$path"; }
stage() { # port token sample -> sets $BATCH and $SUMMARY (no subshell, so both globals survive)
  bw "$1" "$2" "/samples/$3.csv" -o /tmp/e2e-sample.csv
  SUMMARY=$(bw "$1" "$2" /batches -F file=@/tmp/e2e-sample.csv)
  BATCH=$(echo "$SUMMARY" | jq -r ".batchId // empty")
}
draft() { bw "$1" "$2" "/batches/$3/draft" -X POST -H "Idempotency-Key: $4" -o /dev/null -w '%{http_code}'; }
view() { bw "$1" "$2" "/batches/$3"; }
phase() { view "$@" | jq -r '.view.phase // .degraded.phase // .source'; }
wait_phase() { # port token batch phase seconds
  local p=""
  for _ in $(seq 1 "$5"); do p=$(phase "$1" "$2" "$3"); [ "$p" = "$4" ] && break; sleep 1; done
  echo "$p"
}
approve() { core "$1" "$2" /approvals -X POST -H 'Content-Type: application/json' -d "{\"batchId\":\"$3\",\"tac\":\"246810\"}"; }
resolve() { bw "$1" "$2" "/ops/batches/$3/resolve" -X POST -H "Idempotency-Key: e2e-$3-$4-$5" -H 'Content-Type: application/json' \
  -d "{\"chunkId\":\"$4\",\"decision\":\"$5\",\"note\":\"e2e\"}" -o /dev/null -w '%{http_code}'; }
genTx() { local p=$1 tok=$2 b=$3; shift 3; core "$p" "$tok" "/gentx-batches/by-external/$b" "$@"; }
parked() { view "$@" | jq -r '[(.view.chunks // [])[] | select(.attention) | .chunkId] | join(" ")'; }
sql_users() { # fi -> sorted user ids in that FI's Core
  docker compose exec -T -e SQLCMDPASSWORD="$(grep ^MSSQL_SA_PASSWORD .env | cut -d= -f2-)" "sql-$1" \
    /opt/mssql-tools18/bin/sqlcmd -C -S localhost -U sa -d "Core_$1" -h -1 -W -Q "SET NOCOUNT ON; SELECT UserId FROM dbo.Users ORDER BY UserId" | tr -d '\r' | sed '/^$/d' | sort | tr '\n' ' '
}

cp config/keyring.json /tmp/e2e-keyring.json

step "1. Reset; namespace and Search Attributes"
check "reset" "$(cpost /reset | jq -r .ok)" true
SA=$(docker compose exec -T temporal temporal operator search-attribute list --namespace bulkwire --address 127.0.0.1:7233 -o json)
for a in BulkWireStatus:KEYWORD FiId:KEYWORD CustomerId:KEYWORD InitiatedBy:KEYWORD NeedsAttention:BOOL; do
  check "Search Attribute ${a%%:*}" "$(echo "$SA" | jq -r ".customAttributes.${a%%:*}")" "INDEXED_VALUE_TYPE_${a##*:}"
done
for fi in lakeshore prairie; do
  users=""
  for row in $(curl -s "http://localhost:$(port $fi)/api/bulkwire/fi/$fi/branding" | jq -r '.demoUsers[] | "\(.companyId):\(.userId)"'); do
    [ -n "$(login $fi "${row%%:*}" "${row##*:}")" ] && users="$users${row##*:} "
  done
  check "$fi: every configured user signs in and Core knows them" "$(echo $users | tr ' ' '\n' | sort | tr '\n' ' ')" "$(sql_users $fi)"
done

J=$(login lakeshore SUMMIT jordan); A=$(login lakeshore SUMMIT alex); S=$(login lakeshore SUMMIT sam)
M=$(login lakeshore BANK morgan); R=$(login prairie HARVEST riley)

step "2. Upload summit-payroll-5000; Draft is idempotent and starts one execution"
stage 3001 "$J" summit-payroll-5000; B=$BATCH
check "staged VALID, 5,000 rows, 6 groups" "$(echo "$SUMMARY" | jq -r '"\(.status) \(.validRowCount) \(.groups|length)"')" "VALID 5000 6"
cpost /core/lakeshore/latency '{"chunkLatencyMs":1500}' >/dev/null
check "draft K1" "$(draft 3001 "$J" "$B" K1)" 202
check "draft K1 again" "$(draft 3001 "$J" "$B" K1)" 202
check "draft K2" "$(draft 3001 "$J" "$B" K2)" 202
sleep 2
check "one execution for the Workflow ID" "$(docker compose exec -T temporal temporal workflow count --namespace bulkwire --address 127.0.0.1:7233 -q "WorkflowId='bulkwire/lakeshore/$B'" -o json | jq -r .count)" 1

step "3. Core outage 20 s during draft"
sleep 4
cpost /core/lakeshore/faults '{"mode":"outage","durationSeconds":20}' >/dev/null
ATTN=false
for _ in $(seq 1 180); do
  V=$(view 3001 "$J" "$B")
  [ "$(echo "$V" | jq -r .view.needsAttention)" = true ] && ATTN=true
  [ "$(echo "$V" | jq -r .view.phase)" = DRAFTED ] && break
  sleep 1
done
check "DRAFTED within 3 minutes" "$(echo "$V" | jq -r .view.phase)" DRAFTED
check "never needsAttention" "$ATTN" false
check "Core: 5,000 rows, 4,997 Drafted, 3 Rejected" "$(genTx 3001 "$J" "$B" | jq -r '"\(.counts.total) \(.counts.drafted) \(.counts.rejected)"')" "5000 4997 3"
cpost /core/lakeshore/latency '{"chunkLatencyMs":600}' >/dev/null

step "4. Dual approval"
# Variables first: bash 3.2 (macOS) mis-parses escaped quotes inside a nested "$( ... "$( ... )" ... )".
QN=$(login lakeshore NORTHLINE quinn); DENY_BODY="{\"batchId\":\"$B\",\"reason\":\"x\"}"
check "an approver at another company cannot deny it" "$(core 3001 "$QN" /approvals/deny -X POST -H 'Content-Type: application/json' -d "$DENY_BODY" | jq -r .code)" APPROVAL_NOT_ALLOWED
check "Alex -> PENDING_SECOND_APPROVAL" "$(approve 3001 "$A" "$B" | jq -r .status)" PENDING_SECOND_APPROVAL
check "Sam -> APPROVED" "$(approve 3001 "$S" "$B" | jq -r .status)" APPROVED
check "AUTHORIZED" "$(wait_phase 3001 "$J" "$B" AUTHORIZED 120)" AUTHORIZED
check "4,997 Authorized in Core" "$(genTx 3001 "$J" "$B" | jq -r .counts.authorized)" 4997

step "4b. Approve before draft: Core uses the staged amount, never the request's"
stage 3001 "$J" summit-payroll-5000; B4=$BATCH
FORGED=$(core 3001 "$A" /approvals -X POST -H 'Content-Type: application/json' \
  -d "{\"batchId\":\"$B4\",\"tac\":\"246810\",\"amountCents\":100,\"rowCount\":1,\"initiatedBy\":\"sam\"}")
check "a forged \$1 amount still needs two approvers" "$(echo "$FORGED" | jq -r '"\(.status) \(.requiredApprovers)"')" "PENDING_SECOND_APPROVAL 2"
check "another company cannot read the approval" "$(core 3001 "$QN" "/approvals/$B4" -o /dev/null -w '%{http_code}')" 404
cpost /core/lakeshore/latency '{"chunkLatencyMs":1500}' >/dev/null
check "Sam -> APPROVED (starts the workflow)" "$(approve 3001 "$S" "$B4" | jq -r .status)" APPROVED
sleep 3
check "while it drafts, Jordan can no longer cancel it" "$(bw 3001 "$J" "/batches/$B4/cancel" -X POST -H 'Idempotency-Key: e2e-late-cancel' | jq -r .code)" REJECTED_BY_WORKFLOW
cpost /core/lakeshore/latency '{"chunkLatencyMs":600}' >/dev/null
check "one execution drafts, then authorizes" "$(wait_phase 3001 "$J" "$B4" AUTHORIZED 120)" AUTHORIZED

step "5. Hold on ••0033 -> park -> ops releases and retries"
# Upload first, then the hold (as in Scenario B): a hold placed before staging is caught by staging itself.
stage 3001 "$J" summit-payroll-5000; B5=$BATCH
check "Morgan places the hold (BankOps endpoint)" "$(core 3001 "$M" /accounts/4401220033/hold -X POST -H 'Content-Type: application/json' -d '{"onHold":true,"reason":"Fraud review"}' | jq -r .status)" OnHold
draft 3001 "$J" "$B5" K5 >/dev/null
for _ in $(seq 1 120); do
  P=$(parked 3001 "$J" "$B5")
  [ -n "$P" ] && [ "$(view 3001 "$J" "$B5" | jq '[.view.chunks[] | select(.state=="RUNNING" or .state=="PENDING")] | length')" = 0 ] && break
  sleep 1
done
check "chunks parked" "$([ -n "$P" ] && echo yes)" yes
for _ in $(seq 1 15); do Q=$(bw 3001 "$M" /ops/attention | jq -r --arg b "$B5" '[.[] | select(.batchId==$b)] | length'); [ "$Q" = 1 ] && break; sleep 1; done
check "Morgan's exception queue lists the batch" "$Q" 1
core 3001 "$M" /accounts/4401220033/hold -X POST -H 'Content-Type: application/json' -d '{"onHold":false}' >/dev/null
for c in $P; do resolve 3001 "$M" "$B5" "$c" RETRY >/dev/null; done
check "DRAFTED after release + RETRY" "$(wait_phase 3001 "$J" "$B5" DRAFTED 120)" DRAFTED

step "6. Daily limit exceeded"
stage 3001 "$J" summit-cold-chain-over-limit; B6=$BATCH
draft 3001 "$J" "$B6" K6 >/dev/null
check "DRAFTED" "$(wait_phase 3001 "$J" "$B6" DRAFTED 120)" DRAFTED
V=$(view 3001 "$J" "$B6")
check "group DENY DAILY_LIMIT_EXCEEDED, no park" "$(echo "$V" | jq -r '([.view.groups[] | select(.decision=="DENY") | .code] | unique | join(",")) + " " + (.view.needsAttention|tostring)')" "DAILY_LIMIT_EXCEEDED false"
check "denied group's 1,200 wires Rejected in Core" "$(genTx 3001 "$J" "$B6" | jq -r '.counts.rejected >= 1200')" true

step "7. Responses lost after commit"
stage 3001 "$J" summit-payroll-5000; B7=$BATCH
cpost /core/lakeshore/faults '{"mode":"lose-response","count":2}' >/dev/null
draft 3001 "$J" "$B7" K7 >/dev/null
check "DRAFTED" "$(wait_phase 3001 "$J" "$B7" DRAFTED 180)" DRAFTED
check "exact row count" "$(genTx 3001 "$J" "$B7" | jq -r '"\(.counts.total) \(.counts.drafted)"')" "5000 4997"

step "8. Invalid file"
stage 3001 "$J" summit-vendors-with-errors; B8=$BATCH
check "INVALID with 14 errors" "$(echo "$SUMMARY" | jq -r '"\(.status) \(.errorCount)"')" "INVALID 14"
check "error codes" "$(bw 3001 "$J" "/batches/$B8/staging?view=errors&limit=200" | jq -r '[.items[].code] | group_by(.) | map("\(.[0])=\(length)") | join(",")')" \
  "AMOUNT_RANGE=2,DATE_IN_PAST=2,LENGTH=1,ORIGINATING_ACCOUNT=2,REQUIRED=3,ROUTING_CHECKSUM=4"
check "draft refused" "$(draft 3001 "$J" "$B8" K8)" 409

step "9. Tenant isolation"
cpost /core/lakeshore/faults '{"mode":"outage","durationSeconds":120}' >/dev/null
stage 3002 "$R" harvest-2000; B9=$BATCH
draft 3002 "$R" "$B9" K9 >/dev/null
check "Prairie DRAFTED while Lakeshore Core is down" "$(wait_phase 3002 "$R" "$B9" DRAFTED 120)" DRAFTED
cpost /core/lakeshore/faults '{"mode":"none"}' >/dev/null
check "Lakeshore Core has no Prairie batch" "$(genTx 3001 "$M" "$B9" -o /dev/null -w '%{http_code}')" 404
check "Prairie Core has no Lakeshore batch" "$(genTx 3002 "$(login prairie BANK devon)" "$B" -o /dev/null -w '%{http_code}')" 404

step "10. Worker crash during a 10k draft"
cpost /reset >/dev/null
J=$(login lakeshore SUMMIT jordan)
stage 3001 "$J" summit-payroll-10000; B10=$BATCH
cpost /core/lakeshore/latency '{"chunkLatencyMs":1200}' >/dev/null
draft 3001 "$J" "$B10" K10 >/dev/null
sleep 8
check "kill worker-a" "$(cpost /containers/worker-a/kill | jq -r .result)" ok
check "DRAFTED" "$(wait_phase 3001 "$J" "$B10" DRAFTED 300)" DRAFTED
check "exact row count" "$(genTx 3001 "$J" "$B10" | jq -r '"\(.counts.total) \(.counts.drafted) \(.counts.rejected)"')" "10000 9994 6"
cpost /containers/worker-a/start >/dev/null

step "11. All workers down"
stage 3001 "$J" summit-payroll-5000; B11=$BATCH
draft 3001 "$J" "$B11" K11 >/dev/null
sleep 4
cpost /containers/worker-a/stop >/dev/null; cpost /containers/worker-b/stop >/dev/null
sleep 2
check "degraded.reason" "$(view 3001 "$J" "$B11" | jq -r .degraded.reason)" NO_WORKERS
cpost /containers/worker-a/start >/dev/null; cpost /containers/worker-b/start >/dev/null
check "completes after workers return" "$(wait_phase 3001 "$J" "$B11" DRAFTED 240)" DRAFTED
cpost /core/lakeshore/latency '{"chunkLatencyMs":600}' >/dev/null

step "12. Retry storm"
ST=$(cpost /load/retry-storm '{"fiId":"lakeshore","userId":"jordan","requests":20}')
check "20 responses" "$(echo "$ST" | jq -r '[.statusCounts[]] | add')" 20
check "exactly 1 execution" "$(echo "$ST" | jq -r .executions)" 1

step "13. Encryption"
I=$(curl -s "$C/encryption/inspect?workflowId=bulkwire/lakeshore/$B11")
OLDKEY=$(echo "$I" | jq -r '.payloads[0].metadata["encryption-key-id"]')
check "stored payloads are binary/encrypted" "$(echo "$I" | jq -r '[.payloads[].metadata.encoding] | unique | join(",")')" binary/encrypted
NEWKEY=$(cpost /encryption/rotate | jq -r .activeKeyId)
sleep 4
stage 3001 "$J" summit-cold-chain-over-limit; B13=$BATCH
draft 3001 "$J" "$B13" K13 >/dev/null
sleep 3
check "new batch carries the new key id" "$(curl -s "$C/encryption/inspect?workflowId=bulkwire/lakeshore/$B13" | jq -r '.payloads[0].metadata["encryption-key-id"]')" "$NEWKEY"
RAW=$(docker compose exec -T temporal temporal workflow show --namespace bulkwire --address 127.0.0.1:7233 -w "bulkwire/lakeshore/$B11" -o json | jq -c '{payloads: [.events[0].workflowExecutionStartedEventAttributes.input.payloads[0]]}')
check "old batch ($OLDKEY) still decodes via the Codec Server" "$(curl -s -X POST http://localhost:8089/decode -H 'Content-Type: application/json' -H 'X-Namespace: bulkwire' -d "$RAW" | jq -r '.payloads[0].data | @base64d | fromjson | .BatchID')" "$B11"

step "14. Fairness: per-FI limit 3/s during payroll day"
cpost /reset >/dev/null
cput /fairness '{"enabled":true,"rpsPerFi":3}' >/dev/null
L=$(cpost /load/payroll-day '{"batches":[{"fiId":"lakeshore","userId":"jordan","sample":"summit-payroll-10000","concurrency":4},{"fiId":"lakeshore","userId":"taylor","sample":"northline-payroll-10000","concurrency":4},{"fiId":"prairie","userId":"riley","sample":"harvest-6000","concurrency":4}]}')
sleep 12
MAX=0; SEEN=no
for _ in $(seq 1 10); do
  v=$(curl -s "$C/core/stats" | jq -r .lakeshore.callsPerSecond.last10s)
  [ -n "$v" ] && [ "$v" != null ] && SEEN=yes
  MAX=$(echo "$v $MAX" | awk '{print ($1 > $2) ? $1 : $2}')
  sleep 2
done
check "Lakeshore callsPerSecond (10 s) <= 3.5 over 20 s (max $MAX)" "$SEEN $(echo "$MAX" | awk '{print ($1 > 0 && $1 <= 3.5) ? "yes" : "no"}')" "yes yes"
R=$(login prairie HARVEST riley)
check "Prairie batch progresses" "$(wait_phase 3002 "$R" "$(echo "$L" | jq -r '.[] | select(.fiId=="prairie") | .batchId')" DRAFTED 240)" DRAFTED
cput /fairness '{"enabled":false,"rpsPerFi":0}' >/dev/null

cp /tmp/e2e-keyring.json config/.keyring.json.tmp && mv config/.keyring.json.tmp config/keyring.json
echo
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
