#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

mkdir -p "$root/tmp" "$root/runs"
cohort="qa-skill-c3-$(date -u +%Y%m%dT%H%M%SZ)-$$"
db="$root/runs/virmux.sqlite"
fake_pf="$root/tmp/fake_promptfoo.sh"

cat > "$fake_pf" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
mode="${PF_MODE:-pass}"
cmd="$1"
shift || true
if [[ "$cmd" == "validate" ]]; then
  exit 0
fi
if [[ "$cmd" != "eval" ]]; then
  echo "unsupported fake promptfoo command: $cmd" >&2
  exit 2
fi
cfg=""
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -c) cfg="$2"; shift 2 ;;
    --output) out="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [[ -z "$out" ]]; then
  echo "fake promptfoo missing --output" >&2
  exit 2
fi
if [[ "$mode" == "pass" ]]; then
  if [[ "$cfg" == *".base."* ]]; then
    cat > "$out" <<JSON
{"results":[{"metadata":{"fixture_id":"case01"},"score":0.60,"success":true,"cost":1.0}]}
JSON
  else
    cat > "$out" <<JSON
{"results":[{"metadata":{"fixture_id":"case01"},"score":0.80,"success":true,"cost":1.0}]}
JSON
  fi
else
  if [[ "$cfg" == *".base."* ]]; then
    cat > "$out" <<JSON
{"results":[{"metadata":{"fixture_id":"case01"},"score":0.90,"success":true,"cost":1.0}]}
JSON
  else
    cat > "$out" <<JSON
{"results":[{"metadata":{"fixture_id":"case01"},"score":0.20,"success":false,"cost":1.0}]}
JSON
  fi
fi
SH
chmod +x "$fake_pf"

pass_json="$(PF_MODE=pass go run ./cmd/virmux skill ab --db "$db" --runs-dir "$root/runs" --repo-dir "$root" --skills-dir skills --promptfoo-bin "$fake_pf" --cohort "$cohort" dd HEAD~0..HEAD)"
pass_id="$(jq -r '.id' <<<"$pass_json")"
if [[ -z "$pass_id" || "$pass_id" == "null" ]]; then
  echo "skill:ab: missing pass eval id" >&2
  exit 1
fi

if go run ./cmd/virmux skill promote --db "$db" --repo-dir "$root" dd missing-eval-id >/dev/null 2>&1; then
  echo "skill:ab: expected promote missing-eval refusal" >&2
  exit 1
fi

go run ./cmd/virmux skill promote --db "$db" --repo-dir "$root" --tag "skill/dd/c3-test" dd "$pass_id" >/dev/null

if PF_MODE=fail go run ./cmd/virmux skill ab --db "$db" --runs-dir "$root/runs" --repo-dir "$root" --skills-dir skills --promptfoo-bin "$fake_pf" --cohort "$cohort" dd HEAD~0..HEAD >/dev/null 2>&1; then
  echo "skill:ab: expected AB regression failure" >&2
  exit 1
fi

jq -n --arg cohort "$cohort" --arg pass_id "$pass_id" '{cohort:$cohort,pass_eval_id:$pass_id,promotion_tag:"skill/dd/c3-test"}' > "$root/tmp/skill-ab-summary.json"
date -u +%FT%TZ > "$root/tmp/skill-ab.ok"
echo "skill:ab: OK cohort=$cohort pass_eval_id=$pass_id"
