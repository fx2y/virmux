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
# Failure -> Fix Playbook
- Stop-ship invariants:
1. `doctor` red.
2. smoke-bridge markers `Linux+ok` missing where bridge applies.
3. missing VM boundary events (`vm.boot.started`,`vm.exec.injected`,`vm.exit.observed`).
4. missing resume terminal keys (`resume_mode`,`resume_source`,`resume_error`).
5. evidence integrity red (`trace:validate`,`db:check`,cohort SQL cert).
6. cleanup red (orphan proc/stale sock/vsock/fifo/tap).
7. cached-only cert proof.
8. skill-lane run without required score/judge/eval/promotion/refine/suggest evidence rows for exercised commands.

- Triage order (never invert):
1. `runs/virmux.sqlite` (`runs`,`events`,`tool_calls`,`artifacts`,`scores`,`judge_runs`,`eval_runs`,`promotions`,`refine_runs`,`suggest_runs`).
2. `runs/<id>/meta.json` + `runs/<id>/trace.ndjson`.
3. run artifacts (`toolio/*.req|res.json`,`score.json`,`skill-run.json`,`serial.log`,`fc.log`,`fc.metrics.log`).
4. process stdout/stderr.

- Symptom -> likely breach -> first probe:
- `doctor` CPU/KVM fail -> host floor breach -> inspect `/sys/module/kvm`,`/dev/kvm`, group perms.
- lock/artifact mismatch -> image drift -> verify `vm/images.lock` bytes/checksums + cache sha dir.
- smoke-marker miss (bridge lanes) -> serial parser drift -> inspect parsed marker segment in `runs/<id>/serial.log`.
- vsock retry exhaustion -> transport/handshake drift -> inspect connect retry class + `handshake_ms` + chaos report.
- READY parse fail -> protocol drift/spoof -> verify strict `READY v0 tools=` parser.
- CONNECT `OK` then early EOF -> likely guest/agent defect -> classify disconnect-side unless host proof says otherwise.
- resume always cold-fallback -> snapshot load path broken -> verify SDK snapshot handler + lineage resolution.
- resume hard-fails (no cold fallback) -> policy breach -> enforce `StopVMM+Wait` fallback path.
- judge wrote score rows after start-emit failure -> evidence-order breach -> enforce fail-closed pre-insert.
- replay mismatch misses output drift -> replay contract breach -> verify both input+output hash parity and sqlite artifact inventory parity.
- AB drift with same fixture ids -> frozen-fixture breach -> ensure both refs use head payload set + missing base id hard fail.
- refine blocked by latest failed eval despite prior pass -> eval selection bug -> enforce latest-passing lookup.
- suggest candidates chain diffs -> git base-anchor breach -> re-anchor each branch to captured base HEAD.
- suggest/motif fragmentation across run ids -> path canon breach -> normalize run-scoped refs before schema hash.
- db check mutates rows -> validator mutation breach -> ensure non-zero report only; no UPDATE side effects.
- absolute host paths in refine/suggest artifacts -> portability breach -> enforce run-relative/repo-relative refs.

- Anti-patterns (hard no):
- unscoped SQL cert on append-only historical DB.
- counting fallback cold boots as snapshot perf success.
- accepting success without sqlite evidence rows/registered refs.
- filesystem archaeology before sqlite inventory.
- validator scripts that auto-rewrite evidence.
- docs/spec edits decoupled from behavior changes/guards.
