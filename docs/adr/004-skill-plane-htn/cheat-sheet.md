# Skill Plane Expert Cheat Sheet

## Contracts
- **Path:** `skills/<name>/`
- **SoT:** `SKILL.md` (frontmatter + body)
- **SHA:** `sha256(SKILL.md | tools.yaml | rubric.yaml)`
- **Budget:** `int64` only; `tool_calls|seconds|tokens`
- **Replay:** Match `tool_seq + args_hash + output_hash + artifact_inventory`

## Evidence Path
- **Trace:** `runs/<id>/trace.ndjson`
- **DB:** `virmux.sqlite`
- **Artifacts:** `runs/<id>/{skill-run,score,refine-pr,suggest-pr}.json`
- **Inventory:** `lstat` for non-regular; content-hash for files.

## Command TL;DR
| Command | Input | Output | Registry |
| :--- | :--- | :--- | :--- |
| `lint` | `skills/` | JSON / Exit Code | - |
| `run` | `skill + fixture` | `trace + artifacts` | `runs` |
| `judge` | `run_id` | `score.json` | `scores` |
| `ab` | `base..head` | `promptfoo results` | `eval_runs` |
| `promote` | `eval_id` | `git tag` | `promotions` |
| `refine` | `run_id` | `git branch` | `refine_runs` |
| `suggest` | `db` | `git branch` | `suggest_runs` |

## Failure Codes
- `TOOL_DENIED`: Tool not in allowlist.
- `BUDGET_EXCEEDED`: Breach during/pre-run.
- `REPLAY_MISMATCH`: Deterministic drift detected.
- `SKILL_PATH_ESCAPE`: `../` in skill name.
- `STALE_AB_VERDICT`: Promote age > 24h.

## Invariants
- `trace emit` ALWAYS before `sqlite insert`.
- `db:check` is validator-only (no auto-mutation).
- AB frozen fixture = use head payload for base.
- Refine latest-passing only (no failed shadowing).
