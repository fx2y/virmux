#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

skill_file="$root/skills/dd/SKILL.md"
fixture_file="$(find "$root/skills/dd/tests" -type f -name '*.json' | sort | head -n1)"
if [[ ! -f "$skill_file" || -z "$fixture_file" ]]; then
  echo "skill:eval: missing skills/dd fixtures" >&2
  exit 1
fi

body="$(awk 'BEGIN{d=0} /^---$/ {d++; next} d>=2 {print}' "$skill_file")"
fixture_id="$(jq -r '.id // "case01"' "$fixture_file")"
mkdir -p "$root/tmp"
cfg="$root/tmp/skill-promptfoo-validate.json"

jq -n \
  --arg body "$body" \
  --arg fixture_id "$fixture_id" \
  '{description:"virmux skill eval validate",prompts:[$body],providers:["openai:gpt-4.1-mini"],tests:[{vars:{fixture_id:$fixture_id,fixture_json:"{}"},metadata:{fixture_id:$fixture_id}}]}' \
  > "$cfg"

if [[ -x "$root/node_modules/.bin/promptfoo" ]]; then
  "$root/node_modules/.bin/promptfoo" validate -c "$cfg" >/dev/null
else
  npx --yes promptfoo@0.118.0 validate -c "$cfg" >/dev/null
fi

date -u +%FT%TZ > "$root/tmp/skill-eval.ok"
echo "skill:eval: OK cfg=$cfg"
