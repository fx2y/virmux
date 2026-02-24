---
paths:
  - "cmd/**/*.go"
  - "internal/**/*.go"
---
# Go VM/Store/Trace Rules
- `cmd/*`: parse/DI/dispatch/print only. No orchestration, no contract branching, no side-effect sequencing.
- `internal/*`: own behavior, typed failures, invariants, and boundary event order.
- Behavior edits ship success+failure guards. Errors keep typed root + op context.
- Long/IO paths require `context.Context`; no unbounded waits, leaked goroutines, or silent retries.

- VM/transport contracts are fixed (see `AGENTS.md`): host-owned truth, mandatory VM boundaries, guest/agent ready split, shared vsock dialer, strict CONNECT/READY handshake, watchdog-bounded waits, stable terminal keys/codes.
- State/trace/db/artifact/export contracts are fixed (see `AGENTS.md`): trace append-only + `emit->insert`, sqlite invariants hard, artifact inventory SoT, deterministic export/import.

- Code style (opinionated):
- Parsers/validators are separate from mutators.
- Side effects are sequenced explicitly; preflight selectors/inputs before emit/write/mutate.
- Trace events use stable namespaces; terminal events include stable key set (no nil/shape drift).
- Machine JSON on stdout only; progress logs stderr.

- Skill-path specifics:
- Keep skill parser/fingerprint/fixture/budget contracts exact.
- `skill run` deny paths persist evidence.
- Judge started-emit failure forbids score/judge writes.
- Replay parity checks ordered tool hashes + sqlite artifact parity.
- AB/promo/refine/suggest provenance rules are API; add-only.

- Research-path specifics (`internal/skill/research*`, `cmd/virmux/research.go`):
- Seams first (`Planner/Scheduler/Mapper/Reducer/Replay`); no cmd-owned pipeline wiring creep.
- Plan load paths use strict parse+Validate only; selector preflight before emit/mutation.
- `research.plan.created` precedes any map worker side effect.
- Scheduler subset runs fail closed (`BLOCKED`) on unsatisfied deps; zero-progress pending set must not deadlock.
- Worker infra acquire/cancel/errgroup errors propagate + terminalize affected tracks.
- Deterministic-default map rows must not embed wall-clock payloads.
- Replay parity must cover artifacts (map+reduce), not rows-only; nondet bypass only explicit `deterministic:false`.
- Wrapper vs target run IDs must stay explicit in variable names/logging/tests.

- Test hygiene: no `t.Parallel` with global state mutation (`os.Chdir`, stdout swap, temp shared env).
