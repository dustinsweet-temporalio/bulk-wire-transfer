#!/bin/sh
# temporal-init: one-shot job that prepares namespace "bulkwire" for the bulk wire system.
#
# Creates the namespace (if the dev server did not) and the five custom Search Attributes, then lists
# them and FAILS unless every one exists with the right type. Workers and bulkwire-service verify the
# same thing at startup; this script is the place that documents what a real namespace needs.
# The same commands work against a Temporal Cloud namespace (with an API key in the environment).
#
# PATTERN: Search Attributes for Operations — https://docs.temporal.io/search-attribute
# Idempotent: creating an existing namespace or Search Attribute is not an error for this flow.
set -eu

NS="${TEMPORAL_NAMESPACE:-bulkwire}"

temporal operator namespace describe --namespace "$NS" >/dev/null 2>&1 \
  || temporal operator namespace create --namespace "$NS" --retention 72h

# WHY these five, all non-PII: Search Attributes are indexed and NOT encrypted by the payload codec.
for attr in BulkWireStatus:Keyword FiId:Keyword CustomerId:Keyword InitiatedBy:Keyword NeedsAttention:Bool; do
  temporal operator search-attribute create --namespace "$NS" --name "${attr%%:*}" --type "${attr##*:}"
done

LIST="$(temporal operator search-attribute list --namespace "$NS" -o json)"
missing=0
for attr in BulkWireStatus:KEYWORD FiId:KEYWORD CustomerId:KEYWORD InitiatedBy:KEYWORD NeedsAttention:BOOL; do
  name="${attr%%:*}"; type="${attr##*:}"
  if echo "$LIST" | grep -q "\"$name\": \"INDEXED_VALUE_TYPE_$type\""; then
    echo "ok      $name ($type)"
  else
    echo "MISSING $name ($type) on namespace $NS" >&2
    missing=1
  fi
done
exit "$missing"
