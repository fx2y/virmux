#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
db="$root/runs/virmux.sqlite"

if [[ ! -f "$db" ]]; then
  echo "db:check: missing db: $db" >&2
  exit 1
fi

# Bridge legacy local DBs to current schema before invariant checks.
sqlite3 "$db" <<'SQL' >/dev/null
CREATE TABLE IF NOT EXISTS tool_calls (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  req_id INTEGER NOT NULL DEFAULT 0,
  tool TEXT NOT NULL,
  input_hash TEXT NOT NULL,
  output_hash TEXT NOT NULL,
  input_ref TEXT NOT NULL DEFAULT '',
  output_ref TEXT NOT NULL DEFAULT '',
  stdout_ref TEXT NOT NULL DEFAULT '',
  stderr_ref TEXT NOT NULL DEFAULT '',
  rc INTEGER NOT NULL DEFAULT 0,
  dur_ms INTEGER NOT NULL DEFAULT 0,
  bytes_in INTEGER NOT NULL DEFAULT 0,
  bytes_out INTEGER NOT NULL DEFAULT 0,
  error_code TEXT NOT NULL DEFAULT '',
  FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_tool_calls_run_seq ON tool_calls(run_id,seq);
CREATE INDEX IF NOT EXISTS idx_tool_calls_tool_input_hash ON tool_calls(tool,input_hash);
CREATE TABLE IF NOT EXISTS suggest_runs (
  id TEXT PRIMARY KEY,
  skill TEXT NOT NULL,
  motif_key TEXT NOT NULL,
  branch TEXT NOT NULL,
  commit_sha TEXT NOT NULL,
  pr_body_hash TEXT NOT NULL,
  pr_body_path TEXT NOT NULL DEFAULT '',
  run_ids_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_suggest_runs_skill_created ON suggest_runs(skill,created_at);
SQL

journal_mode="$(sqlite3 "$db" 'PRAGMA journal_mode;' | tr -d '[:space:]' | tr '[:upper:]' '[:lower:]')"
if [[ "$journal_mode" != "wal" ]]; then
  echo "db:check: WAL required, got $journal_mode" >&2
  exit 1
fi

fk_errs="$(sqlite3 "$db" 'PRAGMA foreign_key_check;' | wc -l)"
if [[ "$fk_errs" -ne 0 ]]; then
  echo "db:check: FK violations detected" >&2
  sqlite3 "$db" 'PRAGMA foreign_key_check;'
  exit 1
fi

required_indexes=(idx_events_run_id idx_runs_started_at idx_artifacts_run_id idx_tool_calls_run_seq idx_tool_calls_tool_input_hash)
required_indexes+=(idx_scores_run_created idx_scores_skill_pass idx_judge_runs_run_created)
required_indexes+=(idx_eval_runs_skill_created idx_eval_runs_cohort_created idx_eval_cases_run_fixture idx_promotions_skill_created)
required_indexes+=(idx_refine_runs_run_created idx_refine_runs_skill_created)
required_indexes+=(idx_suggest_runs_skill_created)
for idx in "${required_indexes[@]}"; do
  c="$(sqlite3 "$db" "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='$idx';")"
  if [[ "$c" != "1" ]]; then
    echo "db:check: missing index: $idx" >&2
    exit 1
  fi
done

tool_calls_table="$(sqlite3 "$db" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='tool_calls';")"
[[ "$tool_calls_table" == "1" ]] || { echo "db:check: missing table: tool_calls" >&2; exit 1; }
scores_table="$(sqlite3 "$db" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='scores';")"
[[ "$scores_table" == "1" ]] || { echo "db:check: missing table: scores" >&2; exit 1; }
judge_runs_table="$(sqlite3 "$db" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='judge_runs';")"
[[ "$judge_runs_table" == "1" ]] || { echo "db:check: missing table: judge_runs" >&2; exit 1; }
eval_runs_table="$(sqlite3 "$db" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='eval_runs';")"
[[ "$eval_runs_table" == "1" ]] || { echo "db:check: missing table: eval_runs" >&2; exit 1; }
eval_cases_table="$(sqlite3 "$db" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='eval_cases';")"
[[ "$eval_cases_table" == "1" ]] || { echo "db:check: missing table: eval_cases" >&2; exit 1; }
promotions_table="$(sqlite3 "$db" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='promotions';")"
[[ "$promotions_table" == "1" ]] || { echo "db:check: missing table: promotions" >&2; exit 1; }
refine_runs_table="$(sqlite3 "$db" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='refine_runs';")"
[[ "$refine_runs_table" == "1" ]] || { echo "db:check: missing table: refine_runs" >&2; exit 1; }
suggest_runs_table="$(sqlite3 "$db" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='suggest_runs';")"
[[ "$suggest_runs_table" == "1" ]] || { echo "db:check: missing table: suggest_runs" >&2; exit 1; }

tool_rows="$(sqlite3 "$db" "SELECT COUNT(*) FROM tool_calls;")"
if [[ "$tool_rows" != "0" ]]; then
  mismatches=0
  while IFS='|' read -r id run_id input_ref input_hash output_ref output_hash; do
    [[ -n "$run_id" ]] || continue
    if [[ -n "$input_ref" ]]; then
      p="$root/runs/$run_id/$input_ref"
      [[ -f "$p" ]] || { echo "db:check: missing tool input artifact $p" >&2; exit 1; }
      got="sha256:$(sha256sum "$p" | awk '{print $1}')"
      if [[ "$got" != "$input_hash" ]]; then
        echo "db:check: tool_calls id=$id input_hash mismatch expected=$input_hash got=$got" >&2
        mismatches=$((mismatches+1))
      fi
    fi
    if [[ -n "$output_ref" ]]; then
      p="$root/runs/$run_id/$output_ref"
      [[ -f "$p" ]] || { echo "db:check: missing tool output artifact $p" >&2; exit 1; }
      got="sha256:$(sha256sum "$p" | awk '{print $1}')"
      if [[ "$got" != "$output_hash" ]]; then
        echo "db:check: tool_calls id=$id output_hash mismatch expected=$output_hash got=$got" >&2
        mismatches=$((mismatches+1))
      fi
    fi
  done < <(sqlite3 -separator '|' "$db" "SELECT id,run_id,input_ref,input_hash,output_ref,output_hash FROM tool_calls ORDER BY id;")
  if [[ "$mismatches" -ne 0 ]]; then
    echo "db:check: detected $mismatches tool hash mismatch(es); run explicit backfill if legacy repair is required" >&2
    exit 1
  fi
fi

echo "db:check: OK"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/runs/.db-check.ok"
