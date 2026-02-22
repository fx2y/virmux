# AGENTS.md
Only repo-level policy doc; git-tracked; living spec. Update on every new bug class or architecture decision. Heavy detail is imported from scoped `.codex/rules/*` to keep context cost low.

## Mission
Firecracker-first harness: deterministic image build, fast VM loops, trace+sqlite contracts, local Slack/Playwright replay.

## Hard Invariants
- Host: Ubuntu 24.04 bare-metal; KVM-only; `/dev/kvm` must be rw.
- VM runtime: Firecracker via `firecracker-go-sdk`; no container-runtime substitution for VM tasks.
- Topology: thin `cmd/virmux`; logic in `internal/{vm,store,trace,slack}`; `scripts/*` orchestrate only.
- Image immutability: artifacts live at `.cache/ghostfleet/images/<sha>/`; selector is only `vm/images.lock`.
- Doctor is a hard gate with explicit failures; must tolerate built-in kvm via `/sys/module/kvm`.
- Smoke contract: serial must contain `Linux` + `ok`; host drives `ttyS0`; terminate VMM from host for deterministic completion.
- Resume contract: attempt snapshot; on failure fallback cold-boot is required; always persist `resume_mode` and `resume_error`.
- Data contract: sqlite WAL+FK+indexes required; every run emits valid trace JSONL + sqlite events.
- Task DAG contract: expensive `mise` tasks require `sources+outputs`; incremental skips are expected behavior.

## Build + Validate (authoritative entrypoints)
- Tooling: `mise` pinned `go` + `node`; JS package manager is `npm` (not bun/pnpm/yarn).
- Daily: `mise run doctor`; `mise run ci:fast`; `mise run vm:smoke`; `mise run trace:validate ::: db:check`.
- VM perf/race: `mise run vm:smoke:parallel`; `mise run vm:zygote`; `mise run vm:resume`; `mise run bench:snapshot`.
- Integrations: `mise run slack:recv`; `mise run pw:install`; `mise run pw:smoke`.

## Compounding Rule (mandatory)
Any behavioral change in `cmd/`, `internal/`, `scripts/`, `mise.toml`, `vm/` must ship:
- one executable guard (test or script assertion), and
- one learning capture (update `AGENTS.md`/`.codex/rules/*` or append `spec-*/00-learnings.jsonl`).
No guard + no learning = incomplete change.
CI/review policy: treat missing guard/learning as a hard fail.

## Imports
@.codex/rules/00-core.md
@.codex/rules/10-go-vm-store-trace.md
@.codex/rules/20-scripts-mise.md
@.codex/rules/30-contracts-debug.md
@.codex/rules/40-ui-state.md

## Local Overrides
Use `AGENTS.local.md` for private prefs/aliases; never commit it.
