#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

docs=(
  "docs/rfcs/000-ghostfleet-compounding-os.md"
  "docs/rfcs/000-ghostfleet-compounding-os/01-walkthroughs.md"
  "docs/rfcs/000-ghostfleet-compounding-os/02-snippets.md"
)

# Fail on stale claims that conflict with spec-04 canon.
if rg -n "skills/<name>/\{prompt\.md|prompt\.md\s+# behavior" "${docs[@]}" >/dev/null; then
  echo "skill:docs-drift: stale skill canon claim (prompt.md as primary) in RFC docs" >&2
  exit 1
fi
if rg -n "ghostfleet (judge run|ab run|promote skill@|skill refine suggest)" "${docs[@]}" >/dev/null; then
  echo "skill:docs-drift: stale command examples (non-canonical skill subcommands) in RFC docs" >&2
  exit 1
fi

# Positive assertions: docs must carry current canon anchors.
rg -n "skills/<name>/\{SKILL\.md,tools\.yaml,rubric\.yaml,tests/\*\}" docs/rfcs/000-ghostfleet-compounding-os.md >/dev/null || {
  echo "skill:docs-drift: missing SKILL.md dir contract in RFC docs" >&2
  exit 1
}
rg -n -F "virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>" spec-0/04-htn.jsonl >/dev/null || {
  echo "skill:docs-drift: missing command canon anchor in spec-0/04-htn.jsonl" >&2
  exit 1
}
rg -n "trace\\.ndjson" AGENTS.md >/dev/null || {
  echo "skill:docs-drift: missing trace.ndjson canon in AGENTS.md" >&2
  exit 1
}

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/skill-docs-drift.ok"
echo "skill:docs-drift: OK"
