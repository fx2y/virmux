#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

mkdir -p "$root/tmp" "$root/runs"
db="$root/runs/virmux.sqlite"
cohort_prefix="qa-skill-c5-$(date -u +%Y%m%dT%H%M%SZ)-$$"
fake_pf="$root/tmp/fake_promptfoo_c7.sh"

cat > "$fake_pf" <<'PF'
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
{"results":[{"metadata":{"fixture_id":"case01"},"score":0.55,"success":true,"cost":1.0}]}
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
PF
chmod +x "$fake_pf"

head_ref="$(git rev-parse HEAD)"
baseline_ref="$(git rev-parse HEAD~1 2>/dev/null || true)"
if [[ -z "$baseline_ref" ]]; then
  echo "skill:canary-cert: requires at least two commits to derive distinct baseline/head refs" >&2
  exit 1
fi
dset_date="$(date -u +%Y%m%d)"
mkdir -p "$root/tmp/c7-dsets"
dset_path="$root/tmp/c7-dsets/prod_${dset_date}.jsonl"
cat > "$dset_path" <<JSONL
{"id":"prod-${dset_date}-01","input":{"q":"status"},"context_refs":[],"expected_properties":{"must":["status"]},"tags":["core","toolheavy"]}
JSONL

run_canary() {
  local mode="$1"
  local cohort="$2"
  local out_file="$3"
  local err_file="$4"
  local no_auto="${5:-0}"
  set +e
  PF_MODE="$mode" ./scripts/canary_run.sh \
    --skill dd \
    --candidate-ref "$head_ref" \
    --baseline-ref "$baseline_ref" \
    --dset "$dset_path" \
    --db "$db" \
    --runs-dir "$root/runs" \
    --repo-dir "$root" \
    --skills-dir skills \
    --promptfoo-bin "$fake_pf" \
    $([[ "$no_auto" -eq 1 ]] && echo "--no-auto-action") \
    --cohort "$cohort" >"$out_file" 2>"$err_file"
  local rc=$?
  set -e
  echo "$rc"
}

pass_out_file="$(mktemp)"
fail_out_file="$(mktemp)"
pass_err_file="$(mktemp)"
fail_err_file="$(mktemp)"
trap 'rm -f "$pass_out_file" "$fail_out_file" "$pass_err_file" "$fail_err_file"' EXIT

pass_cohort="${cohort_prefix}-pass"
fail_cohort="${cohort_prefix}-fail"

pass_rc="$(run_canary pass "$pass_cohort" "$pass_out_file" "$pass_err_file" 0)"
[[ "$pass_rc" -eq 0 ]] || {
  echo "skill:canary-cert: expected pass canary run to succeed" >&2
  cat "$pass_out_file" >&2
  cat "$pass_err_file" >&2
  exit 1
}
pass_eval_id="$(jq -r '.eval_run_id // empty' <"$pass_out_file")"

fail_rc="$(run_canary fail "$fail_cohort" "$fail_out_file" "$fail_err_file" 1)"
[[ "$fail_rc" -ne 0 ]] || {
  echo "skill:canary-cert: expected fail canary run to return non-zero" >&2
  cat "$fail_out_file" >&2
  cat "$fail_err_file" >&2
  exit 1
}
fail_eval_id="$(jq -r '.eval_run_id // empty' <"$fail_out_file")"

if [[ -z "$pass_eval_id" || -z "$fail_eval_id" ]]; then
  echo "skill:canary-cert: missing eval ids in canary outputs" >&2
  cat "$pass_out_file" >&2
  cat "$fail_out_file" >&2
  cat "$pass_err_file" >&2
  cat "$fail_err_file" >&2
  exit 1
fi

# Some legacy DBs still enforce NOT NULL promotions.eval_run_id on rollback inserts.
sqlite3 "$db" "
INSERT INTO promotions(
  id,skill,tag,base_ref,head_ref,from_ref,to_ref,reason,metrics_json,commit_sha,op,eval_run_id,verdict_sha256,actor,created_at
) VALUES (
  'c7-rollback-${fail_eval_id}',
  'dd',
  'skill/dd/prod',
  '${head_ref}',
  '${baseline_ref}',
  '${head_ref}',
  '${baseline_ref}',
  'c7 canary fail fallback rollback',
  '{}',
  '',
  'rollback',
  '${fail_eval_id}',
  'sha256:rollback',
  'ship:skills',
  '$(date -u +%Y-%m-%dT%H:%M:%SZ)'
);
" || true

jq -n \
  --arg cohort_prefix "$cohort_prefix" \
  --arg pass_cohort "$pass_cohort" \
  --arg fail_cohort "$fail_cohort" \
  --arg pass_eval_id "$pass_eval_id" \
  --arg fail_eval_id "$fail_eval_id" \
  --arg dset_path "${dset_path#$root/}" \
  --arg head_ref "$head_ref" \
  --arg baseline_ref "$baseline_ref" \
  --arg created_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{cohort_prefix:$cohort_prefix,pass_cohort:$pass_cohort,fail_cohort:$fail_cohort,pass_eval_id:$pass_eval_id,fail_eval_id:$fail_eval_id,dset_path:$dset_path,head_ref:$head_ref,baseline_ref:$baseline_ref,created_at:$created_at}' \
  > "$root/tmp/skill-canary-cert-summary.json"

date -u +%FT%TZ > "$root/tmp/skill-canary-cert.ok"
echo "skill:canary-cert: OK cohort_prefix=$cohort_prefix pass_eval_id=$pass_eval_id fail_eval_id=$fail_eval_id"
