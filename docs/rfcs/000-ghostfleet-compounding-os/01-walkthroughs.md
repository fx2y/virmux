# RFC-000 Walkthroughs (Operator Fast-Path)

## 1) Due-diligence skill compounding
```bash
rid=$(virmux skill run dd --fixture skills/dd/tests/case01.json | jq -r .id)
virmux skill judge "$rid"
virmux skill refine "$rid"
virmux skill ab dd HEAD~1..HEAD
virmux skill promote dd <eval_run_id>
```
Expected:
- `runs/<rid>/trace.ndjson` valid (compat `trace.jsonl` symlink may exist)
- `scores.pass=true` for promoted candidate
- `promotions` has one row with reason payload

## 2) Research wedge demo
```bash
ghostfleet research "EU cloud sovereignty vendor map 2026"
```
Pipeline:
1. planner emits DAG
2. specialists parallelize source collection
3. synthesizer writes `report.md` (+ optional `slides.md`)

## 3) Slack ghost safe mode
```bash
ghostfleet role enable triage-ghost
ghostfleet slack recv --fixture fixtures/slack/message_event.json
```
Decision matrix:
```text
if confidence<T or utility<T or cooldown active -> SILENT(draft only)
else -> POST
```
