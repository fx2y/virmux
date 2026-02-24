# Spec-05 Rollback Playbook

## 1) Fast rollback (dry-run)

```bash
go run ./cmd/virmux skill promote --rollback --to-ref <baseline-ref> --dry-run dd
```

Expected: JSON output with `op="rollback"`, no git/db mutation.

## 2) Hard rollback (audited)

```bash
go run ./cmd/virmux skill promote --rollback --to-ref <baseline-ref> --reason "canary regression" dd
```

Expected sqlite evidence:

```sql
SELECT id,skill,op,from_ref,to_ref,reason,eval_run_id,created_at
FROM promotions
WHERE op='rollback'
ORDER BY datetime(created_at) DESC, id DESC
LIMIT 5;
```

## 3) Post-rollback checks

```bash
./scripts/cleanup_audit.sh
cat tmp/cleanup-audit.log
./scripts/rollback_playbook_smoke.sh
```

## 4) Canary linkage query

```sql
SELECT c.id,c.eval_run_id,c.action,c.caught_by_canary,p.id AS rollback_promotion_id
FROM canary_runs c
LEFT JOIN promotions p ON p.eval_run_id=c.eval_run_id AND p.op='rollback'
WHERE c.action='rollback'
ORDER BY datetime(c.created_at) DESC, c.id DESC
LIMIT 10;
```
