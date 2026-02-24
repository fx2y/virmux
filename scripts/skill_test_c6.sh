#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

./scripts/skill_docs_drift.sh
go test ./cmd/virmux -run 'Test(DBCheckFailsHashMismatchWithoutMutation|DBCheckFailsMissingSchemaWithoutCreatingTables|ExportImportEvalBundleRoundTrip|NoParallelInTestsThatChdir|SkillDocsDriftScript)' -count=1

# Keep release-lane isolation hard: ship:core must not depend on skill lanes.
ship_core_block="$(awk '
  /^\[tasks\."ship:core"\]/{inblock=1}
  /^\[tasks\./ && !/^\[tasks\."ship:core"\]/{if(inblock){exit}; inblock=0}
  inblock{print}
' mise.toml)"
if grep -E 'skill:|ship:skills' <<<"$ship_core_block" >/dev/null; then
  echo "skill:test:c6: ship:core block references skill lanes (isolation breach)" >&2
  exit 1
fi

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/skill-test-c6.ok"
echo "skill:test:c6: OK"
