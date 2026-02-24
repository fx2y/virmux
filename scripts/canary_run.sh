#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

skill=""
candidate_ref=""
baseline_ref=""
dset=""
repo_dir="$root"
skills_dir="skills"
runs_dir="$root/runs"
db="$root/runs/virmux.sqlite"
promptfoo_bin="promptfoo"
provider="openai:gpt-4.1-mini"
cohort=""
max_age_hours=24
auto_action=1

usage() {
  echo "usage: scripts/canary_run.sh --skill <name> --candidate-ref <ref> [--baseline-ref <ref>] [--dset <path>] [--db <path>] [--runs-dir <dir>] [--repo-dir <dir>] [--skills-dir <dir>] [--promptfoo-bin <bin>] [--provider <provider>] [--cohort <label>] [--max-age-hours <n>] [--no-auto-action]" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skill) skill="$2"; shift 2 ;;
    --candidate-ref) candidate_ref="$2"; shift 2 ;;
    --baseline-ref) baseline_ref="$2"; shift 2 ;;
    --dset) dset="$2"; shift 2 ;;
    --db) db="$2"; shift 2 ;;
    --runs-dir) runs_dir="$2"; shift 2 ;;
    --repo-dir) repo_dir="$2"; shift 2 ;;
    --skills-dir) skills_dir="$2"; shift 2 ;;
    --promptfoo-bin) promptfoo_bin="$2"; shift 2 ;;
    --provider) provider="$2"; shift 2 ;;
    --cohort) cohort="$2"; shift 2 ;;
    --max-age-hours) max_age_hours="$2"; shift 2 ;;
    --no-auto-action) auto_action=0; shift ;;
    *) usage ;;
  esac
done

[[ -n "$skill" && -n "$candidate_ref" ]] || usage
[[ "$skill" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || { echo "canary:run: invalid --skill token (expected kebab-case)" >&2; exit 1; }
[[ "$max_age_hours" =~ ^[0-9]+$ ]] || { echo "canary:run: --max-age-hours must be integer" >&2; exit 1; }
[[ -f "$db" ]] || { echo "canary:run: missing db $db" >&2; exit 1; }

if [[ -z "$baseline_ref" ]]; then
  if git -C "$repo_dir" rev-parse --verify "skill/$skill/prod" >/dev/null 2>&1; then
    baseline_ref="skill/$skill/prod"
  else
    echo "canary:run: missing --baseline-ref and no skill/$skill/prod tag" >&2
    exit 1
  fi
fi

if [[ -z "$dset" ]]; then
  dset="$(ls -1 "$root"/dsets/prod_*.jsonl 2>/dev/null | sort | tail -n1 || true)"
fi
[[ -n "$dset" && -f "$dset" ]] || { echo "canary:run: missing dset; run scripts/canary_snapshot.sh first" >&2; exit 1; }

if [[ -z "$cohort" ]]; then
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  cand_short="$(printf '%s' "$candidate_ref" | tr -cd '[:alnum:]' | cut -c1-12)"
  cohort="qa-skill-c5-${ts}-${cand_short:-head}"
fi

ab_out_file="$(mktemp)"
trap 'rm -f "$ab_out_file"' EXIT

set +e
go run ./cmd/virmux skill ab \
  --db "$db" \
  --runs-dir "$runs_dir" \
  --repo-dir "$repo_dir" \
  --skills-dir "$skills_dir" \
  --promptfoo-bin "$promptfoo_bin" \
  --provider "$provider" \
  --cohort "$cohort" \
  --judge pairwise \
  --anti-tie \
  "$skill" "${baseline_ref}..${candidate_ref}" >"$ab_out_file" 2>&1
ab_rc=$?
set -e

ab_json="$(grep -E '^\{.*\}$' "$ab_out_file" | tail -n1 || true)"
eval_id="$(jq -r '.id // empty' <<<"$ab_json" 2>/dev/null || true)"
if [[ -z "$eval_id" ]]; then
  echo "canary:run: failed to parse eval id from skill ab output" >&2
  cat "$ab_out_file" >&2
  exit 1
fi

dset_sha="$(sha256sum "$dset" | awk '{print $1}')"
dset_count="$(wc -l < "$dset" | tr -d ' ')"

esc() { printf "%s" "$1" | sed "s/'/''/g"; }
IFS='|' read -r pass score_delta fail_delta cost_delta <<<"$(sqlite3 "$db" "SELECT pass,score_p50_delta,fail_rate_delta,cost_delta FROM eval_runs WHERE id='$(esc "$eval_id")';")"
if [[ -z "$pass" ]]; then
  echo "canary:run: eval row not found for $eval_id" >&2
  exit 1
fi

curated_eval_id="$(sqlite3 "$db" "SELECT id FROM eval_runs WHERE skill='$(esc "$skill")' AND cohort LIKE 'qa-skill-c3-%' AND pass=1 ORDER BY datetime(created_at) DESC, id DESC LIMIT 1;")"
caught_by_canary=0
if [[ "$pass" -eq 0 && -n "$curated_eval_id" ]]; then
  caught_by_canary=1
fi

action="none"
action_ref=""
backlog_path=""
action_stdout=""
action_stderr=""
action_rc=0
action_error=""

if [[ "$pass" -eq 1 ]]; then
  action="promote"
  action_ref="$candidate_ref"
else
  action="rollback"
  action_ref="$baseline_ref"
fi

if [[ "$auto_action" -eq 1 ]]; then
  action_out_file="$(mktemp)"
  action_err_file="$(mktemp)"
  trap 'rm -f "$ab_out_file" "$action_out_file" "$action_err_file"' EXIT
  if [[ "$action" == "promote" ]]; then
    set +e
    go run ./cmd/virmux skill promote --db "$db" --repo-dir "$repo_dir" --max-age-hours "$max_age_hours" --reason "canary pass eval=${eval_id} dset=$(basename "$dset")" "$skill" "$eval_id" >"$action_out_file" 2>"$action_err_file"
    action_rc=$?
    set -e
  else
    eval_dir="$runs_dir/$eval_id"
    mkdir -p "$eval_dir"
    backlog_path="$eval_dir/canary-backlog.md"
    {
      echo "# Canary Regression Backlog"
      echo
      echo "- skill: $skill"
      echo "- eval_id: $eval_id"
      echo "- cohort: $cohort"
      echo "- dset: ${dset#$root/}"
      echo "- baseline_ref: $baseline_ref"
      echo "- candidate_ref: $candidate_ref"
      echo "- fail_rate_delta: $fail_delta"
      echo "- score_p50_delta: $score_delta"
      echo "- cost_delta: $cost_delta"
      echo
      echo "## Top failing fixtures (head failed, base passed)"
      sqlite3 -line "$db" "SELECT fixture_id, base_score, head_score FROM eval_cases WHERE eval_run_id='$(esc "$eval_id")' AND base_pass=1 AND head_pass=0 ORDER BY (base_score-head_score) DESC, fixture_id ASC LIMIT 10;"
    } > "$backlog_path"
    set +e
    go run ./cmd/virmux skill promote --db "$db" --repo-dir "$repo_dir" --rollback --to-ref "$baseline_ref" --reason "canary regression eval=${eval_id} dset=$(basename "$dset")" "$skill" >"$action_out_file" 2>"$action_err_file"
    action_rc=$?
    set -e
  fi
  action_stdout="$(cat "$action_out_file")"
  action_stderr="$(cat "$action_err_file")"
  if [[ "$action_rc" -ne 0 ]]; then
    action_error="auto-action failed rc=$action_rc"
  fi
fi

summary_path="$runs_dir/$eval_id/canary-summary.json"
mkdir -p "$(dirname "$summary_path")"

jq -n -S \
  --arg id "${eval_id}-canary" \
  --arg skill "$skill" \
  --arg eval_run_id "$eval_id" \
  --arg curated_eval_run_id "$curated_eval_id" \
  --arg cohort "$cohort" \
  --arg dset_path "${dset#$root/}" \
  --arg dset_sha256 "$dset_sha" \
  --arg baseline_ref "$baseline_ref" \
  --arg candidate_ref "$candidate_ref" \
  --arg action "$action" \
  --arg action_ref "$action_ref" \
  --arg backlog_path "${backlog_path#$root/}" \
  --arg action_stdout "$action_stdout" \
  --arg action_stderr "$action_stderr" \
  --arg action_error "$action_error" \
  --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --argjson ab_rc "$ab_rc" \
  --argjson action_rc "$action_rc" \
  --argjson pass "$pass" \
  --argjson dset_count "$dset_count" \
  --argjson score_delta "$score_delta" \
  --argjson fail_delta "$fail_delta" \
  --argjson cost_delta "$cost_delta" \
  --argjson caught_by_canary "$caught_by_canary" \
  --argjson auto_action "$auto_action" \
  '{id:$id,skill:$skill,eval_run_id:$eval_run_id,curated_eval_run_id:$curated_eval_run_id,cohort:$cohort,dset_path:$dset_path,dset_sha256:$dset_sha256,dset_count:$dset_count,baseline_ref:$baseline_ref,candidate_ref:$candidate_ref,gates:{pass:($pass==1),score_p50_delta:$score_delta,fail_rate_delta:$fail_delta,cost_delta:$cost_delta,ab_exit_code:$ab_rc},action:{selected:$action,ref:$action_ref,auto:($auto_action==1),exit_code:$action_rc,stdout:$action_stdout,stderr:$action_stderr,error:$action_error},caught_by_canary:($caught_by_canary==1),backlog_path:$backlog_path,generated_at:$generated_at}' \
  > "$summary_path"
summary_json="$(jq -c '{pass:.gates.pass,score_p50_delta:.gates.score_p50_delta,fail_rate_delta:.gates.fail_rate_delta,cost_delta:.gates.cost_delta,ab_exit_code:.gates.ab_exit_code}' "$summary_path")"
curated_sql="NULL"
if [[ -n "$curated_eval_id" ]]; then
  curated_sql="'$(esc "$curated_eval_id")'"
fi

sqlite3 "$db" "
INSERT INTO canary_runs(
  id,skill,eval_run_id,curated_eval_run_id,dset_path,dset_sha256,dset_count,candidate_ref,baseline_ref,gate_verdict_json,action,action_ref,caught_by_canary,backlog_path,summary_path,created_at
) VALUES (
  '$(esc "${eval_id}-canary")',
  '$(esc "$skill")',
  '$(esc "$eval_id")',
  ${curated_sql},
  '$(esc "${dset#$root/}")',
  '$(esc "$dset_sha")',
  ${dset_count},
  '$(esc "$candidate_ref")',
  '$(esc "$baseline_ref")',
  '$(esc "$summary_json")',
  '$(esc "$action")',
  '$(esc "$action_ref")',
  ${caught_by_canary},
  '$(esc "${backlog_path#$root/}")',
  '$(esc "${summary_path#$root/}")',
  '$(date -u +%Y-%m-%dT%H:%M:%SZ)'
);
"

cat "$summary_path"
if [[ "$action_rc" -ne 0 ]]; then
  echo "canary:run: auto action failed eval=$eval_id action=$action rc=$action_rc" >&2
  exit 1
fi
if [[ "$pass" -ne 1 ]]; then
  echo "canary:run: regression detected eval=$eval_id action=$action caught_by_canary=$caught_by_canary" >&2
  exit 1
fi

echo "canary:run: OK eval=$eval_id action=$action caught_by_canary=$caught_by_canary" >&2
