# Showcase 005: Spec-05 Live E2E Operator Drillbook

Goal: extract ship value from current impl fast, then prove every decision from artifacts+sqlite (not stdout).

Law: `if not queryable in runs/* + runs/virmux.sqlite + tmp/*cert*.json, it did not happen`.

## 0) Hard Contract (stop if violated)
- Host floor: Ubuntu 24.04 bare-metal; `/dev/kvm` rw; Firecracker via go-sdk.
- Skill canon: `skills/<name>/{SKILL.md,tools.yaml,rubric.yaml,tests/*}`; `SKILL.md` SoT.
- CLI canon: `virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>`.
- Translation doctrine: map ghostfleet terms into canon; never fork cmd namespace.
- Trace canon: `runs/<id>/trace.ndjson` append-only (`trace.jsonl` symlink compat only).
- Order canon: `trace emit -> sqlite insert` (never invert).
- DB canon: WAL+FK+required indexes; `db:check` validator-only.
- Pairwise canon: ties only on dual hard-fail; equal-pass score must still pick winner.
- Promote canon: passing+fresh AB required; stale/missing typed refusal.
- Rollback canon: resolvable current+target refs; immutable `commit_sha`; auditable row.
- Canary canon: summary + `canary_runs` row must persist even if auto-action fails.
- Release canon: `ship:skills` additive; `ship:core` decisive+isolated.

## 1) Mental Model (3 loops)
- `L0 Build truth`: `skill run -> judge -> replay`.
- `L1 Compare truth`: `skill ab --judge pairwise --anti-tie` + report.
- `L2 Ship truth`: `promote/rollback` tx + canary + `ship:skills` cert.

## 2) Bootstrap (2m)
```bash
set -euo pipefail
cd /home/haris/projects/virmux

ROOT="$(git rev-parse --show-toplevel)"
DB="$ROOT/runs/virmux.sqlite"
RUNS="$ROOT/runs"
SKILL="dd"

test -r /dev/kvm -a -w /dev/kvm
mise run doctor
go run ./cmd/virmux skill lint skills/$SKILL
```

Pass:
- host+kvm ok
- doctor green
- lint json prints skill name, no fatal

## 3) Golden Path (12-18m, live E2E)
```bash
set -euo pipefail
COHORT_C3="qa-skill-c3-$(date -u +%Y%m%dT%H%M%SZ)-$$"

RID_JSON="$(go run ./cmd/virmux skill run $SKILL --fixture case01.json --label po05-run --agent po05)"
RID="$(jq -r '.run_id' <<<"$RID_JSON")"
test -n "$RID" -a "$RID" != "null"

go run ./cmd/virmux skill judge "$RID"
go run ./cmd/virmux skill replay "$RID"

AB_JSON="$(go run ./cmd/virmux skill ab --judge pairwise --anti-tie --cohort "$COHORT_C3" $SKILL HEAD~1..HEAD)"
EID="$(jq -r '.id' <<<"$AB_JSON")"
XID="$(jq -r '.experiment.id // empty' <<<"$AB_JSON")"
go run ./cmd/virmux skill ab --report-only --eval-id "$EID" --fmt one-line
test -n "$XID" && go run ./cmd/virmux skill ab --report-only --eval-id "$XID" --fmt one-line || true

go run ./cmd/virmux skill promote --dry-run $SKILL "$EID"
go run ./cmd/virmux skill promote --rollback --to-ref HEAD~1 --dry-run $SKILL "$EID"
```

## 4) Proof Queries (required after golden path)
```bash
sqlite3 "$DB" "select id,task,label,status,agent_id from runs where id='$RID';"
sqlite3 "$DB" "select kind from events where run_id='$RID' order by id;"
sqlite3 "$DB" "select seq,tool,input_hash,output_hash,error_code from tool_calls where run_id='$RID' order by seq;"
sqlite3 "$DB" "select skill,score,pass,judge_cfg_hash,artifact_hash from scores where run_id='$RID' order by datetime(created_at) desc limit 1;"
sqlite3 "$DB" "select id,skill,cohort,pass,score_p50_delta,fail_rate_delta,cost_delta from eval_runs where id='$EID';"
sqlite3 "$DB" "select id,skill,op,from_ref,to_ref,eval_run_id,commit_sha,created_at from promotions order by datetime(created_at) desc limit 5;"
test -n "$XID" && sqlite3 "$DB" "select experiment_id,winner,count(*) from comparisons where experiment_id='$XID' group by experiment_id,winner order by winner;" || true
```

Must see:
- `run.finished` for `RID`.
- score row + judge row evidence.
- eval row exists with cohort.
- promotion dry-run prints JSON but does not mutate tag/db.

## 5) Canary Walkthroughs (real integration)
### 5.1 Safe/manual (`--no-auto-action`)
```bash
./scripts/canary_snapshot.sh --lookback-hours 24 --limit 200
./scripts/canary_run.sh \
  --skill $SKILL \
  --candidate-ref HEAD \
  --baseline-ref HEAD~1 \
  --cohort "qa-skill-c5-$(date -u +%Y%m%dT%H%M%SZ)-manual" \
  --no-auto-action || true
./scripts/canary_report.sh --skill $SKILL --fmt one-line --limit 10
```

### 5.2 Cert mode (deterministic pass+fail via local fake promptfoo)
```bash
./scripts/skill_canary_cert.sh
cat tmp/skill-canary-cert-summary.json
./scripts/canary_report.sh --fmt one-line --limit 5
```

Pass:
- summary has `pass_eval_id` + `fail_eval_id`
- report rows include action `promote|rollback`
- at least one `caught_by_canary=1` in fail path

## 6) SQL Board (operator single-pane)
```bash
sqlite3 "$DB" "select id,cohort,pass,score_p50_delta,fail_rate_delta,cost_delta,created_at from eval_runs order by datetime(created_at) desc limit 12;"
sqlite3 "$DB" "select id,skill,op,from_ref,to_ref,eval_run_id,reason,created_at from promotions order by datetime(created_at) desc limit 12;"
sqlite3 "$DB" "select id,skill,eval_run_id,action,caught_by_canary,summary_path,created_at from canary_runs order by datetime(created_at) desc limit 12;"
sqlite3 "$DB" "select id,skill,motif_key,branch,commit_sha,pr_body_path,created_at from suggest_runs order by datetime(created_at) desc limit 12;"
```

## 7) Portability E2E (eval bundle)
```bash
EID_LATEST="$(sqlite3 "$DB" "select id from eval_runs order by datetime(created_at) desc limit 1;")"
go run ./cmd/virmux export --mode eval --eval-id "$EID_LATEST" --db "$DB" --runs-dir "$RUNS" --out "$RUNS/$EID_LATEST.eval.tar.zst"
go run ./cmd/virmux import --bundle "$RUNS/$EID_LATEST.eval.tar.zst" --db "$RUNS/imported.sqlite" --runs-dir "$RUNS/imported"
sqlite3 "$RUNS/imported.sqlite" "select id,skill,cohort from eval_runs where id='$EID_LATEST';"
```

## 8) Cert/Release Flow (QA decisive)
```bash
mise run skill:test:core
mise run skill:test:c2
mise run skill:test:c3
mise run skill:test:c4
mise run skill:test:c5
mise run skill:test:c6
mise run skill:test:c7
mise run ship:skills
```

Freshness scoped cert:
```bash
CERT_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
./scripts/skill_sql_cert.sh --cert-ts "$CERT_TS" --require-canary
./scripts/spec05_dod_matrix.sh --cert-ts "$CERT_TS"
cat tmp/skill-sql-cert-summary.json
cat tmp/spec05-dod-matrix.json
cat tmp/spec05-residual-risk.md
```

Required artifacts:
- `tmp/ship-skills-summary.json`
- `tmp/skill-sql-cert-summary.json`
- `tmp/skill-canary-cert-summary.json`
- `tmp/spec05-dod-matrix.json`
- `tmp/spec05-residual-risk.md`
- `tmp/rollback-playbook-smoke.ok`
- `tmp/cleanup-audit.ok`

## 9) FDE Daily Loop (fast, high-signal)
```bash
RID_JSON="$(go run ./cmd/virmux skill run $SKILL --fixture case01.json --label fde05-run --agent fde05)"
RID="$(jq -r '.run_id' <<<"$RID_JSON")"
go run ./cmd/virmux skill judge "$RID"
go run ./cmd/virmux skill replay "$RID"
go run ./cmd/virmux skill ab --judge pairwise --anti-tie $SKILL HEAD~1..HEAD || true
```

Forensics pack:
```bash
cat "$RUNS/$RID/skill-run.json"
cat "$RUNS/$RID/score.json"
sqlite3 "$DB" "select kind,count(*) from events where run_id='$RID' group by kind order by kind;"
sqlite3 "$DB" "select seq,tool,input_hash,output_hash,error_code from tool_calls where run_id='$RID' order by seq;"
sqlite3 "$DB" "select path,sha256,bytes from artifacts where run_id='$RID' order by id;"
```

## 10) Typed Failure Drills (must stay explicit)
```bash
go run ./cmd/virmux skill run ../x --fixture case01.json || true
go run ./cmd/virmux skill ab --judge typo $SKILL HEAD~1..HEAD || true
go run ./cmd/virmux skill promote $SKILL missing-eval || true
go run ./cmd/virmux skill judge --mode typo "$RID" || true
go run ./cmd/virmux skill suggest --min-repeats 999 --db "$DB" --runs-dir "$RUNS" --skills-dir "$ROOT/skills" --repo-dir "$ROOT" || true
go run ./cmd/virmux skill refine --max-hunks 1 --db "$DB" --runs-dir "$RUNS" --skills-dir "$ROOT/skills" --repo-dir "$ROOT" "$RID" || true
```

Expect typed surfaces:
- `SKILL_PATH_ESCAPE`
- strict `--judge` mode error
- `MISSING_AB_VERDICT` / `STALE_AB_VERDICT`
- `JUDGE_INVALID` (malformed/unknown judge output path)
- `SUGGEST_NOT_TRIGGERED`
- `REFINE_PATCH_TOO_LARGE`

## 11) Scenario Bank (copy/paste matrix)
| ID | Scenario | Cmd | Pass Signal |
|---|---|---|---|
| S01 | host floor | `mise run doctor` | exit 0 |
| S02 | kvm gate | `test -r /dev/kvm -a -w /dev/kvm` | exit 0 |
| S03 | lint canon | `go run ./cmd/virmux skill lint skills/dd` | json row |
| S04 | vm run truth | `go run ./cmd/virmux skill run dd --fixture case01.json` | run id |
| S05 | judge rules | `go run ./cmd/virmux skill judge <rid>` | score json |
| S06 | replay self | `go run ./cmd/virmux skill replay <rid>` | `verified=true` |
| S07 | replay compare | `go run ./cmd/virmux skill replay --against <b> <a>` | mismatch or verified |
| S08 | pairwise ab | `go run ./cmd/virmux skill ab --judge pairwise --anti-tie dd HEAD~1..HEAD` | eval+experiment ids |
| S09 | eval report | `go run ./cmd/virmux skill ab --report-only --eval-id <eid> --fmt one-line` | one-line deltas |
| S10 | exp report | `go run ./cmd/virmux skill ab --report-only --eval-id <xid> --fmt one-line` | wr/wins/ties |
| S11 | promote dry-run | `go run ./cmd/virmux skill promote --dry-run dd <eid>` | json `op=promote` |
| S12 | rollback dry-run | `go run ./cmd/virmux skill promote --rollback --to-ref HEAD~1 --dry-run dd <eid>` | json `op=rollback` |
| S13 | canary snapshot | `./scripts/canary_snapshot.sh` | `dsets/prod_*.jsonl` |
| S14 | canary manual | `./scripts/canary_run.sh ... --no-auto-action` | summary json |
| S15 | canary cert | `./scripts/skill_canary_cert.sh` | pass+fail eval ids |
| S16 | canary report | `./scripts/canary_report.sh --fmt one-line` | action rows |
| S17 | sql cert | `./scripts/skill_sql_cert.sh --require-canary` | summary json |
| S18 | dod matrix | `./scripts/spec05_dod_matrix.sh --cert-ts <ts>` | all pass |
| S19 | ship skills | `mise run ship:skills` | OK cert_tag |
| S20 | trace validate | `mise run trace:validate` | green |
| S21 | db check | `mise run db:check` | green+nonmutating |
| S22 | eval export | `go run ./cmd/virmux export --mode eval --eval-id <eid> ...` | tar.zst |
| S23 | eval import | `go run ./cmd/virmux import --bundle <tar> ...` | imported row |
| S24 | refine | `go run ./cmd/virmux skill refine <rid>` | refine row+artifacts |
| S25 | suggest | `go run ./cmd/virmux skill suggest ...` | branch/commit or typed no-trigger |
| S26 | cleanup audit | `./scripts/cleanup_audit.sh` | no leak markers |
| S27 | rollback smoke | `./scripts/rollback_playbook_smoke.sh` | `.ok` marker |
| S28 | core isolation | `awk ... mise.toml | rg 'skill:|ship:skills' && exit 1 || true` | no match |
| S29 | docs drift | `./scripts/skill_docs_drift.sh` | exit 0 |
| S30 | c7 guard set | `mise run skill:test:c7` | green |

## 12) Anti-Patterns (banlist)
- cert from stale rows only
- stdout-only success claims
- ghostfleet command literals in runbooks
- db checker schema repair
- tie acceptance on equal-pass pairwise rows
- canary fail without persisted `canary_runs` row
- skill-lane coupling into `ship:core`

## 13) Minimal Operator Ritual (daily)
```bash
mise run doctor
go run ./cmd/virmux skill lint skills/dd
go run ./cmd/virmux skill run dd --fixture case01.json
go run ./cmd/virmux skill judge <rid>
go run ./cmd/virmux skill replay <rid>
go run ./cmd/virmux skill ab --judge pairwise --anti-tie dd HEAD~1..HEAD || true
./scripts/canary_report.sh --fmt one-line --limit 5
```

This is the shortest loop that still compounds real evidence.
