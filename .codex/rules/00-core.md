---
paths:
  - "**/*"
---
# Core Rules
- Contract-complete edits only; partial fixes are regressions deferred.
- Determinism is non-negotiable: no hidden env deps, no in-place mutation of canonical artifacts, no unstable schema behavior.
- Fail closed + fail legibly: every hard error names invariant + operator fix action.
- Evidence-first outputs: stable SQL/JSON keys, UTC timestamps, parseable logs, reproducible refs.
- Keep I/O at boundaries; keep core pure/injectable/testable.
- Inject unstable deps (`clock`,`id`,`runner`,`process`,`sleep`,`timeout`,`probe`); globals/singletons banned.
- Retries/timeouts are explicit, bounded, typed, and test-covered.
- Serialization/hashing is canonical (sorted inputs, normalized paths, fixed metadata).
- Validators must not mutate evidence (`db:check`-style rewrite is forbidden).
- Schema evolution is additive only; never silently remove/repurpose contract fields.
- Harness-critical diffs stay surgical; opportunistic refactor is a contract breach.
- Every new recurring failure class ships same-diff with 1 executable guard + 1 learning capture.
