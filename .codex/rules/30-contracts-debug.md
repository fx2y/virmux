---
paths:
  - "cmd/**/*.go"
  - "scripts/doctor.sh"
  - "scripts/vm_*.sh"
  - "scripts/skill_*.sh"
  - "scripts/bench_snapshot.sh"
  - "scripts/trace_validate.sh"
  - "scripts/db_check.sh"
  - "scripts/slack_*.sh"
  - "scripts/pw_*.sh"
  - "internal/vm/*.go"
  - "internal/store/*.go"
  - "internal/trace/*.go"
  - "internal/slack/*.go"
  - "internal/skill/*.go"
  - "internal/vm/**/*.go"
  - "internal/store/**/*.go"
  - "internal/trace/**/*.go"
  - "internal/slack/**/*.go"
  - "internal/skill/**/*.go"
---
# Contract Debug Playbook
- Stop-ship on: `doctor` red; missing VM boundaries; missing resume terminal keys; `trace:validate`/`db:check`/cohort cert red; cleanup red; cached-only cert claim; exercised skill command missing required rows/artifacts.

- Triage order (never invert):
1. sqlite SoT (`runs`,`events`,`tool_calls`,`artifacts`,`scores`,`judge_runs`,`eval_runs`,`experiments`,`comparisons`,`promotions`,`canary_runs`,`refine_runs`,`suggest_runs`).
2. `runs/<id>/meta.json` + `runs/<id>/trace.ndjson`.
3. run artifacts (`toolio/*.req|res.json`,`skill-run.json`,`score.json`,`ab-verdict.json`,`canary-summary.json`,`serial.log`,`fc.log`,`fc.metrics.log`).
4. stdout/stderr.

- Symptom -> breach -> probe:
- retry exhaustion -> transport drift -> retry class + `handshake_ms` + chaos logs.
- READY parse fail -> protocol drift/spoof -> strict `READY v0 tools=` parser.
- CONNECT OK then EOF -> guest/agent defect likely -> disconnect-side unless host proof says otherwise.
- resume always cold / resume hard-fail -> snapshot policy breach -> precedence + SDK path + enforced `StopVMM+Wait`.
- judge writes after started-emit failure / accepts malformed mode -> fail-closed parser breach -> enforce pre-write `JUDGE_INVALID` path.
- replay misses divergence -> parity breach -> ordered tool in/out hashes + sqlite artifact parity.
- AB tie on both-pass equal score -> anti-tie breach -> deterministic non-tie winner.
- pairwise rows dropped on export/import -> eval-link breach -> join on `experiments.eval_run_id`.
- rollback row has empty/ambiguous refs -> provenance breach -> resolvable refs + immutable `commit_sha`.
- canary action failure drops row -> evidence-on-fail breach -> persist summary+canary row before exit.
- dset lint misses early bad row -> stream-validation bug -> full-stream `jq -s all`.
- db checker mutates schema/evidence -> validator breach -> report-only checker.
- refine/suggest emits absolute host paths -> portability breach -> run-relative/repo-relative refs only.

- Hard anti-patterns: stale/unscoped certs; pass claims without sqlite evidence; filesystem archaeology before sqlite inventory; validator mutation scripts; behavior diffs without same-diff docs/rules updates.
