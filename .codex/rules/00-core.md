---
paths:
  - "**/*"
---
# Core Rules
- Prefer contract-complete edits over partial fixes.
- Prefer canonical `mise` lanes over ad-hoc command chains.
- Determinism is mandatory: ban hidden env deps, mutable-in-place artifacts, unstable schemas.
- Fail closed, fail legibly: every hard error must name violated invariant + fix action.
- Machine-first outputs only: stable SQL/JSON fields, UTC timestamps, reproducible paths, parseable logs.
- Keep I/O at edges; keep core logic pure/injectable/testable.
- Inject unstable deps (`clock`,`id`,`runner`,`process`,`sleep`,`timeout`); no globals/singletons.
- Retries/timeouts must be explicit, bounded, typed (retryable vs non-retryable), and test-covered.
- Serialize deterministically (sorted inputs, canonical delimiters, fixed metadata where required).
- Schema/data evolution is additive; do not silently drop/rename contract keys.
- Diffs in harness-critical paths must stay surgical; no opportunistic refactor.
- New recurring failure class must ship with executable guard + learning capture in same diff.
