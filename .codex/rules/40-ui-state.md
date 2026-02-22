---
paths:
  - "web/**"
  - "ui/**"
  - "frontend/**"
  - "**/*.tsx"
  - "**/*.jsx"
  - "**/*.css"
---
# UI + State Rules (future-facing)
- UI is an operator console, not marketing: terse copy, explicit status, no decorative ambiguity.
- Canonical data model is run/event/trace semantics from backend contracts; never invent alternate truth in UI.
- State split:
- Server state (fetched/persisted) is immutable snapshots keyed by IDs.
- UI state (filters/panels/sort) is ephemeral and local.
- Derived data must be pure selectors; no duplicated denormalized blobs.
- Time/render contract: show UTC timestamps; preserve raw IDs + statuses; avoid lossy formatting in primary views.
- Error UX: surface exact failing invariant + next command (`doctor`, `trace:validate`, `db:check`, etc.).
- New UI flows require deterministic replay fixture(s) and at least one headless smoke assertion.
