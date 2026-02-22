---
paths:
  - "cmd/**/*.go"
  - "internal/**/*.go"
---
# Go Runtime Rules
- CLI stays thin: parse flags + wire deps; business logic belongs in `internal/*`.
- Public behavior changes require tests in same package (`*_test.go`) covering success + failure path.
- Errors wrap operation context (`op: %w`); never drop root cause.
- `context.Context` is mandatory for I/O or long-running paths; no background goroutine leaks.
- Keep VM automation host-driven and deterministic; never rely on guest self-shutdown as sole completion signal.
- Preserve smoke markers (`Linux`,`ok`) and resume marker (`resumed_ok`) unless contract/test updates are included.
- `vm.Resume` must preserve snapshot-first semantics + cold-boot fallback telemetry (`resume_mode`,`resume_error`).
- Store invariants are non-negotiable: WAL, FK, required indexes; schema changes require `db:check` updates.
- Trace invariants are non-negotiable: each line has `ts,run_id,task,event,payload(object)`.
