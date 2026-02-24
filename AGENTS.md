# AGENTS.md
Living constitution for deterministic VM+skill ops. Update on each new failure class/architecture decision.

## North Star
Only queryable evidence counts. If sqlite+trace+artifacts cannot prove it, it did not happen.

## Priority (strict)
1. Contract > convenience.
2. Determinism > throughput.
3. Evidence > stdout.
4. Small blast radius > broad refactor.
5. Explicit seams > globals/singletons.

## Runtime Floors
- Host: Ubuntu 24.04 bare metal; `/dev/kvm` rw; KVM-only.
- VMM: Firecracker only; launch only via `firecracker-go-sdk`.
- Topology: `cmd/virmux` parse/DI/print only; behavior in `internal/{vm,store,trace,slack,agent,transport,agentd,skill}`; `scripts/*` orchestration only.
- Unstable deps are injected only: `clock,id,runner,process,sleep,timeout,probe`.

## Plane Contracts
- Image: `vm/images.lock`; cache `.cache/ghostfleet/images/<sha>/` write-once; key from canonical pinned source bytes only.
- State: SoT `agents/<id>.json`; persistent RW bytes only `volumes/<agent>.ext4`; rootfs RO (`rootflags=noload`).
- Run truth: host-owned lifecycle; host-observed VMM exit is completion truth.
- Bridge: `Linux+ok` markers only for smoke/debug bridge; vsock lanes are tty-independent.
- Transport: shared vsock dialer only; CONNECT ack prefix `OK `; strict `READY v0 tools=`; framed `u32le/json` RPC; no cmd-local dialers.
- Evidence: canonical trace `runs/<id>/trace.ndjson`; `trace.jsonl` symlink compat-only; append-only.
- DB: per-event order fixed `trace emit -> sqlite insert`; sqlite hard invariants `WAL+FK+required indexes`.
- Artifact: sqlite inventory is SoT; regular=content hash; non-regular=`meta:*`,`bytes=0`,`lstat`; tool/skill evidence host-materialized then hash-registered.
- Bundle: deterministic/safe export-import only (canonical tar order/mtime/uid/gid, manifest verify pre-insert, symlink/path-escape deny, `runs.source_bundle` provenance).

## VM/Lifecycle
- Mandatory boundaries: `vm.boot.started`,`vm.exec.injected`,`vm.exit.observed`.
- Readiness split: `vm.guest.ready` (boot) vs `vm.agent.ready` (vsock).
- Terminal telemetry keys non-null/stable: `resume_*`,`lost_*`,`guest_ready_ms`,`connect_attempts`,`handshake_ms`,`error_obj`.
- Resume precedence: explicit mem/state > `agent.last_snapshot_id` > `latest.json`; attempt once; any resolve/load/wait fault => `StopVMM+Wait` then cold fallback.
- Every `vm:resume` terminal event includes non-null `resume_mode`,`resume_source`,`resume_error`.
- Waits are watchdog-bounded; stalled waits may SIGKILL FC and must emit `vm.watchdog.kill`.
- Stable host error classes: `TIMEOUT`,`DISCONNECT`,`CRASH`,`DENIED`,`INTERNAL`,`JUDGE_INVALID`.

## Skill Plane (Spec-05 hardened)
- Canon: `skills/<name>/SKILL.md` SoT (`prompt.md` compat); CLI `virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>`.
- Identity/parser: skill arg strict kebab token; path escape => `SKILL_PATH_ESCAPE`; unknown mode/enum hard-fails.
- Fingerprint: hash `SKILL.md|tools.yaml|rubric.yaml` by canonical rel-path+bytes; fixture resolution `raw -> skill/raw -> skill/tests/raw`.
- Policy parsing: budgets are explicit integers only (`tool_calls`,`seconds`,`tokens`).
- `skill run`: VM-backed, vsock-first, tty-decoupled; denies (`TOOL_DENIED`,`BUDGET_EXCEEDED`) fail closed and still persist required evidence.
- Judge: rule-first fail-closed; `skill.judge.started` emit failure => zero score/judge_run writes; invalid/unknown output/mode => `JUDGE_INVALID`; persist raw output + schema hash.
- Replay: deterministic parity = ordered tool hashes (input+output) + sqlite artifact parity; nondet only via declared data (`deterministic:false`) => `NONDET_FIXTURE`.
- AB: pairwise rows FK-link via `experiments.eval_run_id`; tie only on dual hard-fail; both-pass equal score must pick deterministic non-tie winner.
- Promote/rollback: require passing fresh AB verdict (default max-age 24h) else `MISSING_AB_VERDICT|STALE_AB_VERDICT`; rollback requires resolvable current+target refs; `commit_sha` must be resolved immutable SHA.
- Refine: default eval=latest passing AB row; deny `tools.yaml` edits unless explicit opt-in; dirty targets hard-fail; hunk cap => `REFINE_PATCH_TOO_LARGE`; persisted refs run-relative/repo-relative only.
- Suggest: mine from run evidence snapshots (not workspace HEAD); motif key from normalized run-scoped fingerprints; dedupe latest score/run; re-anchor candidate branches to captured base HEAD; non-trigger => `SUGGEST_NOT_TRIGGERED`.
- Canary: `canary_runs` is SoT; summary/row must persist even when auto-action fails.
- Datasets: append/new-version only; no in-place JSONL edits.
- Stable typed skill failures (minimum): `TOOL_DENIED`,`BUDGET_EXCEEDED`,`REPLAY_MISMATCH`,`NONDET_FIXTURE`,`AB_REGRESSION`,`MISSING_AB_VERDICT`,`STALE_AB_VERDICT`,`REFINE_PATCH_TOO_LARGE`,`SUGGEST_NOT_TRIGGERED`,`SKILL_PATH_ESCAPE`,`JUDGE_INVALID`.

## Release Oracle
- Decisive core gate: uncached `mise run ship:core`.
- Core families mandatory: G0 host baseline; G1 boot truth/loss; G2 transport chaos; G3 tool policy; G4 trace+db+export determinism; G5 watchdog+partial-export+cleanup.
- `ship:skills` is additive/isolated (C2..C7) and must not couple/redefine `ship:core`.
- SQL cert must be cohort-scoped + freshness-scoped; historical rows alone are non-authoritative.
- Cleanup audit hard law: zero orphan `firecracker`; zero stale `firecracker.sock|vsock*.sock|*.fifo`; zero leaked `virmux-tap*`.

## Engineering Posture
- Fail closed on ambiguity; hard errors name invariant + operator action.
- Strict parser default; unknown keys/shape drift/type coercion hard-fail unless forward fields are explicitly allowed.
- I/O at boundaries; core pure/injectable/testable; retries/timeouts explicit, bounded, typed, guard-covered.
- Deterministic serialization/hash only: canonical order + normalized paths + fixed metadata; never hash host-absolute paths.
- Validators are validators, not mutators (`db:check` must not repair schema/evidence).
- Schema evolution additive-only; never silently repurpose/remove contract fields.
- No opportunistic refactor in harness-critical diffs.
- Determinism test law: tests using `os.Chdir` must not call `t.Parallel`.

## New-Lane Admission (future iterations)
A lane is incomplete unless same diff defines:
1. SoT rows/files + canonical IDs + deterministic hash basis.
2. Boundary events + terminal keys + typed failures.
3. Export/import scope + replay/parity rule.
4. One executable guard (success+failure path).
5. One learning capture (`AGENTS.md` or `.codex/rules/*` or `spec-*/00-learnings.jsonl`).

## Compounding Rule (hard fail)
Any behavioral change in `cmd/`, `internal/`, `scripts/`, `mise.toml`, `vm/` must ship:
- one executable guard (test/assertion/task check)
- one learning capture (`AGENTS.md`, `.codex/rules/*`, or `spec-*/00-learnings.jsonl`)
No guard + no learning = incomplete change.

## Imports
@.codex/rules/00-core.md
@.codex/rules/10-go-vm-store-trace.md
@.codex/rules/20-scripts-mise.md
@.codex/rules/30-contracts-debug.md
@.codex/rules/40-ui-state.md

## Local Overrides
`AGENTS.local.md` is private/local only; never commit.
