---
paths:
  - "cmd/**/*.go"
  - "internal/**/*.go"
---
# Go Runtime Rules
- CLI stays thin: flags+DI in `cmd/*`; behavior in `internal/*`.
- Behavior changes require `*_test.go` coverage for success+failure paths.
- Errors wrap operation context (`op: %w`) and preserve typed root cause.
- All I/O/long paths take `context.Context`; no unbounded wait, no goroutine leak.

- VM lifecycle is host-driven/deterministic; never trust guest self-poweroff as sole completion.
- Serial exec contract uses parsed marker lines (`__virmux_exec_start__`,`__virmux_exec_rc__`); marker checks only inside parsed command-output segment.
- Smoke bridge still requires `Linux` + `ok` markers until explicitly retired.
- Mandatory boundary events on VM lanes: `vm.boot.started`,`vm.exec.injected`,`vm.exit.observed`.

- Vsock contract:
- Dial via shared transport (`internal/transport/vsock`), not command-local dialers.
- Handshake accept condition is `OK ` prefix; non-OK ack is non-retryable `ErrConnectAck`.
- Retry budget applies only to early stream faults; terminal retry exhaustion must surface typed error.
- READY parser is strict (`READY v0 tools=`) and capability list is parsed, not free-text trusted.

- `run.finished` payload contract (non-null/stable where applicable): `lost_logs`,`lost_metrics`,`guest_ready_ms`,`connect_attempts`,`handshake_ms`,`error_obj{code,msg,retryable}`.
- Error classification must map to stable host-visible classes (`TIMEOUT`,`DENIED`,`DISCONNECT`,`CRASH`,`INTERNAL`) instead of ad-hoc strings.

- Resume contract:
- Resolve precedence: explicit mem/state > `agent.last_snapshot_id` > `latest.json`.
- Snapshot restore must use SDK snapshot handler path (`WithSnapshot(...)`), not config-only stubs.
- Attempt snapshot once; any resolve/load/wait fault => `StopVMM+Wait` then cold fallback.
- `vm:resume` terminal event must include non-null `resume_mode`,`resume_source`,`resume_error`.
- Resume wait handling must remain seam-injected/testable (start/stop/wait/sleep/process probes), not hardwired runtime calls.

- Agent/state contract:
- SoT is `agents/<id>.json` via `internal/agent`.
- Persistent RW disk is `volumes/<agent>.ext4`; rootfs remains RO (`rootflags=noload`).

- Store/trace contract:
- `runs` identity fields evolve additively.
- `artifacts` is mandatory evidence inventory for run outputs.
- Artifact typing uses `lstat`; regular files hash content, non-regular types persist `meta:*` rows with `bytes=0`.
- SQLite invariants are hard (`WAL`,`FK`,`required indexes`); keep `db:check` green.
- Trace is append-only (`O_APPEND|O_CREATE|O_WRONLY`); no truncate-on-reopen.
- Per-event ordering is fixed: `trace emit -> sqlite insert`.

- Export/import contract:
- Export bundles are deterministic (sorted names, epoch mtime, uid/gid=0).
- Import verifies manifest before insert and denies path/symlink escapes.
