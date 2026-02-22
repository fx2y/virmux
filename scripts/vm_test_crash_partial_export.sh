#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
go test ./cmd/virmux -run 'TestClassifyRunFailure|TestMaybeAutoExportFailureEmitsPartialEventAndArtifact|TestExportRunBundleMarksPartialMeta' -count=1
mkdir -p "$root/tmp"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/vm-test-crash-partial-export.ok"

