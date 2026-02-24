---
paths:
  - "web/**"
  - "ui/**"
  - "frontend/**"
  - "**/*.tsx"
  - "**/*.jsx"
  - "**/*.css"
---
# UI + State Rules
- UI = ops surface, not marketing. Terse copy, explicit status, no decorative ambiguity.
- Backend rows/events/artifacts are SoT; UI never invents derived truth not reproducible from contract data.
- State split hard: server snapshot keyed by IDs; UI state only controls/view prefs; derived state via pure selectors.
- Render canon: UTC timestamps + raw IDs/enums/codes; no lossy prettification of contract fields.
- Failure keys are first-class (`error_code`,`error_retryable`,`resume_*`,`lost_*`,`judge_invalid_count`, lane-specific typed failures).
- Evidence drilldown must expose hash + ref + path in one interaction.
- Error panels must show violated invariant + exact repair command (`doctor`,`trace:validate`,`db:check`,`vm:cleanup:audit`,`ship:*`).
- Research UI rule: visually distinguish wrapper run vs target run everywhere replay/map/reduce/timeline are shown.
- New UI behavior ships deterministic fixtures + at least one headless assertion bound to contract rows/events.
