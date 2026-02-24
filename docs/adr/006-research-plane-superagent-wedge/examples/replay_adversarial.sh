#!/bin/bash
# ADR-006: Adversarial Replay Drill

Q="agent market drill $(date +%s)"
RUN_JSON="/tmp/s06.run.json"

echo "1. Initial Run..."
go run ./cmd/virmux research run --query "$Q" --label adr-006-drill | tee $RUN_JSON
RID=$(jq -r .run_id $RUN_JSON)

echo "2. Replay with invalid selector (Fail-Closed)..."
go run ./cmd/virmux research replay --run $RID --only ghost-track 2>&1 | grep "RERUN_SELECTOR_INVALID"

echo "3. Tamper plan (Strict Parse)..."
sed -i 's/tracks:/broken_tracks:/' runs/$RID/plan.yaml
go run ./cmd/virmux research map --run $RID 2>&1 | grep "PLAN_SCHEMA_INVALID"

echo "4. Restore and Replay (Deterministic)..."
sed -i 's/broken_tracks:/tracks:/' runs/$RID/plan.yaml
go run ./cmd/virmux research replay --run $RID --only track-1
test -f runs/$RID/mismatch.json && echo "FAIL: Mismatch on deterministic track" || echo "PASS"
