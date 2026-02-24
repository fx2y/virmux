# Virmux Skill Ops & Evaluation Discipline (Spec-05)

Handoff for Spec-05: Scored evals, pairwise AB, promotion/rollback, and prod canary.

## Stance
- **Contract > Convenience**: If it's not in SQLite/Trace, it didn't happen.
- **Determinism > Throughput**: Replay parity is mandatory.
- **Fail-Closed**: Schema/budget/policy faults block all downstream side effects.
- **Evidence-First**: Triage from `runs/<id>/` and `sqlite` only.

## Architecture: The 3 Loops
1. **L0 Build Truth**: `skill run` (VM) + `skill judge` (Det rules) + `skill replay` (Parity).
2. **L1 Compare Truth**: `skill ab --judge pairwise` + `experiments`/`comparisons` tables.
3. **L2 Ship Truth**: `skill promote` (Git tags + Audit) + `canary_run.sh` (Safeguard).

## Technical Seams
- **CLI**: `virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>`.
- **Services**: `internal/skill/{absvc,promosvc,judgeflow,refine,suggest}`.
- **Persistence**: `runs/<id>/trace.ndjson` (append-only) -> `virmux.sqlite`.
- **Artifacts**: `runs/<id>/toolio/*.{req,res}.json`, `score.json`, `ab-verdict.json`.

---

## Walkthroughs & Examples

### 1. The Golden Path (Execution -> Evidence)
Run a skill, judge it, and verify determinism.
```bash
# 1. Lint and Run
go run ./cmd/virmux skill lint skills/dd
RID=$(go run ./cmd/virmux skill run --fixture case01.json dd | jq -r .run_id)

# 2. Judge and Persist (Emits score.json + SQLite rows)
go run ./cmd/virmux skill judge "$RID"

# 3. Replay (Hard parity check)
go run ./cmd/virmux skill replay "$RID"
```

### 2. Pairwise AB (The Decision Engine)
Compare two versions with anti-tie protection.
```bash
# Run AB loop (Independent by default, use --judge pairwise for subjective)
EID=$(go run ./cmd/virmux skill ab --judge pairwise --anti-tie dd HEAD~1..HEAD | jq -r .id)

# View one-line report
go run ./cmd/virmux skill ab --report-only --eval-id "$EID" --fmt one-line
```

### 3. Promotion & Rollback (The Transaction)
Auditable GitOps via SQLite.
```bash
# Promote: Requires fresh passing AB
go run ./cmd/virmux skill promote dd "$EID"

# Rollback: Emergency revert with audit reason
go run ./cmd/virmux skill promote --rollback --to-ref HEAD~1 --reason "canary failure" dd
```

### 4. Forensic SQL (The Single Board)
Query truth directly from the database.
```sql
-- All-in-one ship decision board
SELECT skill, op, from_ref, to_ref, reason, eval_run_id, created_at 
FROM promotions ORDER BY created_at DESC LIMIT 10;

-- Evidence for a specific AB run
SELECT id, cohort, pass, score_p50_delta, fail_rate_delta, cost_delta 
FROM eval_runs WHERE id = 'EID';
```

### 5. Portability (Export/Import)
Move eval evidence across hosts.
```bash
# Export eval cohort
go run ./cmd/virmux export --mode eval --eval-id "$EID" --out "$EID.tar.zst"

# Import to new DB
go run ./cmd/virmux import --bundle "$EID.tar.zst" --db imported.sqlite
```

---

## Operator Habits (Anti-Patterns)
- **NEVER** assume success from exit=0; check `runs/*/skill-run.json`.
- **NEVER** skip `skill replay`; it's the only guard against non-det drift.
- **AVOID** manual git tagging; `skill promote` ensures the audit row exists.
- **ALWAYS** scope SQL certs by `cohort` + `cert_ts`; historical rows are non-authoritative.

## Core Mandates for Evolution
- **Rule-First Judge**: New judge modes must be added to `internal/skill/rules`.
- **Typed Failures**: Use `TOOL_DENIED`, `BUDGET_EXCEEDED`, `REPLAY_MISMATCH`, etc.
- **DB Check**: `mise run db:check` is a validator; it must NEVER mutate.
- **Image Locking**: `vm/images.lock` is the root of trust for VM runs.
