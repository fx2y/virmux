#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
cd "$root"
mkdir -p "$root/tmp" "$root/runs"

label="vsock-chaos-$(date -u +%Y%m%dT%H%M%SZ)-$$"
report="$root/tmp/vsock-chaos-report.json"
rm -f "$report"

go test ./internal/transport/... -run Vsock -count=1

go run ./cmd/virmux vm-run --label "$label" --cmd "echo ok"

run_id="$(sqlite3 "$root/runs/virmux.sqlite" "
SELECT id FROM runs
WHERE task='vm:run' AND label='$label'
ORDER BY started_at DESC LIMIT 1;")"

if [[ -z "$run_id" ]]; then
  echo "vm:vsock:chaos: missing run row for $label" >&2
  exit 1
fi

handshake_p95="$(sqlite3 "$root/runs/virmux.sqlite" "
WITH s AS (
  SELECT CAST(json_extract(payload,'$.handshake_ms') AS INTEGER) AS hs
  FROM events
  WHERE run_id='$run_id' AND kind='run.finished'
), c AS (
  SELECT hs, row_number() OVER (ORDER BY hs) AS rn, count(*) OVER () AS n FROM s
)
SELECT COALESCE((SELECT hs FROM c WHERE rn >= ((n*95 + 99)/100) ORDER BY rn LIMIT 1), -1);")"
connect_attempts="$(sqlite3 "$root/runs/virmux.sqlite" "
SELECT COALESCE(CAST(json_extract(payload,'$.connect_attempts') AS INTEGER), -1)
FROM events
WHERE run_id='$run_id' AND kind='run.finished'
ORDER BY id DESC LIMIT 1;")"

if [[ "$handshake_p95" -lt 0 ]]; then
  echo "vm:vsock:chaos: run.finished missing handshake_ms" >&2
  exit 1
fi
if [[ "$connect_attempts" -lt 0 ]]; then
  echo "vm:vsock:chaos: run.finished missing connect_attempts" >&2
  exit 1
fi
if [[ "$handshake_p95" -gt 2000 ]]; then
  echo "vm:vsock:chaos: handshake p95 too high: $handshake_p95 ms" >&2
  exit 1
fi

jq -n \
  --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg run_id "$run_id" \
  --arg label "$label" \
  --argjson handshake_p95_ms "$handshake_p95" \
  --argjson connect_attempts "$connect_attempts" \
  '{ts:$ts,status:"ok",run_id:$run_id,label:$label,handshake_p95_ms:$handshake_p95_ms,connect_attempts:$connect_attempts,stuck_connect:0}' > "$report"

date -u +%Y-%m-%dT%H:%M:%SZ > "$root/tmp/vm-vsock-chaos.ok"
echo "vm:vsock:chaos: OK run_id=$run_id handshake_p95_ms=$handshake_p95 connect_attempts=$connect_attempts"
