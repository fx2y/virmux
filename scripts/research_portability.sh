#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

echo "--- Research Portability Test Started ---"

# 1. Run research
LABEL="portability-test-$(date +%s)"
QUERY="Portability Test"
echo "1. Running research..."
go run ./cmd/virmux research run --query "$QUERY" --label "$LABEL" > run_output.json
RUN_ID=$(cat run_output.json | jq -r .run_id)

# 2. Export
echo "2. Exporting run $RUN_ID..."
BUNDLE="tmp/$RUN_ID.tar.zst"
go run ./cmd/virmux export --run-id "$RUN_ID" --out "$BUNDLE"

# 3. Clean up local run
echo "3. Cleaning up local run data..."
rm -rf "runs/$RUN_ID"
sqlite3 runs/virmux.sqlite "PRAGMA foreign_keys = ON; DELETE FROM runs WHERE id='$RUN_ID';"

# Verify deletion
rows_left=$(sqlite3 runs/virmux.sqlite "SELECT COUNT(*) FROM runs WHERE id='$RUN_ID';")
if [ "$rows_left" -ne 0 ]; then
    echo "FAIL: run row not deleted before import"
    exit 1
fi
ev_left=$(sqlite3 runs/virmux.sqlite "SELECT COUNT(*) FROM evidence WHERE run_id='$RUN_ID';")
if [ "$ev_left" -ne 0 ]; then
    echo "FAIL: evidence rows not deleted by cascade"
    exit 1
fi
echo "Local data cleaned up successfully."

# 4. Import
echo "4. Importing bundle..."
go run ./cmd/virmux import --bundle "$BUNDLE"

# 5. Verify
echo "5. Verifying imported data..."
[ -f "runs/$RUN_ID/plan.yaml" ] || { echo "FAIL: plan.yaml missing after import"; exit 1; }
[ -d "runs/$RUN_ID/map" ] || { echo "FAIL: map dir missing after import"; exit 1; }
[ -f "runs/$RUN_ID/reduce/report.md" ] || { echo "FAIL: report.md missing after import"; exit 1; }

# Verify DB rows
rows_run="$(sqlite3 runs/virmux.sqlite "SELECT COUNT(*) FROM runs WHERE id='$RUN_ID';")"
[ "$rows_run" -eq 1 ] || { echo "FAIL: run row missing in DB after import"; exit 1; }

rows_evidence="$(sqlite3 runs/virmux.sqlite "SELECT COUNT(*) FROM evidence WHERE run_id='$RUN_ID';")"
[ "$rows_evidence" -ge 1 ] || { echo "FAIL: evidence rows missing in DB after import"; exit 1; }

rows_artifacts="$(sqlite3 runs/virmux.sqlite "SELECT COUNT(*) FROM artifacts WHERE run_id='$RUN_ID';")"
[ "$rows_artifacts" -ge 3 ] || { echo "FAIL: artifact rows missing in DB after import"; exit 1; }

echo "--- Research Portability Test PASSED ---"
rm run_output.json "$BUNDLE"
date -u +%FT%TZ > tmp/research-portability.ok
