# AGENTS.md
Repo constitution for deterministic VM+skill operations. Git-tracked, living, terse. Update on every new failure class/architecture decision.

## Mission
Deterministic Firecracker harness with replayable proof: immutable inputs, host-owned lifecycle, append-only evidence, bounded failure semantics, portable export/import.

## Priority Stack (strict order)
1. Contract > convenience.
2. Determinism > throughput.
3. Evidence > stdout.
4. Small blast radius > broad refactor.
5. Explicit seams > globals/singletons.

## Runtime Hard Floors
- Host: Ubuntu 24.04 bare-metal, KVM-only, `/dev/kvm` rw.
- VMM: Firecracker only, launched only through `firecracker-go-sdk`.
- Topology: `cmd/virmux` thin; logic in `internal/{vm,store,trace,slack,agent,transport,agentd,skill}`; `scripts/*` orchestration only.

## Plane Contracts
- Image plane: selector=`vm/images.lock`; cache=`.cache/ghostfleet/images/<sha>/`; cache dir immutable/write-once.
- Image reproducibility: manifest source bytes pinned (`*_sha256`) and preverified; image key uses canonical byte-stable mix only.
- State plane: SoT=`agents/<id>.json`; persistent RW bytes only `volumes/<agent>.ext4`; rootfs always RO (`rootflags=noload`).
- Run plane: host drives lifecycle and host-terminated VMM is completion truth.
- Bridge policy: serial `Linux+ok` markers are required for smoke/debug bridge only; vsock-first lanes (including `skill run`) must not depend on tty markers.
- Transport plane: tool path is vsock-first (`CONNECT/OK*` ack + strict `READY v0 tools=` + framed u32le/json RPC); command-local dialers banned.
- Evidence plane: canonical trace path `runs/<id>/trace.ndjson`; `trace.jsonl` symlink is compat only; trace is append-only.
- DB plane: sqlite must run WAL+FK+required indexes; per-event write order is fixed `trace emit -> sqlite insert` (never reversed).
- Artifact plane: sqlite is SoT inventory; regular files hash content; non-regular inode types store `meta:*` + `bytes=0` via `lstat`; tool/skill evidence must be host-materialized then hash-registered.
- Bundle plane: export/import is deterministic+safe (canonical tar ordering/mtime/uid/gid, manifest verify pre-insert, symlink/path escape denial, provenance via `runs.source_bundle`).

## Lifecycle Contracts
- Boundary events are mandatory on VM lanes: `vm.boot.started`, `vm.exec.injected`, `vm.exit.observed`.
- Agent readiness split is semantic: boot boundary emits `vm.guest.ready`; vsock readiness emits `vm.agent.ready` only.
- Terminal telemetry must keep non-null/stable contract keys (`resume_*`,`lost_*`,`guest_ready_ms`,`connect_attempts`,`handshake_ms`,`error_obj`).
- Resume precedence: explicit mem/state > `agent.last_snapshot_id` > `latest.json`.
- Resume policy: snapshot attempt once; any resolve/load/wait fault => `StopVMM+Wait` then cold fallback.
- Resume event rule: every `vm:resume` terminal event must include non-null `resume_mode`,`resume_source`,`resume_error`.
- Shutdown bounds: waits are watchdog-mediated; stalled waits may SIGKILL FC and must trace `vm.watchdog.kill`.
- Host-visible error classes are stable (`TIMEOUT`,`DISCONNECT`,`CRASH`,`DENIED`,`INTERNAL`), not ad-hoc strings.

## Skill Plane (Spec-04)
- Canon artifacts: `skills/<name>/SKILL.md` is SoT; `prompt.md` is compat shim only.
- Canon CLI: `virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>`.
- Skill identity: skill name args are strict kebab tokens; path escapes hard-fail typed `SKILL_PATH_ESCAPE`.
- Skill fingerprint: hash required files `SKILL.md|tools.yaml|rubric.yaml` by canonical rel-path+bytes only; host path salt forbidden; missing required file hard-fails.
- `skill run`: VM-backed, vsock-first, writes evidence on standard run plane; policy denies (`TOOL_DENIED`,`BUDGET_EXCEEDED`) fail closed and still preserve required evidence via host-writer path.
- Fixture resolution is deterministic: `raw -> skill/raw -> skill/tests/raw`; first existing path wins.
- Tool policy parser is strict: budget keys explicit integers only (`tool_calls`,`seconds`,`tokens`); no float/string coercion.
- `skill judge`: fail closed if `skill.judge.started` emit fails; no score-side effects before start boundary evidence exists.
- `skill replay`: deterministic parity uses ordered tool transcript hashes (input+output) and sqlite artifact inventory parity; nondet exemption must be data-declared (`deterministic:false`) and typed `NONDET_FIXTURE`.
- `skill ab`: frozen fixture SoT is head payload set; base id missing hard-fails; eval cfg/results hashes must match exact persisted bundle bytes/paths.
- `skill promote`: requires passing AB verdict with freshness gate (default max-age 24h) else typed refusal (`MISSING_AB_VERDICT`/`STALE_AB_VERDICT`).
- `skill refine`: default eval resolution is latest passing AB row; deny `tools.yaml` edits unless explicit opt-in; dirty target files hard-fail pre-branch; output refs must be run-relative/repo-relative only.
- `skill suggest`: motif/key must derive from run evidence snapshots (not workspace HEAD), dedupe latest score per run, normalize run-scoped refs, and re-anchor each candidate branch to captured base HEAD.
- Skill lane isolation: `ship:skills` is optional/additive and must not couple into `ship:core`.
- Typed skill failures are stable API (at minimum): `TOOL_DENIED`,`BUDGET_EXCEEDED`,`REPLAY_MISMATCH`,`NONDET_FIXTURE`,`AB_REGRESSION`,`MISSING_AB_VERDICT`,`STALE_AB_VERDICT`,`REFINE_PATCH_TOO_LARGE`,`SUGGEST_NOT_TRIGGERED`,`SKILL_PATH_ESCAPE`.

## Release Oracle
- Decisive core gate: `mise run ship:core` (fresh/uncached proof).
- Mandatory gate families: G0 host baseline, G1 boot truth/loss counters, G2 transport chaos, G3 tool policy, G4 trace+db+export determinism, G5 watchdog+partial-export+cleanup.
- SQL cert on append-only DB must be cohort-scoped (for example `label like 'qa-cert-%'`) unless legacy rows were backfilled.
- Cleanup audit is hard: zero orphan `firecracker` procs; zero stale `firecracker.sock`/`vsock*.sock`/`*.fifo`; zero leaked `virmux-tap*`.
- Skill oracle (optional lane): `ship:skills` requires fresh eval cohort proof + docs-drift guard + cleanup audit; cohort SQL cert must prove both pass+fail AB and promotion evidence.
- Optional lanes (`vm:net:probe`,`slack:recv`,`pw:*`,`skill:*`) are isolated and cannot redefine core contract unless explicitly promoted.

## Engineering Style (ultra-opinionated)
- Fail closed on ambiguity; errors must name invariant + direct fix action.
- Keep I/O at boundaries; core logic pure/injectable/testable.
- Inject unstable deps (`clock`,`id`,`runner`,`process`,`sleep`,`timeout`,`probe`); no hidden env coupling/globals/singletons.
- Parser posture is strict-by-default: unknown keys/shape drift/type coercion are hard failures unless contract explicitly allows forward fields.
- Deterministic hashing/serialization must use canonical ordering + normalized paths + fixed metadata; never hash host-absolute paths.
- Validators are validators, not mutators (`db:check` must not rewrite evidence rows).
- Additive schema evolution only; never silently repurpose/remove evidence fields.
- No opportunistic refactor in harness-critical diffs.

## Compounding Rule (hard fail if missing)
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
