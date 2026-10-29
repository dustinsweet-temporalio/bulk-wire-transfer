#!/bin/bash
# init.sh <fiId> — one-shot schema + seed for ONE FI's Core database, run by sqlinit-<fiId> against that FI's
# own SQL Server (SQL_HOST).
#
# Creates Core_<fiId> only if it does not exist, then runs 01-schema, 02-types, 03-procs, and that FI's seed
# with `sqlcmd -b` (stop on the first error). When the database already exists, only 03-procs runs again:
# stored procedures are code (every one is CREATE OR ALTER), so an updated repository takes effect on the next
# `make up` without wiping the bank's data. If any script fails, the half-built database is dropped and
# the job exits non-zero, so core-api (which depends on this job completing successfully) never starts
# against a half-initialized database. `make reset` deletes the volume and rebuilds from scratch.
set -euo pipefail

FI="${1:?usage: init.sh <fiId>}"
DB="Core_${FI}"
SQLCMD=/opt/mssql-tools18/bin/sqlcmd
# The password is passed through the environment, not argv, so it never appears in a process listing.
export SQLCMDPASSWORD="${MSSQL_SA_PASSWORD:?MSSQL_SA_PASSWORD is required}"
sql() { "$SQLCMD" -C -S "${SQL_HOST:?SQL_HOST is required}" -U sa -b "$@"; }

case "$FI" in
  lakeshore) SEED=/sql/04-seed-lakeshore.sql ;;
  prairie)   SEED=/sql/05-seed-prairie.sql ;;
  *) echo "unknown FI '$FI'" >&2; exit 2 ;;
esac

echo "[$FI] waiting for SQL Server at $SQL_HOST"
for i in $(seq 1 90); do
  sql -Q "SELECT 1" -o /dev/null && break
  [ "$i" = 90 ] && { echo "[$FI] SQL Server never became ready" >&2; exit 1; }
  sleep 2
done

exists="$(sql -h -1 -W -Q "SET NOCOUNT ON; SELECT CASE WHEN DB_ID('$DB') IS NULL THEN 0 ELSE 1 END")"
if [ "$(echo "$exists" | tr -d '[:space:]')" = "1" ]; then
  echo "[$FI] database $DB already exists; re-applying stored procedures"
  sql -d "$DB" -i /sql/03-procs.sql
  echo "[$FI] $DB procedures up to date"
  exit 0
fi

echo "[$FI] creating $DB"
sql -v DbName="$DB" -i /sql/00-database.sql

drop_on_failure() {
  echo "[$FI] initialization failed; dropping $DB so the next run starts clean" >&2
  sql -Q "ALTER DATABASE [$DB] SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE [$DB];" || true
}
trap drop_on_failure ERR

for f in /sql/01-schema.sql /sql/02-types.sql /sql/03-procs.sql "$SEED"; do
  echo "[$FI] running $(basename "$f")"
  sql -d "$DB" -i "$f"
done

trap - ERR
echo "[$FI] $DB initialized"
