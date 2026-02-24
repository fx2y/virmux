#!/usr/bin/env bash
set -euo pipefail

# Research End-to-End Certification Script
# Ensures plan -> map -> reduce -> replay lifecycle is intact and correct.

echo "--- Research Certification Started ---"

QUERY="Certification Test $(date +%s)"
echo "1. Running full research run..."
go run ./cmd/virmux research run --query "$QUERY" > run_output.json
RUN_ID=$(cat run_output.json | jq -r .run_id)

if [ -z "$RUN_ID" ] || [ "$RUN_ID" == "null" ]; then
    echo "FAIL: Could not determine run_id"
    exit 1
fi
echo "Run ID: $RUN_ID"

echo "2. Checking artifacts..."
[ -f "runs/$RUN_ID/plan.yaml" ] || { echo "FAIL: plan.yaml missing"; exit 1; }
[ -d "runs/$RUN_ID/map" ] || { echo "FAIL: map directory missing"; exit 1; }
[ -f "runs/$RUN_ID/reduce/report.md" ] || { echo "FAIL: report.md missing"; exit 1; }

echo "3. Checking timeline..."
go run ./cmd/virmux research timeline --run "$RUN_ID" | grep "research.plan.created" > /dev/null || { echo "FAIL: timeline missing plan event"; exit 1; }
go run ./cmd/virmux research timeline --run "$RUN_ID" | grep "research.map.track.done" > /dev/null || { echo "FAIL: timeline missing map events"; exit 1; }

echo "4. Running selective replay..."
# Replay track-1 (deep)
go run ./cmd/virmux research replay --run "$RUN_ID" --only track-1 > replay_output.json
REPLAY_RUN_ID=$(cat replay_output.json | jq -r .run_id)

echo "5. Checking replay timeline for mismatch..."
# Since track-1 is non-deterministic (timestamp added in C5), it SHOULD mismatch.
go run ./cmd/virmux research timeline --run "$REPLAY_RUN_ID" | grep "research.replay.mismatch" > /dev/null || { echo "FAIL: replay mismatch not detected for non-deterministic track"; exit 1; }

echo "6. Checking contradiction in report..."
go run ./cmd/virmux research reduce --run "$RUN_ID"
cat "runs/$RUN_ID/reduce/report.md" | grep "## Contradictions" > /dev/null || { echo "FAIL: report.md missing Contradictions section"; exit 1; }

echo "7. Testing deterministic bypass..."
# Mark track-1 as deterministic: false
# Use a robust way to insert deterministic: false after id: track-1
sed -i '/id: track-1/a \  deterministic: false' "runs/$RUN_ID/plan.yaml"

go run ./cmd/virmux research replay --run "$RUN_ID" --only track-1 > replay_nondet_output.json
REPLAY_NONDET_ID=$(cat replay_nondet_output.json | jq -r .run_id)
go run ./cmd/virmux research timeline --run "$REPLAY_NONDET_ID" | grep "research.replay.nondet_exception" > /dev/null || { echo "FAIL: nondet_exception not found"; exit 1; }

echo "--- Research Certification PASSED ---"
rm -f run_output.json replay_output.json replay_nondet_output.json
mkdir -p tmp
date > tmp/research-cert.ok
echo "{\"status\": \"ok\", \"ts\": \"$(date -u +%FT%TZ)\"}" > tmp/ship-research-summary.json
