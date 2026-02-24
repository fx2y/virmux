# AGENTS.md
Living constitution for deterministic VM/skill/research ops. Update on every new invariant, failure class, or lane admission decision.

## North Star
Queryable evidence only. If `sqlite + trace.ndjson + artifacts` cannot prove it, it did not happen.

## Order (hard)
1. Contract > convenience
2. Determinism > throughput
3. Evidence > stdout
4. Small blast radius > broad refactor
5. Explicit seams > globals/singletons

## Runtime Floor (non-negotiable)
- Host: Ubuntu 24.04 bare metal, `/dev/kvm` rw, KVM-only.
- VMM: Firecracker only, launched only via `firecracker-go-sdk`.
- Topology: `cmd/virmux` parse/DI/print only; behavior in `internal/*`; `scripts/*` orchestration only.
- Unstable deps DI-only: `clock,id,runner,process,sleep,timeout,probe`.

## Global Contracts
### Planes / SoT
- Images: `vm/images.lock` -> immutable cache `.cache/ghostfleet/images/<sha>/`; sha from canonical pinned source bytes only.
- State: SoT `agents/<id>.json`; persistent RW bytes only `volumes/<agent>.ext4`; rootfs RO (`rootflags=noload`).
- Run truth: host-owned lifecycle; host-observed VMM exit is completion truth.
- Transport: shared vsock dialer only; CONNECT ack prefix `OK `; strict `READY v0 tools=`; framed `u32le/json`; no cmd-local dialers.
- Bridge `Linux+ok`: smoke/debug marker only; never truth for vsock lanes.
- Evidence: canonical trace `runs/<id>/trace.ndjson`; `trace.jsonl` symlink compat-only; append-only.
- DB: per-event order is fixed `trace emit -> sqlite insert`; sqlite invariants hard (`WAL+FK+required idx`).
- Artifacts: sqlite inventory is SoT; regular=content hash; non-regular=`meta:*` + `bytes=0` + `lstat`; host materialize then hash-register.
- Bundle: deterministic+safe export/import only (canonical tar order/mtime/uid/gid; manifest pre-verify; symlink/path-escape deny; `runs.source_bundle` provenance); partial-fail runs still export partial bundle.

### VM / Lifecycle
- Mandatory boundaries: `vm.boot.started`,`vm.exec.injected`,`vm.exit.observed`.
- Readiness split: `vm.guest.ready` (boot) vs `vm.agent.ready` (vsock).
- Terminal telemetry keys non-null/stable: `resume_*`,`lost_*`,`guest_ready_ms`,`connect_attempts`,`handshake_ms`,`error_obj`.
- Resume precedence (one-shot): explicit mem/state > `agent.last_snapshot_id` > `latest.json`; any resolve/load/wait fault => `StopVMM+Wait` then cold boot.
- Every `vm:resume` terminal event includes non-null `resume_mode`,`resume_source`,`resume_error`.
- Waits are watchdog-bounded; stalled waits may SIGKILL FC and must emit `vm.watchdog.kill`.
- Stable host error classes: `TIMEOUT`,`DISCONNECT`,`CRASH`,`DENIED`,`INTERNAL`,`JUDGE_INVALID`.

### Engineering Posture (ultra-opinionated)
- Fail closed on ambiguity. Error = violated invariant + operator action.
- Strict parsers by default. Unknown keys/shape/type coercion hard-fail unless forward fields are explicitly allowed.
- Validators never mutate (`db:check`/linters/cert checks report only).
- I/O only at boundaries; core logic pure/injectable/testable.
- Retries/timeouts explicit, bounded, typed, guard-covered.
- Deterministic serialization/hash only: canonical order, normalized rel-paths, fixed metadata; never hash host-absolute paths.
- Schema/API evolution add-only; never silently repurpose/remove contract fields/codes.
- Harness-critical diffs are surgical; no opportunistic refactor.
- Machine-readable stdout is sacred: one canonical object/stream; progress/logging goes stderr.
- Guard ledgers/matrices are claims, not prose: every named guard must resolve to a real executable test/script/check in-tree.
- Tests touching global process state (`os.Chdir`, stdout swap, env mutation) must not use `t.Parallel`.

## Skill Plane (Spec-05 hardened)
- Canon: `skills/<name>/SKILL.md` SoT (`prompt.md` compat-only); CLI `virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>`.
- Identity/parser: skill arg strict kebab token; path escape => `SKILL_PATH_ESCAPE`; unknown mode/enum hard-fails.
- Fingerprint: hash `SKILL.md|tools.yaml|rubric.yaml` by canonical rel-path+bytes; fixtures resolve `raw -> skill/raw -> skill/tests/raw`.
- Policy parsing: budgets are explicit integers only (`tool_calls`,`seconds`,`tokens`).
- `skill run`: VM-backed, vsock-first, tty-decoupled; deny paths (`TOOL_DENIED`,`BUDGET_EXCEEDED`) fail closed and still persist required evidence.
- Judge: rule-first fail-closed; `skill.judge.started` emit failure => zero score/judge_run writes; invalid/unknown output/mode/schema => `JUDGE_INVALID`; persist raw output + schema hash.
- Replay parity: ordered tool input/output hashes + sqlite artifact parity; nondet only via declared data (`deterministic:false`) => `NONDET_FIXTURE`.
- AB: pairwise rows FK-link via `experiments.eval_run_id`; tie only on dual hard-fail; both-pass equal score must choose deterministic non-tie winner.
- Promote/rollback: require fresh passing AB verdict (default max-age 24h) else `MISSING_AB_VERDICT|STALE_AB_VERDICT`; rollback requires resolvable current+target refs; `commit_sha` stores resolved immutable SHA.
- Refine: default eval=latest passing AB row; `tools.yaml` edits denied unless explicit opt-in; dirty targets hard-fail; hunk cap => `REFINE_PATCH_TOO_LARGE`; persisted refs run-/repo-relative only.
- Suggest: mine run evidence snapshots (not workspace HEAD); motif key from normalized run-scoped fingerprints; dedupe latest score/run; re-anchor candidate branches to captured base HEAD; non-trigger => `SUGGEST_NOT_TRIGGERED`.
- Canary/data: `canary_runs` is SoT; row+summary persist even on auto-action failure; datasets append/new-version only (no in-place JSONL edits).
- Stable typed skill failures (minimum API): `TOOL_DENIED`,`BUDGET_EXCEEDED`,`REPLAY_MISMATCH`,`NONDET_FIXTURE`,`AB_REGRESSION`,`MISSING_AB_VERDICT`,`STALE_AB_VERDICT`,`REFINE_PATCH_TOO_LARGE`,`SUGGEST_NOT_TRIGGERED`,`SKILL_PATH_ESCAPE`,`JUDGE_INVALID`.

## Research Plane (Spec-06 wedge; not assumed shipped)
- Canon/runtime split law: runtime canon is `virmux research <plan|map|reduce|replay|run>`; any extra surface (e.g. `timeline`) is drift until canonized in maps/docs/tests. Translation-only ghostfleet vocabulary must not create parallel runtime namespace.
- Seams first: `internal/skill/research` owns behavior behind explicit seams (`Planner`,`Scheduler`,`Mapper`,`Reducer`,`Replay`/typed failures). `cmd` may not accrete orchestration.
- Plan contract: strict parse (`UnmarshalStrict` + `Validate()`); `plan_id` from canonical YAML body excluding `plan_id`; `research.plan.created` emits before any tool/scheduler side effect; selector preflight (`--only`) happens before emit/mutation (unknown => typed fail, e.g. `RERUN_SELECTOR_INVALID`); schema floors must be explicit/ratcheting (e.g. `dims_you_didnt_ask>=4`, non-empty `reduce.outputs`).
- Scheduler contract: concurrent topo batches allowed; dependency failures cascade `BLOCKED`; subset runs with unsatisfied deps must terminalize `BLOCKED` (no zero-progress deadlock); worker infra/semaphore/context errors propagate fail-closed.
- Map contract: writes `runs/<id>/map/<track>.jsonl` rows with stable schema + `research.map.track.*` events; deterministic-default rows must not embed wall-clock payloads.
- Wide-search contract: cache key basis is canonical `sha256(query + LF + url)`; cache-hit rows are explicit (`cache:hit`); coverage stop uses deterministic ledger, not loop whim.
- Evidence/reduce contract: evidence SoT is sqlite (`evidence`,`row_evidence`); reducer is pure fn of target `map/*` (+ target run metadata) -> `reduce/*`; uncited rows stay visible but demoted (`weight=0`); `## Contradictions` section always emitted (`None.` on clean path).
- Replay contract: parity is ordered map+reduce/artifact hashes (not row count only); deterministic mismatch => typed `REPLAY_MISMATCH` + persisted `mismatch.json`; nondet bypass only explicit `deterministic:false` and must emit exception event.
- Wrapper vs target run law: standalone `research map|reduce|replay|timeline` create wrapper runs; target artifacts/mismatch live on target run; wrapper trace explains execution. Never mix wrapper `run_id` with target artifact/sql probes.
- Artifact registration law: target-run `plan.yaml`,`map/*`,`reduce/*`,`mismatch.json` must be hash-registered via shared path; FS-only cloned targets without DB row skip registration (preserve FK fail-closed).
- Default run selection law: auto-select latest run only after required-artifact filter (`trace.ndjson`,`plan.yaml`,`map/` etc.), never naive latest dir.
- Maturity truth law: cert green != ship. If product/contract P1 gaps remain (planner stub, schema underfit, timeout/budget/retry missing, weak evidence semantics, shallow replay parity, missing guard/script coverage, weak ship lane), lane stays unshipped.
- Claim discipline for demos/tutorials: separate `what is real now` vs `must not claim`; prove with run-dir + trace + sqlite + cert markers. Narrative/task ledgers are coordination aids, never authority without fresh executable proof.

## Release Oracle
- Decisive core gate: uncached `mise run ship:core`.
- Core families mandatory: G0 host baseline; G1 boot truth/loss; G2 transport chaos; G3 tool policy; G4 trace+db+export determinism; G5 watchdog+partial-export+cleanup.
- `ship:skills` is additive/isolated (C2..C7); must not couple/redefine `ship:core`.
- New optional lanes (incl research) are non-authoritative until they have isolated ship lane + freshness-scoped certs + cleanup law.
- SQL certs must be cohort-scoped + freshness-scoped (`cert_ts`/window/label); historical rows alone are non-authoritative.
- Cleanup audit hard law: zero orphan `firecracker`; zero stale `firecracker.sock|vsock*.sock|*.fifo`; zero leaked `virmux-tap*`.

## New-Lane Admission (future iterations)
A lane is incomplete unless same diff defines:
1. SoT rows/files + canonical IDs + deterministic hash basis
2. Boundary events + terminal keys + typed failures
3. Export/import scope + replay/parity rule
4. One executable guard (success + failure path)
5. One learning capture (`AGENTS.md` / `.codex/rules/*` / `spec-*/00-learnings.jsonl`)

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
