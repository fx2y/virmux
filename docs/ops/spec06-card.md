# Spec-06 Operations Card: Research Superagent

## Lane ID: `research`
Coherent research lifecycle: plan -> parallel map -> pure reduce.

## Command Surface
- `virmux research plan --query "<q>"`: Generate deterministic DAG + plan.yaml.
- `virmux research map [--run <id>]`: Execute tracks in parallel.
- `virmux research reduce [--run <id>]`: Synthesize map outputs into artifacts.
- `virmux research run --query "<q>"`: End-to-end composite command.
- `virmux research replay [--run <id>] [--only <ids>]`: selective track rerun.
- `virmux research timeline [--run <id>]`: render event journey.

## Truth SoT
- Run state: `runs/<id>/plan.yaml` + `runs/<id>/map/*.jsonl` + `runs/<id>/reduce/*`.
- Canonical Trace: `runs/<id>/trace.ndjson` (event namespace `research.*`).
- Evidence Store: `evidence` and `row_evidence` tables in `virmux.sqlite`.

## Budgets
- Parallel tracks: 4 (default).
- Storage: Artifacts registered in sqlite inventory.
- Network: Cached retrieval (sha256(query+url)) in `~/.cache/virmux/research/`.

## Critical Alarms
- `PLAN_NOT_WRITTEN_FIRST`: Planner side effects before persistence.
- `REDUCER_IMPURE`: Reducer attempted tool/network I/O.
- `RERUN_SELECTOR_INVALID`: Replay requested tracks not in original plan.
- `research.replay.mismatch`: replayed track diverged from original result.

## Maintenance
- Certification: `mise run research:cert`
- Cleanup: `rm -rf ~/.cache/virmux/research/` (resets retrieval dedupe)
