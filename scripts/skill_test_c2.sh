#!/usr/bin/env bash
set -euo pipefail

go test ./internal/skill/judge -count=1
go test ./internal/skill/run -count=1 -run 'Test(CompareReplayHashes|Evaluate|VerifyReplayHashes)'
go test ./cmd/virmux -count=1 -run 'Test(CmdSkillJudgeWritesScoreRowsAndTraceEvent|ExportImportDeterministicRoundTrip)'

mkdir -p tmp
date -u +%FT%TZ > tmp/skill-test-c2.ok
