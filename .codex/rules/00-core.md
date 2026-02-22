---
paths:
  - "**/*"
---
# Core Rules
- Optimize for edit->verify latency; prefer existing `mise` task entrypoints over ad-hoc command chains.
- Determinism first: no hidden env deps, no mutable-in-place artifacts, no nondeterministic output formats.
- Fail fast + explicit: hard errors include cause + fix hint; avoid silent retries unless policy-approved fallback.
- Keep contracts machine-first: stable JSON/SQL schemas, UTC timestamps, reproducible file paths.
- Minimize blast radius: smallest viable diff, no opportunistic refactors in harness-critical changes.
- If adding a new recurring failure mode, encode it immediately as test/assertion + rule/learning entry.
