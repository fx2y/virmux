---
paths:
  - "cmd/**/*.go"
  - "internal/**/*.go"
---
# Go VM/Store/Trace Rules
- `cmd/*` is parse/DI/dispatch/print only; behavior belongs in `internal/*`.
- Behavior edits require success+failure tests; errors keep typed root + op context.
- Long paths require `context.Context`; no unbounded waits or goroutine leaks.

- VM truth is host-owned: require `vm.boot.started`,`vm.exec.injected`,`vm.exit.observed`; keep `vm.guest.ready` vs `vm.agent.ready` split.
- Serial `Linux+ok` markers are bridge-only; vsock lanes must be tty-agnostic.
- Resume contract: precedence `explicit > last_snapshot_id > latest.json`; attempt once; fault => `StopVMM+Wait` then cold fallback; terminal `vm:resume` always includes non-null `resume_mode,resume_source,resume_error`.

- Transport contract: shared vsock dialer only (`internal/transport/vsock`), no cmd-local dialers; CONNECT ack accept=`OK ` prefix; non-OK is non-retryable; retry budget only for early stream faults; READY parser strict `READY v0 tools=`.
- Terminal keys non-null/stable: `lost_*`,`guest_ready_ms`,`connect_attempts`,`handshake_ms`,`error_obj{code,msg,retryable}`,`resume_*`.
- Stable host error API: `TIMEOUT`,`DENIED`,`DISCONNECT`,`CRASH`,`INTERNAL`,`JUDGE_INVALID`.

- State/trace/db contract: SoT `agents/<id>.json`; mutable disk `volumes/<agent>.ext4`; rootfs RO; trace append-only and ordered `trace emit -> sqlite insert`; sqlite hard invariants `WAL+FK+required indexes`; checker paths are validator-only.
- Artifact/export contract: sqlite inventory is SoT (regular=content hash; non-regular=`meta:*`,`bytes=0`,`lstat`); export/import deterministic+safe (canonical order/mtime/uid/gid, manifest pre-verify, symlink/path escape deny).

- Skill hard rules in Go paths:
- `SKILL.md` SoT (`prompt.md` compat only); CLI surface fixed.
- Skill arg strict kebab token; escapes => `SKILL_PATH_ESCAPE`.
- Fingerprint hashes canonical rel-path+bytes of `SKILL.md|tools.yaml|rubric.yaml`.
- Fixture lookup deterministic `raw -> skill/raw -> skill/tests/raw`.
- Budget parser integer-only (`tool_calls`,`seconds`,`tokens`).
- `skill run` denies (`TOOL_DENIED`,`BUDGET_EXCEEDED`) fail closed and preserve required evidence.
- Judge is rule-first; started-emit failure forbids score/judge_run writes; malformed/unknown output/mode => typed `JUDGE_INVALID` before writes.
- Replay parity checks ordered tool input+output hashes + sqlite artifact parity; nondet only via declared fixture flag.
- AB pairwise evidence must join via `experiments.eval_run_id`; tie only dual hard-fail.
- Promote/rollback resolve refs (`git rev-parse`) before audit writes; `commit_sha` immutable SHA.
- Refine/suggest persisted refs must be run-relative/repo-relative; suggest branches re-anchor to captured base HEAD.

- Determinism guard: no test may combine `t.Parallel` + `os.Chdir`.
