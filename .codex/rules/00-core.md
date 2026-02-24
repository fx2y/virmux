---
paths:
  - "**/*"
---
# Core Rules
- Ship facts, not narratives: if not queryable from sqlite/trace/artifacts, it did not happen.
- Contract-complete edits only; deferred invariant fixes are regressions.
- Fail closed + legible: error must name invariant + direct operator action.
- Determinism is mandatory: no hidden env deps, no unstable schema behavior, no host-path-salted hashes.
- Keep I/O at boundaries; keep core logic pure/injectable/testable.
- Inject unstable deps only (`clock,id,runner,process,sleep,timeout,probe`); globals/singletons banned.
- Retries/timeouts must be explicit, bounded, typed, and guard-covered.
- Canonical serialization/hash only: sorted inputs, normalized rel paths, fixed metadata.
- Validators must never mutate evidence/schema (`db:check` is read-only).
- Schema evolution is additive-only; never silently remove/repurpose contract fields.
- Harness-critical diffs are surgical; no opportunistic refactor.
- New recurring failure class must ship same-diff with 1 executable guard + 1 learning capture.
