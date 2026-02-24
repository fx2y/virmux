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
- UI is ops console: terse copy, explicit status, no decorative ambiguity.
- Backend contracts are SoT (`runs`,`events`,`tool_calls`,`artifacts`,`scores`,`judge_runs`,`eval_runs`,`experiments`,`comparisons`,`promotions`,`canary_runs`,`refine_runs`,`suggest_runs`,`trace`).
- State split hard: server snapshots keyed by ID; UI state only for controls; derived state via pure selectors.
- Render canon: UTC timestamps + raw IDs/enums; no lossy prettification of contract fields.
- Keep failure keys first-class: `error_code`,`error_retryable`,`resume_mode`,`resume_source`,`resume_error`,`judge_invalid_count`.
- Evidence drilldown must expose hash+ref+path in one click path.
- Error panels must show violated invariant + exact repair command (`doctor`,`trace:validate`,`db:check`,`vm:cleanup:audit`,`ship:core`,`ship:skills`).
- New UI behavior requires deterministic fixtures + at least one headless assertion bound to contract rows/events.
