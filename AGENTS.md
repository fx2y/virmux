# AGENTS.md
Repo control-plane contract. Git-tracked, living, terse. Update on every new failure class or architecture decision.

## Mission
Deterministic Firecracker harness with replayable proof: immutable image inputs, host-driven VM control, append-only evidence, bounded failure semantics, portable export/import.

## Priority Stack (strict order)
1. Contract > convenience.
2. Determinism > throughput.
3. Evidence > stdout.
4. Small blast radius > broad refactor.
5. Explicit seams > globals/singletons.

## Runtime Hard Floors
- Host: Ubuntu 24.04 bare-metal, KVM-only, `/dev/kvm` rw.
- VMM: Firecracker only, launched only through `firecracker-go-sdk`.
- Topology: `cmd/virmux` thin; logic in `internal/{vm,store,trace,slack,agent,transport,agentd}`; `scripts/*` orchestration only.

## Plane Contracts
- Image plane: selector is only `vm/images.lock`; cache is only `.cache/ghostfleet/images/<sha>/`; cache dirs are write-once.
- Image reproducibility: manifest source bytes pinned (`*_sha256`), preverified, mixed into image key; key calc must be canonical/byte-stable.
- State plane: SoT=`agents/<id>.json`; mutable bytes only `volumes/<agent>.ext4`; rootfs always RO (`rootflags=noload`).
- Run plane: host drives VM lifecycle; smoke bridge requires `Linux`+`ok`; host-terminated VMM is completion truth.
- Transport plane: tool path is vsock-first (`CONNECT/OK` + strict `READY v0 tools=` + framed RPC); serial remains bounded bridge for smoke/debug.
- Evidence plane: canonical trace path `runs/<id>/trace.ndjson`; compat `trace.jsonl` symlink allowed; trace is append-only.
- DB plane: sqlite requires WAL+FK+required indexes; dual-write order is `trace emit -> sqlite insert` (never reversed).

## Lifecycle Contracts
- Boundary events are mandatory on VM lanes: `vm.boot.started`, `vm.exec.injected`, `vm.exit.observed`.
- Terminal telemetry must stay non-null/stable for contract keys (`resume_*`, loss counters, handshake/connect stats, `error_obj`).
- Resume precedence: explicit mem/state > `agent.last_snapshot_id` > `latest.json`.
- Resume policy: snapshot attempt once; any resolve/load/wait fault => `StopVMM+Wait` then cold fallback.
- Resume event rule: every `vm:resume` terminal event must include non-null `resume_mode`,`resume_source`,`resume_error`.

## Artifact + Bundle Contracts
- Artifact registry is sqlite-first inventory, not best-effort fs scan.
- Regular files => content hash; non-regular inode types (sock/fifo/symlink/dir) => metadata rows (`sha256=meta:*`,`bytes=0`) using `lstat` semantics.
- Tool evidence must materialize host-visible refs in run dir, then hash/register.
- Export/import must remain deterministic + safe: canonical tar ordering/mtime/uid/gid, manifest verify before DB insert, symlink target escape denied.

## Release Oracle
- One-line decisive gate: `mise run ship:core` (fresh/uncached proof).
- Mandatory gate families: G0 host baseline, G1 boot truth/loss counters, G2 transport chaos, G3 tool policy, G4 trace+db+export determinism, G5 watchdog+partial-export+cleanup.
- SQL cert on append-only DB must be cohort-scoped (for example `label like 'qa-cert-%'`) unless legacy rows were backfilled.
- Cleanup audit is hard: zero orphan `firecracker` procs; zero stale `firecracker.sock`/`vsock*.sock`/`*.fifo`; zero leaked `virmux-tap*`.
- Optional lanes (`vm:net:probe`,`slack:recv`,`pw:*`) are isolated and cannot redefine core contract unless explicitly promoted.

## Engineering Style (ultra-opinionated)
- Fail closed on ambiguity; errors must name invariant + direct fix action.
- Keep I/O at boundaries; keep core logic pure/injectable/testable.
- Inject all unstable deps (clock,id,runner,process probes,timeouts); no hidden env coupling.
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
