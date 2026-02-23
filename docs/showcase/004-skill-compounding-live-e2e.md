# Showcase 004: Skill Compounding Live E2E (Spec-04)

Goal: extract production value from current `virmux skill` lane (`lint->run->judge->replay->ab->promote->refine->suggest`) with evidence-first proof.

Rule-0: never certify from stdout. Certify from `runs/virmux.sqlite` + `runs/<id>/trace.ndjson` + `artifacts/tool_calls/scores/eval/promotions/refine/suggest` rows.

## 0) Hard Contract (non-negotiable)
- Host floor: Ubuntu 24.04 bare-metal, `/dev/kvm` rw, Firecracker via go-sdk.
- Canon skill artifact: `skills/<name>/{SKILL.md,tools.yaml,rubric.yaml,tests/*}`. `prompt.md` compat only.
- Canon CLI: `virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>`.
- Skill arg safety: kebab token only; escapes => `SKILL_PATH_ESCAPE`.
- `skill run`: vm+vsock evidence lane; tool allowlist+budget fail-closed.
- Evidence order invariant: trace emit -> sqlite insert.
- Trace canon: `runs/<id>/trace.ndjson`; `trace.jsonl` symlink compat only.
- DB posture: WAL+FK+required indexes; append-only validation (`db:check` validator-only).
- `ship:skills` is optional lane and must stay isolated from `ship:core`.

## 1) Bootstrap (2m)
```bash
set -euo pipefail
cd /home/haris/projects/virmux

test -r /dev/kvm -a -w /dev/kvm
mise run doctor
go run ./cmd/virmux skill lint --json skills/dd
```

Pass:
- `doctor` green.
- lint returns `dd`, `dormant=false`.

## 2) Tacit CLI Rules (save hours)
- `skill run` safest form: `go run ./cmd/virmux skill run <skill> --fixture <path> [flags...]`.
- If skill is not first positional, Go `flag` parsing can stop early; keep flags before trailing positionals.
- `skill replay --against` form is strict: `go run ./cmd/virmux skill replay --against <runB> <runA>`.
- `skill run` prints JSON on stdout, FC logs on stderr; parse stdout JSON only.

## 3) PO loop (10-15m): run -> judge -> replay(self)
```bash
set -euo pipefail
RID_JSON="$(go run ./cmd/virmux skill run dd --fixture case01.json --label po04-run --agent po04)"
RID="$(jq -r '.run_id' <<<"$RID_JSON")"
test -n "$RID" -a "$RID" != "null"

go run ./cmd/virmux skill judge "$RID"
go run ./cmd/virmux skill replay "$RID"
```

Proof pack:
```bash
sqlite3 runs/virmux.sqlite "select id,task,label,status,agent_id from runs where id='$RID';"
sqlite3 runs/virmux.sqlite "select kind from events where run_id='$RID' order by id;"
sqlite3 runs/virmux.sqlite "select seq,tool,input_hash,output_hash,error_code from tool_calls where run_id='$RID' order by seq;"
sqlite3 runs/virmux.sqlite "select skill,score,pass,judge_cfg_hash,artifact_hash from scores where run_id='$RID' order by id desc limit 1;"
sqlite3 runs/virmux.sqlite "select path,sha256,bytes from artifacts where run_id='$RID' order by id;"
```

Must see:
- `run.started`, `skill.run.selected`, `vm.boot.started`, `vm.agent.ready`, `vm.tool.result`, `run.finished`, `skill.judge.*`.
- `tool_calls` has non-empty `input_hash`,`output_hash`.
- `scores` row exists.

## 4) Live AB+promotion integration (15m)
Fast path (fake promptfoo provider, no external API creds):
```bash
./scripts/skill_ab.sh
cat tmp/skill-ab-summary.json
./scripts/skill_sql_cert.sh
```

Manual path:
```bash
PF=tmp/fake_promptfoo.sh
COHORT="qa-skill-c3-$(date -u +%Y%m%dT%H%M%SZ)-$$"
PASS_JSON="$(PF_MODE=pass go run ./cmd/virmux skill ab --db runs/virmux.sqlite --runs-dir runs --repo-dir . --skills-dir skills --promptfoo-bin "$PF" --cohort "$COHORT" dd HEAD~0..HEAD)"
EID="$(jq -r '.id' <<<"$PASS_JSON")"
go run ./cmd/virmux skill promote --db runs/virmux.sqlite --repo-dir . --tag skill/dd/manual dd "$EID"
```

AB SQL board:
```bash
sqlite3 runs/virmux.sqlite "select id,cohort,pass,score_p50_delta,fail_rate_delta,cost_delta,created_at from eval_runs order by created_at desc limit 8;"
sqlite3 runs/virmux.sqlite "select id,skill,tag,eval_run_id,created_at from promotions order by created_at desc limit 8;"
```

## 5) Refine walkthrough (operator mode, git-aware)
Preconditions:
- scored `skill:run` RID exists.
- target files clean (`skills/<skill>/SKILL.md`, `rubric.yaml`, optionally `tools.yaml`).
- run on disposable branch, not `main`.

```bash
RID="$(sqlite3 runs/virmux.sqlite "select run_id from scores where skill='dd' order by created_at desc limit 1;")"
go run ./cmd/virmux skill refine --db runs/virmux.sqlite --runs-dir runs --skills-dir skills --repo-dir . "$RID"
```

Check:
```bash
sqlite3 runs/virmux.sqlite "select id,run_id,skill,branch,commit_sha,patch_hash,hunk_count,tools_edit from refine_runs where run_id='$RID' order by created_at desc limit 1;"
sqlite3 runs/virmux.sqlite "select path,sha256,bytes from artifacts where run_id='$RID' and path like '%refine%' order by id;"
```

Hard guards:
- default deny `tools.yaml` edits (needs `--allow-tools-edit`).
- oversized patch => `REFINE_PATCH_TOO_LARGE`.
- dirty target files => hard fail pre-branch.

## 6) Suggest walkthrough (motif miner)
Use scratch branch; command can create branch+commit+new `skills/suggest-*`.

```bash
go run ./cmd/virmux skill suggest --db runs/virmux.sqlite --runs-dir runs --skills-dir skills --repo-dir . --min-repeats 3 --min-pass-rate 0.66 --min-score-p50 0.8 --max-candidates 1
```

Check:
```bash
sqlite3 runs/virmux.sqlite "select id,skill,motif_key,branch,commit_sha,pr_body_path,created_at from suggest_runs order by created_at desc limit 5;"
```

No trigger path:
```bash
go run ./cmd/virmux skill suggest --db runs/virmux.sqlite --runs-dir runs --skills-dir skills --repo-dir . --min-repeats 999
# exits with SUGGEST_NOT_TRIGGERED
```

## 7) Replay semantics (use correctly)
- Stable today: `go run ./cmd/virmux skill replay <run-id>` (self-parity).
- Cross-run: `go run ./cmd/virmux skill replay --against <runB> <runA>` compares ordered tool input+output hashes; mismatch => `REPLAY_MISMATCH`.
- Nondet exemption is data-declared only (`fixture deterministic:false`) and returns `exempt_reason=NONDET_FIXTURE`.

Nondet demo:
```bash
cat > tmp/nondet.json <<'JSON'
{"id":"nondet","tool":"shell.exec","args":{"cmd":"date +%s"},"deterministic":false}
JSON
R1="$(go run ./cmd/virmux skill run dd --fixture tmp/nondet.json --label po04-nd1 --agent po04 | jq -r '.run_id')"
R2="$(go run ./cmd/virmux skill run dd --fixture tmp/nondet.json --label po04-nd2 --agent po04 | jq -r '.run_id')"
go run ./cmd/virmux skill replay --against "$R2" "$R1"
```

## 8) Bundle roundtrip (deterministic portability)
```bash
RID="$(sqlite3 runs/virmux.sqlite "select id from runs where task='skill:run' and status='ok' order by started_at desc limit 1;")"
go run ./cmd/virmux export --db runs/virmux.sqlite --runs-dir runs --run-id "$RID" --out "runs/$RID.tar.zst"
go run ./cmd/virmux import --db runs/imported.sqlite --runs-dir runs/imported --bundle "runs/$RID.tar.zst"
sqlite3 runs/imported.sqlite "select id,task,status,source_bundle from runs where id='$RID';"
```

## 9) QA certification lane (decisive for skills)
One-shot:
```bash
mise run ship:skills
cat tmp/ship-skills-summary.json
cat tmp/skill-sql-cert-summary.json
```

Decompose when red:
```bash
mise run skill:lint
mise run skill:test:core
mise run skill:test:c2
mise run skill:test:c3
mise run skill:test:c4
mise run skill:test:c5
mise run skill:test:c6
mise run skill:sql-cert
```

Isolation proof:
```bash
awk '/^\[tasks\."ship:core"\]/{f=1} /^\[tasks\./&&f&&$0!~/ship:core/{exit} f{print}' mise.toml | rg -n 'skill:|ship:skills' && exit 1 || true
```

## 10) Typed-failure drills (must be explicit)
| Code | Drill |
|---|---|
| `SKILL_PATH_ESCAPE` | `go run ./cmd/virmux skill run ../escape --fixture case01.json` |
| `TOOL_DENIED` | run fixture tool not in allowlist (`http.fetch` on `dd`) |
| `BUDGET_EXCEEDED` | skill budget `tool_calls:0`, then run once |
| `REPLAY_MISMATCH` | compare 2 deterministic runs: `go run ./cmd/virmux skill replay --against <B> <A>` |
| `NONDET_FIXTURE` | compare 2 runs from `deterministic:false` fixture |
| `AB_REGRESSION` | `PF_MODE=fail go run ... skill ab ...` |
| `MISSING_AB_VERDICT` | `go run ./cmd/virmux skill promote dd missing-eval-id` |
| `STALE_AB_VERDICT` | run promote against db copy with backdated `eval_runs.created_at` |
| `REFINE_PATCH_TOO_LARGE` | `go run ./cmd/virmux skill refine --max-hunks 1 <rid>` on multi-hunk suggestion |
| `SUGGEST_NOT_TRIGGERED` | `go run ./cmd/virmux skill suggest --min-repeats 999 ...` |

## 11) Scenario bank (dense copy/paste)
| ID | Scenario | Command | Pass |
|---|---|---|---|
| S01 | host floor | `mise run doctor` | green |
| S02 | kvm gate | `test -r /dev/kvm -a -w /dev/kvm` | exit 0 |
| S03 | lint canon | `go run ./cmd/virmux skill lint --json skills/dd` | `dormant=false` |
| S04 | run happy | `go run ./cmd/virmux skill run dd --fixture case01.json` | JSON `status=ok` |
| S05 | judge happy | `go run ./cmd/virmux skill judge <rid>` | JSON `score/pass` |
| S06 | replay self | `go run ./cmd/virmux skill replay <rid>` | `verified=true` |
| S07 | replay cross mismatch | `go run ./cmd/virmux skill replay --against <rid2> <rid1>` | typed `REPLAY_MISMATCH` on drift |
| S08 | replay nondet exempt | nondet fixture + against | `exempt_reason=NONDET_FIXTURE` |
| S09 | tool deny | disallowed fixture tool | `TOOL_DENIED` |
| S10 | budget deny | budget0 skill | `BUDGET_EXCEEDED` + failed run evidence |
| S11 | arg escape(run) | `go run ./cmd/virmux skill run ../x --fixture case01.json` | `SKILL_PATH_ESCAPE` |
| S12 | arg escape(ab) | `go run ./cmd/virmux skill ab ../x HEAD~0..HEAD` | `SKILL_PATH_ESCAPE` |
| S13 | AB pass/fail pair | `./scripts/skill_ab.sh` | pass id + fail row |
| S14 | SQL cert cohort | `./scripts/skill_sql_cert.sh` | totals/pass/fail/promo thresholds |
| S15 | promote pass | `go run ./cmd/virmux skill promote dd <pass_eval_id>` | tag + promotion row |
| S16 | promote missing | promote unknown eval | `MISSING_AB_VERDICT` |
| S17 | promote stale | backdated db copy + promote | `STALE_AB_VERDICT` |
| S18 | refine run | `go run ./cmd/virmux skill refine <rid>` | `refine_runs` row + artifacts |
| S19 | refine too big | `go run ./cmd/virmux skill refine --max-hunks 1 <rid>` | `REFINE_PATCH_TOO_LARGE` |
| S20 | suggest trigger | `go run ./cmd/virmux skill suggest --min-repeats 3 ...` | branch+commit+suggest row |
| S21 | suggest no trigger | `go run ./cmd/virmux skill suggest --min-repeats 999 ...` | `SUGGEST_NOT_TRIGGERED` |
| S22 | suggest path hygiene | query `suggest_runs.pr_body_path` | run-relative path |
| S23 | refine path hygiene | inspect `runs/<rid>/refine-pr.md` | no absolute host paths |
| S24 | latest score per run | query motif source | deduped score rows |
| S25 | canonical skill sha | same bytes diff dirs hash check | equal sha |
| S26 | trace validator | `mise run trace:validate` | green |
| S27 | db validator | `mise run db:check` | green, no rewrites |
| S28 | skill eval validate | `mise run skill:eval` | promptfoo config validates |
| S29 | c1 guards | `mise run skill:test:core` | green |
| S30 | c2 guards | `mise run skill:test:c2` | green |
| S31 | c3 guards | `mise run skill:test:c3` | green |
| S32 | c4 guards | `mise run skill:test:c4` | green |
| S33 | c5 guards | `mise run skill:test:c5` | green |
| S34 | c6 guards | `mise run skill:test:c6` | green |
| S35 | full skill oracle | `mise run ship:skills` | `ship:skills: OK cert_tag=...` |
| S36 | core isolation | awk/rg one-liner on `ship:core` block | no skill refs |
| S37 | cleanup audit | `./scripts/cleanup_audit.sh` | zero FC/socket/fifo/tap leaks |
| S38 | run bundle export | `go run ./cmd/virmux export --run-id <rid> --out runs/<rid>.tar.zst` | deterministic bundle created |
| S39 | run bundle import | `go run ./cmd/virmux import --bundle runs/<rid>.tar.zst ...` | row imported with `source_bundle` |
| S40 | forensic board | SQL pack (runs/events/tool_calls/artifacts/scores/evals/promotions/refine/suggest) | every stage queryable |

## 12) Stop-ship conditions
- `ship:skills` not green.
- no fresh pass+fail AB pair in cert window.
- missing evidence rows after command claims success.
- untyped/ambiguous failure where typed code contract exists.
- cleanup audit leak.
- `ship:core` coupled to `skill:*` lane.
