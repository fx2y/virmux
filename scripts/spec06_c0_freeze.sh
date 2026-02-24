#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

required=(
  "spec-0/06-htn.jsonl"
  "spec-0/06-prime.jsonl"
  "spec-0/06/c0-cli-map.jsonl"
  "spec-0/06/c0-data-map.jsonl"
  "spec-0/06/c0-seams.jsonl"
  "spec-0/06/c0-guard-matrix.jsonl"
  "spec-0/06-tasks.jsonl"
)

for f in "${required[@]}"; do
  [[ -f "$f" ]] || { echo "spec06:c0: missing required artifact $f" >&2; exit 1; }
done

jq -e '
  select(.id=="map.cli.canon")
  | .cmd_canon=="virmux research <plan|map|reduce|replay|run>"
' spec-0/06/c0-cli-map.jsonl >/dev/null || {
  echo "spec06:c0: missing canonical research command map row" >&2
  exit 1
}

jq -e '
  select(.id=="map.cli.alias-policy")
  | .alias==false and .why!=""
' spec-0/06/c0-cli-map.jsonl >/dev/null || {
  echo "spec06:c0: missing explicit no-alias policy row" >&2
  exit 1
}

jq -e '
  select(.id=="map.data.trace")
  | .canonical=="runs/<id>/trace.ndjson"
  and .compat=="runs/<id>/trace.jsonl"
' spec-0/06/c0-data-map.jsonl >/dev/null || {
  echo "spec06:c0: missing canonical trace path row" >&2
  exit 1
}

for svc in planner scheduler mapper reducer replay; do
  jq -e --arg svc "$svc" '
    select(.k=="seam" and .svc==$svc)
    | (.anch | type=="array" and length>0)
  ' spec-0/06/c0-seams.jsonl >/dev/null || {
    echo "spec06:c0: seam $svc missing anchor list" >&2
    exit 1
  }
done

jq -s -e '
  [ .[] | select(.k=="guard") | ((.owner|type=="string") and (.owner|length>0)) ]
  | length > 0 and all
' spec-0/06/c0-guard-matrix.jsonl >/dev/null || {
  echo "spec06:c0: every guard row must declare owner" >&2
  exit 1
}

covers_tmp="$(mktemp)"
gaps_tmp="$(mktemp)"
risk_tmp="$(mktemp)"
missing_tmp="$(mktemp)"
trap 'rm -f "$covers_tmp" "$gaps_tmp" "$risk_tmp" "$missing_tmp"' EXIT

jq -r 'select(.k=="guard") | .covers[]?' spec-0/06/c0-guard-matrix.jsonl | sort -u > "$covers_tmp"
jq -r 'select(.k=="gap") | .id' spec-0/06-prime.jsonl | sort -u > "$gaps_tmp"
jq -r '
  select(
    .k=="risk" and
    (
      .id=="plan_first_before_tools.risk" or
      (.id|startswith("spec06.")) or
      (.id|startswith("anti_pattern."))
    )
  )
  | .id
' spec-0/06-prime.jsonl | sort -u > "$risk_tmp"

comm -23 "$gaps_tmp" "$covers_tmp" > "$missing_tmp" || true
if [[ -s "$missing_tmp" ]]; then
  echo "spec06:c0: uncovered prime gaps:" >&2
  cat "$missing_tmp" >&2
  exit 1
fi

comm -23 "$risk_tmp" "$covers_tmp" > "$missing_tmp" || true
if [[ -s "$missing_tmp" ]]; then
  echo "spec06:c0: uncovered prime risks:" >&2
  cat "$missing_tmp" >&2
  exit 1
fi

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/spec06-c0-freeze.ok"
echo "spec06:c0: OK"
