---
paths:
  - "**/*"
---
# Core Rules
- Optimize edit->verify latency: prefer canonical `mise` tasks over ad-hoc command chains.
- Contract-first engineering: choose explicit invariant over permissive behavior.
- Determinism first: no hidden env deps, no mutable-in-place artifacts, no nondeterministic output schemas.
- Fail hard, fail legibly: every hard error must include cause + direct fix action.
- Machine-first outputs: stable SQL/JSON fields, UTC timestamps, reproducible paths, parseable logs.
- Opinionated code style:
- Keep I/O at edges; keep core logic pure/testable.
- Inject unstable deps (clock, random, run-id, external runners); avoid globals/singletons.
- Prefer additive, backward-compatible schema evolution; avoid destructive rewrites.
- Keep diffs surgical; no opportunistic refactor in critical lanes.
- New recurring failure class must land with executable guard + learning capture in same diff.
