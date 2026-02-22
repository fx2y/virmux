# RFC-000 Walkthroughs (Operator Fast-Path)

## 1) Due-diligence skill compounding
```bash
rid=$(ghostfleet skill run dd --input cases/acme.md --json | jq -r .run_id)
ghostfleet judge run "$rid"
ghostfleet skill refine suggest "$rid" > /tmp/patch.diff
ghostfleet ab run experiments/dd-candidate-vs-main.yaml
ghostfleet promote skill@candidate-sha
```
Expected:
- `runs/<rid>/trace.jsonl` valid
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
