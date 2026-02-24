---
paths:
  - "**/*"
---
# Core Rules
- `AGENTS.md` is constitution; this file is the short universal overlay.
- Evidence-only truth: no sqlite/trace/artifact proof => no claim.
- Contract-complete diffs only; "fix later" on invariants = regression debt.
- Fail closed; errors name invariant + operator action.
- Strict parser default; unknown keys/types hard-fail unless explicitly forward-allowed.
- Determinism first: no hidden env deps, no host-path-salted hashes, no unstable serialization.
- I/O at boundaries; core pure/injectable/testable; unstable deps DI-only.
- Retries/timeouts explicit+bounded+typed+guarded.
- Validators/certs are read-only; never mutate schema/evidence.
- Schema/API/codes evolve add-only.
- Harness-critical changes are surgical.
- New recurring failure class => same diff ships guard + learning capture.
