# ADR-001: Firecracker Contract-First Harness

- Status: Accepted
- Date: 2026-02-22
- Scope: `cmd/`,`internal/{vm,store,trace,slack}`,`scripts/*`,`mise.toml`,`vm/*`
- Source-of-truth inputs: `spec-0/00-learnings.jsonl`, `spec-0/01-tasks.jsonl`

## Decision (opinionated)
Build/run path is **VM-native, deterministic, contract-checked, failure-explicit**:
1. Runtime is **Firecracker via `firecracker-go-sdk` only** (no container-runtime swap for VM tasks).
2. Image inputs are **content-addressed immutable artifacts** (`.cache/ghostfleet/images/<sha>/`) selected only by `vm/images.lock`.
3. `doctor` is a **hard gate** (explicit fail reason), tolerant of built-in KVM (`/sys/module/kvm`), blocks all downstream work on invariant breach.
4. Smoke correctness is **serial-contract based** (`Linux` + `ok` on `ttyS0`) with host-driven termination (SIGTERM VMM) for deterministic completion.
5. Resume is **attempted snapshot, guaranteed fallback cold boot**; telemetry always persists `resume_mode`,`resume_error`.
6. Data plane is **dual-write contract**: trace JSONL + SQLite (WAL/FK/indexes mandatory) for every run.
7. Task graph is **incremental by design** (`mise` sources/outputs): skips are correctness, not surprise.

## Why this architecture
- Fast feedback loop requires deterministic boot inputs + deterministic exit semantics.
- CI/dev heterogeneity (sudo/no-sudo, module vs built-in KVM, snapshot API variance) demands explicit fallback, not brittle purity.
- Debuggability requires machine-checkable contracts at each seam: host prereq -> image -> VM boot -> trace/db integrity.
- Thin CLI + modular internals preserves replaceability/testability without changing external contract surface.

## Hard invariants (non-negotiable)
- Host: Ubuntu 24.04 bare-metal, KVM-only, `/dev/kvm` rw.
- VM tasks: Firecracker only.
- Topology: thin `cmd/virmux`; logic in `internal/vm|store|trace|slack`; scripts orchestrate only.
- Immutability: image dir addressed by hash; lockfile selects hash.
- Doctor: hard fail + explicit reason + built-in KVM tolerance.
- Smoke: serial must contain `Linux` and `ok`; host controls `ttyS0`; host terminates VMM.
- Resume: fallback cold boot required; mode/error always recorded.
- Data: SQLite WAL+FK+indexes + trace JSONL valid each run.
- DAG: expensive tasks must declare `sources+outputs`; incremental skip expected.

## Core contracts (walkthrough-first)
### C1. Host Gate
Input: machine state.
Output: pass/fail with exact reason (`cpu`,`kvm`,`devkvm`,`firecracker`,`apisock`,`ulimit`,`dirs`).
Rule: fail fast; no best-effort continuation.

### C2. Image Reproducibility
Input: pinned manifest URLs.
Transform: containerized fetch/build ext4.
Output: immutable artifact + metadata in hash dir; selector lockfile.
Property: same inputs => same hash dir; no in-place mutation.

### C3. Smoke Determinism
Flow:
`host -> boot VM -> send serial cmds(uname;echo ok) -> detect markers -> SIGTERM VMM -> persist trace/db`.
Contract success iff both markers present.
Rationale: guest poweroff unreliable with `init=/bin/sh`.

### C4. Resume Robustness
Flow:
`try snapshot resume -> if API/path/prestart invalid => fallback cold boot`.
Always write telemetry:
- `resume_mode in {snapshot_resume,fallback_cold_boot}`
- `resume_error` nullable but populated on fallback.
Goal: green workflow + preserved failure truth.

### C5. Data Integrity
Per run:
- trace JSONL schema-valid.
- sqlite has runs/events (+ slack_events when used).
- WAL mode, FK enforcement, required indexes pass `db:check`.

## Task completion ledger (spec-0 mapped)
- Bootstrap harness: complete.
- Host deps: implemented, privileged path unverified in non-tty sudo context.
- Doctor/image build+stamp/smoke/parallel/zygote/resume+fallback/bench/trace/db/pw/slack/mise DAG: complete and verified via declared entrypoints.

## Accepted tradeoffs
- Host-driven termination over guest shutdown purity (wins deterministic runtime).
- Resume reliability over resume absolutism (fallback allowed, telemetry mandatory).
- Strict gate friction over silent misconfiguration.
- Incremental task skipping over naive always-run (speed + reproducibility).

## Explicit non-goals
- Supporting non-Ubuntu host permutations.
- Container runtime abstraction for VM execution.
- Mutable image pipeline semantics.

## Failure semantics (must remain explicit)
- Gate failures are user-actionable and terminal.
- Resume failure is non-terminal for workflow, terminal for mode truth (must surface in telemetry).
- Privileged dependency install may be environment-blocked; implementation may exist without root-path verification.

## Operator fast-path
```bash
mise run doctor
mise run ci:fast
mise run vm:smoke
mise run trace:validate ::: db:check
```
Perf/race/integration:
```bash
mise run vm:smoke:parallel
mise run vm:zygote
mise run vm:resume
mise run bench:snapshot
mise run slack:recv
mise run pw:install
mise run pw:smoke
```

## Architecture sketch
```text
cmd/virmux (thin CLI)
  -> internal/vm    (boot/snapshot/resume/serial contracts)
  -> internal/trace (jsonl emit/validate assumptions)
  -> internal/store (sqlite WAL/FK/index discipline)
  -> internal/slack (fixture replay + ingest)

scripts/*  = orchestration wrappers only
mise.toml  = DAG + sources/outputs truth
vm/*       = pinned image selection + metadata
```

## Change policy hook
Any behavioral change in `cmd/`,`internal/`,`scripts/`,`mise.toml`,`vm/` must ship:
1. executable guard (test/assertion), and
2. learning capture (`AGENTS.md`/`.codex/rules/*` or `spec-*/00-learnings.jsonl`).

Rationale: prevent silent contract drift.
