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
if rg -n "ghostfleet (eval run|judge run|ab run|promote( skill@)?|rollback|skill refine suggest)" "${docs[@]}" >/dev/null; then
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
rg -n -F "\"cmd_canon\":\"virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>\"" spec-0/05/cli-map.jsonl >/dev/null || {
  echo "skill:docs-drift: missing spec-05 cli canon map anchor" >&2
  exit 1
}
rg -n -F "\"id\":\"map.cli.ghostfleet->virmux\"" spec-0/05/cli-map.jsonl >/dev/null || {
  echo "skill:docs-drift: missing spec-05 translation map id" >&2
  exit 1
}
rg -n "db:check validator-only" spec-0/05/56-c6-portability-hardening.jsonl >/dev/null || {
  echo "skill:docs-drift: missing C6 db:check validator-only anchor" >&2
  exit 1
}
rg -n "eval_runs,eval_cases,promotions,experiments,comparisons,suggest_runs" spec-0/05/c0-data-map.jsonl >/dev/null || {
  echo "skill:docs-drift: missing C0 bundle scope anchor" >&2
  exit 1
}
rg -n "trace\\.ndjson" AGENTS.md >/dev/null || {
  echo "skill:docs-drift: missing trace.ndjson canon in AGENTS.md" >&2
  exit 1
}
rg -n "SKILL\\.md.*SoT" AGENTS.md >/dev/null || {
  echo "skill:docs-drift: missing SKILL.md canon in AGENTS.md" >&2
  exit 1
}

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/skill-docs-drift.ok"
echo "skill:docs-drift: OK"
