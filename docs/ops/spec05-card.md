# Spec-05 Ops Card (C7 Cutover)

Reference map: `spec-0/05/cli-map.jsonl` id `map.cli.ghostfleet->virmux`.

## Canon CLI

`virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>`

## Cert Lane (uncached)

```bash
mise run ship:skills
cat tmp/ship-skills-summary.json
cat tmp/skill-sql-cert-summary.json
cat tmp/spec05-dod-matrix.json
cat tmp/spec05-residual-risk.md
```

## AB / Promote / Rollback / Canary

```bash
# AB (pairwise)
go run ./cmd/virmux skill ab dd HEAD~0..HEAD --judge pairwise --anti-tie

# promote from eval id
go run ./cmd/virmux skill promote dd <eval_id>

# rollback (dry-run then commit)
go run ./cmd/virmux skill promote --rollback --to-ref <git-ref> --dry-run dd
go run ./cmd/virmux skill promote --rollback --to-ref <git-ref> --reason "regression" dd

# canary deterministic cert helper
./scripts/skill_canary_cert.sh
```

## SQL Board (cohort-scoped)

```sql
SELECT id,cohort,pass,score_p50_delta,fail_rate_delta,cost_delta,created_at
FROM eval_runs
WHERE cohort LIKE 'qa-skill-c3-%' OR cohort LIKE 'qa-skill-c5-%'
ORDER BY datetime(created_at) DESC, id DESC
LIMIT 20;

SELECT id,skill,op,eval_run_id,from_ref,to_ref,reason,created_at
FROM promotions
WHERE eval_run_id IN (
  SELECT id FROM eval_runs
  WHERE cohort LIKE 'qa-skill-c3-%' OR cohort LIKE 'qa-skill-c5-%'
)
ORDER BY datetime(created_at) DESC, id DESC
LIMIT 20;

SELECT id,skill,eval_run_id,action,caught_by_canary,summary_path,created_at
FROM canary_runs
ORDER BY datetime(created_at) DESC, id DESC
LIMIT 20;
```

## Required Artifacts

- `tmp/skill-sql-cert-summary.json`
- `tmp/spec05-dod-matrix.json`
- `tmp/spec05-residual-risk.md`
- `tmp/rollback-playbook-smoke.ok`
- `tmp/cleanup-audit.ok`
