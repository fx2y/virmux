#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

docs=(
  "docs/ops/spec06-card.md"
  "docs/ops/spec06-rollback-playbook.md"
  "spec-0/06-htn.jsonl"
  "spec-0/06/c0-cli-map.jsonl"
)

docs_for_stale_check=(
  "docs/ops/spec06-card.md"
  "docs/ops/spec06-rollback-playbook.md"
  "spec-0/06-htn.jsonl"
)

# Fail on stale 'ghostfleet research' examples (it should be 'virmux research').
if rg -n "ghostfleet research" "${docs_for_stale_check[@]}" >/dev/null; then
  echo "research:docs-drift: stale command examples (ghostfleet research) in docs" >&2
  exit 1
fi

# Positive assertions: docs must carry current canon anchors.
# Use -F (fixed strings) and correct quoting.
rg -n -F "virmux research <plan|map|reduce|replay|run>" spec-0/06/50-c0-translation-seams.jsonl >/dev/null || {
  echo "research:docs-drift: missing command canon anchor in spec-0/06/50-c0-translation-seams.jsonl" >&2
  exit 1
}

rg -n -F '"cmd_canon":"virmux research <plan|map|reduce|replay|run>"' spec-0/06/c0-cli-map.jsonl >/dev/null || {
  echo "research:docs-drift: missing spec-06 cli canon map anchor" >&2
  exit 1
}

rg -n "virmux research plan --query" docs/ops/spec06-card.md >/dev/null || {
  echo "research:docs-drift: missing plan command in spec06 card" >&2
  exit 1
}

rg -n "virmux research replay" docs/ops/spec06-rollback-playbook.md >/dev/null || {
  echo "research:docs-drift: missing replay command in rollback playbook" >&2
  exit 1
}

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/research-docs-drift.ok"
echo "research:docs-drift: OK"
