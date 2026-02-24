#!/usr/bin/env bash
set -euo pipefail

# Research End-to-End Certification Script
# Ensures plan -> map -> reduce -> replay lifecycle is intact and correct.

echo "--- Research Certification Started ---"

CERT_ID=$(date +%s)
LABEL="research-cert-$CERT_ID"
CERT_TS=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

QUERY="Certification Test $CERT_ID"
echo "1. Running full research run with label $LABEL..."
go run ./cmd/virmux research run --query "$QUERY" --label "$LABEL" > run_output.json
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

echo "4. Running selective replay with label $LABEL..."
# Replay track-1 (deep)
go run ./cmd/virmux research replay --run "$RUN_ID" --only track-1 --label "$LABEL" > replay_output.json
REPLAY_RUN_ID=$(cat replay_output.json | jq -r .run_id)

echo "5. Checking replay timeline for mismatch..."
# Default deterministic tracks must not emit replay mismatches.
if go run ./cmd/virmux research timeline --run "$REPLAY_RUN_ID" | grep "research.replay.mismatch" > /dev/null; then
    echo "FAIL: deterministic replay emitted mismatch"
    exit 1
fi

echo "6. Checking contradiction in report..."
go run ./cmd/virmux research reduce --run "$RUN_ID" --label "$LABEL"
cat "runs/$RUN_ID/reduce/report.md" | grep "## Contradictions" > /dev/null || { echo "FAIL: report.md missing Contradictions section"; exit 1; }

echo "7. Testing deterministic bypass with label $LABEL..."
# Mark track-1 as deterministic: false
# Use a robust way to insert deterministic: false after id: track-1
sed -i '/id: track-1/a \  deterministic: false' "runs/$RUN_ID/plan.yaml"

go run ./cmd/virmux research replay --run "$RUN_ID" --only track-1 --label "$LABEL" > replay_nondet_output.json
REPLAY_NONDET_ID=$(cat replay_nondet_output.json | jq -r .run_id)
go run ./cmd/virmux research timeline --run "$REPLAY_NONDET_ID" | grep "research.replay.nondet_exception" > /dev/null || { echo "FAIL: nondet_exception not found"; exit 1; }

echo "8. Running SQL certification..."
bash scripts/research_sql_cert.sh --label-glob "$LABEL" --cert-ts "$CERT_TS"

echo "9. Running docs drift check..."
bash scripts/research_docs_drift.sh

echo "10. Running portability test..."
bash scripts/research_portability.sh

echo "11. Running parallel scheduler guards..."
go test ./internal/skill/research -run 'TestSchedulerTopo|TestSchedulerFailure|TestSchedulerReturnsWorkerInfraError|TestSchedulerOnlyMissingDependencyDoesNotDeadlock' > /dev/null
mkdir -p tmp
date > tmp/research-parallel.ok

echo "12. Generating DoD matrix..."
bash scripts/spec06_dod_matrix.sh

echo "--- Research Certification PASSED ---"
rm -f run_output.json replay_output.json replay_nondet_output.json
date > tmp/research-cert.ok
echo "{\"status\": \"ok\", \"ts\": \"$(date -u +%FT%TZ)\"}" > tmp/ship-research-summary.json
