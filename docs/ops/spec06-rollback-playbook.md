# Spec-06 Rollback Playbook: Research Superagent

## Failure Classes & Recovery

### 1. `PLAN_SCHEMA_INVALID`
- **Symptom**: `virmux research plan` fails to unmarshal.
- **Root Cause**: Breaking change in `internal/skill/research/planner.go` schema.
- **Rollback**: Revert to previous `internal/skill/research/planner.go`.

### 2. `RERUN_SELECTOR_INVALID`
- **Symptom**: `virmux research replay --only <id>` fails.
- **Root Cause**: Requested ID not found in `plan.yaml` for that run.
- **Action**: Verify `track-id` in `runs/<id>/plan.yaml` before replaying.

### 3. `research.replay.mismatch` (Contradiction)
- **Symptom**: Reducer report shows "Contradictions" section.
- **Root Cause**: Replayed track produced different data than original.
- **Resolution**:
  1. Check `runs/<id>/mismatch.json` for details.
  2. If track is intentionally non-deterministic, update plan to include `deterministic: false`.
  3. If track is deterministic, investigate tool provider (mapper) for drift.

### 4. `REDUCER_IMPURE` (Breach)
- **Symptom**: Reducer attempted tool or network call.
- **Severity**: HIGH (Violation of pure synthesis contract).
- **Rollback**: Immediate revert of `internal/skill/research/reducer.go` to pure state.

### 5. `PLAN_NOT_WRITTEN_FIRST` (Breach)
- **Symptom**: Trace shows tool calls before `research.plan.created`.
- **Severity**: HIGH (Violation of plan-first contract).
- **Rollback**: Fix runner lifecycle in `cmd/virmux/research.go` or `internal/skill/research/planner.go`.

## Rollback Command
- `git revert <commit_sha>` of the research lane feature.
- `mise run research:cert` (verify recovery).
