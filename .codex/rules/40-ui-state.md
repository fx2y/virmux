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
- UI is an operator console, not marketing: terse copy, explicit status, zero decorative ambiguity.
- Backend contracts are SoT: `runs`,`events`,`tool_calls`,`artifacts`,`scores`,`judge_runs`,`eval_runs`,`promotions`,`refine_runs`,`suggest_runs`,`trace`.
- State split is hard: server snapshots keyed by IDs; UI state for controls only; derived state via pure selectors only.
- Render canon: UTC timestamps; raw IDs/enums visible; no lossy prettification of contract keys.
- Failure keys stay first-class: `error_code`,`error_retryable`,`resume_mode`,`resume_source`,`resume_error`.
- Tool/skill evidence must be inspectable by stable hash+ref+path links in one click path.
- Error panels must show violated invariant + exact repair command (`doctor`,`trace:validate`,`db:check`,`vm:cleanup:audit`,`ship:core`,`ship:skills`).
- New UI features require deterministic fixtures + at least one headless assertion bound to contract rows/events.
