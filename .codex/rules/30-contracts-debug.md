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
- Stop-ship on: `doctor` red; VM boundaries missing; resume terminal keys missing; `trace:validate`/`db:check`/fresh cert red; cleanup red; cached-only cert claim; exercised command missing required sqlite/artifact evidence.

- Triage order (never invert):
1. sqlite SoT (`runs`,`events`,`tool_calls`,`artifacts`, scoring/eval/promo/canary/refine/suggest/research tables as relevant)
2. `runs/<id>/trace.ndjson` (+ `meta.json` if present)
3. run artifacts (tool I/O, skill outputs, reduce/report outputs, serial/fc logs)
4. stdout/stderr

- Symptom -> likely breach -> first probe
- retry exhaustion -> transport drift -> retry class + `handshake_ms` + chaos logs
- READY parse fail -> protocol drift/spoof -> strict `READY v0 tools=` parse
- CONNECT `OK ` then EOF -> guest/agent defect likely -> classify disconnect unless host proof says otherwise
- resume always cold/hard-fail -> snapshot policy breach -> precedence + SDK path + enforced `StopVMM+Wait`
- judge writes after started-emit failure / accepts bad mode -> fail-closed breach -> pre-write `JUDGE_INVALID`
- replay misses divergence -> parity breach -> ordered tool hashes + sqlite artifact parity
- AB tie on both-pass equal score -> anti-tie breach -> deterministic non-tie winner
- export/import drops pairwise rows -> eval-link breach -> `experiments.eval_run_id`
- rollback row empty/ambiguous refs -> provenance breach -> resolvable refs + immutable `commit_sha`
- canary auto-action fail drops row/summary -> evidence-on-fail breach -> persist before exit
- dataset lint misses bad early row -> stream-validation bug -> full-stream validation
- checker repairs DB/evidence -> validator breach -> report-only checker
- refine/suggest writes absolute paths -> portability breach -> run-/repo-relative only

- Research lane quick triage (wrapper/target split):
- command exits but target artifacts unchanged -> you inspected wrapper run; probe target `--run` dir + target sqlite artifact rows
- replay "success" but no report change -> check target `mismatch.json` first, then rerun target `reduce`
- timeline looks empty -> wrong run ID (use wrapper run) or wrong trace (`trace.ndjson`)
- replay subset hangs -> zero-progress subset deadlock regression; run scheduler guard test
- replay selector weirdly accepted -> selector preflight regression (`RERUN_SELECTOR_INVALID`)
- map/replay accepts tampered plan unknown key -> strict parse bypass regression

- Hard anti-patterns:
- stale/unscoped certs
- pass claims without sqlite proof
- FS archaeology before sqlite inventory
- validator mutation scripts
- wrapper/target run ID confusion in probes/docs/tests
- behavior diffs without same-diff guard + learning capture
