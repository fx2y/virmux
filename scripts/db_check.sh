#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
db="$root/runs/virmux.sqlite"

if [[ ! -f "$db" ]]; then
  echo "db:check: missing db: $db" >&2
  exit 1
fi

journal_mode="$(sqlite3 "$db" 'PRAGMA journal_mode;' | tr -d '[:space:]' | tr '[:upper:]' '[:lower:]')"
if [[ "$journal_mode" != "wal" ]]; then
  echo "db:check: WAL required, got $journal_mode" >&2
  exit 1
fi

fk_errs="$(sqlite3 "$db" 'PRAGMA foreign_key_check;' | wc -l)"
if [[ "$fk_errs" -ne 0 ]]; then
  echo "db:check: FK violations detected" >&2
  sqlite3 "$db" 'PRAGMA foreign_key_check;'
  exit 1
fi

required_indexes=(idx_events_run_id idx_runs_started_at)
for idx in "${required_indexes[@]}"; do
  c="$(sqlite3 "$db" "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='$idx';")"
  if [[ "$c" != "1" ]]; then
    echo "db:check: missing index: $idx" >&2
    exit 1
  fi
done

echo "db:check: OK"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/runs/.db-check.ok"
