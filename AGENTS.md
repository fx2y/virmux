# AGENTS.md
Repo control-plane spec. Git-tracked, living, terse. Update on every new failure class or architecture decision.

## Mission
Deterministic Firecracker harness with fast proof loops and replayable evidence:
- immutable image inputs
- host-driven VM execution
- total trace+sqlite contracts
- reproducible resume/perf/cert lanes

## Doctrine (ultra-opinionated)
- Contract > convenience. If behavior is ambiguous, fail hard with fix hint.
- Determinism > throughput. Ban hidden env deps and mutable-in-place artifacts.
- Evidence > stdout. Ship decisions only when sqlite/trace/artifacts prove them.
- Small blast radius. No opportunistic refactor in harness-critical diffs.
- Explicit seams. Inject time/id/runner deps; avoid singleton/global coupling.

## Non-Negotiables
- Host/runtime: Ubuntu 24.04 bare-metal, KVM-only, `/dev/kvm` rw, Firecracker via `firecracker-go-sdk` only.
- Topology: thin `cmd/virmux`; logic in `internal/{vm,store,trace,slack,agent}`; `scripts/*` orchestration only.
- Image immutability: `.cache/ghostfleet/images/<sha>/`; selected only by `vm/images.lock`; cache dirs are write-once.
- Image reproducibility: source bytes are pinned in manifest (`*_sha256`), verified pre-build, and mixed into image cache key.
- State plane: canonical agent state in `agents/<id>.json`; persistent mutable bytes in `volumes/<agent>.ext4`; rootfs stays RO (`rootflags=noload`).
- Run plane: host drives `ttyS0`; smoke markers must include `Linux` + `ok`; host terminates VMM for deterministic completion.
- Resume: attempt snapshot once; any resolve/load/wait fault must fallback cold boot; snapshot-fail path must `StopVMM+Wait` before fallback.
- Resume telemetry: every `vm:resume` terminal event includes non-null `resume_mode`,`resume_source`,`resume_error`.
- Boundary events: `vm.boot.started`,`vm.exec.injected`,`vm.exit.observed` are mandatory for VM task triage.
- Data plane: sqlite WAL+FK+required indexes; append-only trace JSONL; dual-write order is `trace emit -> sqlite insert`.
- Artifact contract: regular files are hashed; sockets/fifo/symlink/dir persist metadata rows (`sha256=meta:*`, `bytes=0`).
- DAG contract: expensive `mise` tasks must define precise `sources+outputs`; incremental skip is expected and must stay correct.

## Release Gates
- Daily core: `mise run doctor`; `mise run ci:fast`; `mise run vm:smoke`; `mise run trace:validate ::: db:check`.
- VM correctness/perf: `mise run vm:smoke:parallel`; `mise run vm:zygote`; `mise run vm:resume`; `mise run bench:snapshot`.
- Cert SQL must be cohort-scoped for append-only DBs (for example `label like 'qa-cert-%'`), unless legacy rows are backfilled.
- Cleanup gate: zero orphan `firecracker` procs, zero stale `runs/**/firecracker.sock`, zero leaked `virmux-tap*`.
- Optional lanes (`vm:net:probe`,`slack:recv`,`pw:*`) are isolated; they do not redefine core VM contract unless explicitly required.

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
`AGENTS.local.md` is for private aliases/prefs; never commit it.
