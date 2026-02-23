# RFC-000 Snippets

## Skill scaffold
```bash
virmux skill suggest --run-ids "r123,r124,r130" --name vendor-dd
```

## Experiment config
```yaml
name: dd-main-vs-candidate
dataset: datasets/dd_regression.yaml
baseline:
  skill_ref: dd@main
  model: local-small
candidate:
  skill_ref: dd@feature/refine-2
  model: local-small
metrics:
  primary: score_total
  constraints:
    max_fail_rate: 0.05
    max_cost_usd: 0.75
```

## Promotion predicate (pseudo)
```go
func promotable(b, c Dist) bool {
  return c.P50 > b.P50 && c.FailRate < b.FailRate && c.CostMean <= budget
}
```
