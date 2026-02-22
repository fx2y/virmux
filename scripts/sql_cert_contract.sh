#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
db="${1:-$root/runs/virmux.sqlite}"
label_glob="${VIRMUX_CERT_LABEL_GLOB:-qa-cert-%}"

if [[ ! -f "$db" ]]; then
  echo "qa:sql-contract: missing db: $db" >&2
  exit 1
fi

resume_ok_count="$(sqlite3 "$db" "
SELECT COUNT(*)
FROM runs
WHERE task='vm:resume'
  AND status='ok'
  AND label LIKE '$label_glob';
")"
if [[ "$resume_ok_count" -lt 1 ]]; then
  echo "qa:sql-contract: expected >=1 vm:resume ok row for label_glob=$label_glob, got $resume_ok_count" >&2
  exit 1
fi

missing_keys_count="$(sqlite3 "$db" "
SELECT COUNT(*)
FROM events e
JOIN runs r ON r.id=e.run_id
WHERE e.kind='run.finished'
  AND r.task='vm:resume'
  AND r.label LIKE '$label_glob'
  AND (
    json_extract(e.payload,'$.resume_mode') IS NULL OR
    json_extract(e.payload,'$.resume_source') IS NULL OR
    json_extract(e.payload,'$.resume_error') IS NULL
  );
")"
if [[ "$missing_keys_count" -ne 0 ]]; then
  echo "qa:sql-contract: missing resume telemetry keys for label_glob=$label_glob: $missing_keys_count" >&2
  exit 1
fi

echo "qa:sql-contract: OK (label_glob=$label_glob)"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/qa-sql-contract.ok"
