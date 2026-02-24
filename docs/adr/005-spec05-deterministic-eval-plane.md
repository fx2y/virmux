# ADR 005: Spec-05 Deterministic Eval/AB/Canary/Promotion Plane

**Status:** Accepted; Hard-Gated
**North Star:** if not queryable from `runs/*` + `sqlite` + `tmp/*cert`, it did not happen.
**Rule:** contract > convenience; determinism > throughput; evidence > stdout; fail-closed always.

## Context
Spec-05 (evals+AB+canary+promotion discipline) must ship via multi-cycle HTN mapped onto `virmux` contracts. Ghostfleet verbs are translated to `virmux skill` canon (SoT=`spec-0/05/cli-map.jsonl`).

## Decision: Multi-Layered Truth (L0-L2)
| Loop | Scope | Command | Evidence (SoT) |
| :--- | :--- | :--- | :--- |
| **L0: Build** | VM Run | `virmux skill run` | `trace.ndjson`, `skill-run.json`, `toolio/*` |
| **L0: Judge** | Det Checks | `virmux skill judge` | `score.json`, `sqlite:scores+judge_runs` |
| **L1: Compare** | Pairwise AB | `virmux skill ab` | `sqlite:eval_runs+experiments+comparisons` |
| **L2: Ship** | Promo/Rollback | `virmux skill promote` | `sqlite:promotions`, `git tag` |
| **L2: Verify** | Prod Canary | `./scripts/canary_run.sh` | `sqlite:canary_runs`, `canary-summary.json` |

## Hard Invariants
1. **Rule-First Judge:** Deterministic rules (replay, budget, schema) run **before** LLM modes; rule-failure => `pass=false` regardless of rubric.
2. **Fail-Closed Parser:** Typoed modes (e.g., `skill ab --judge typo`) or malformed output => `JUDGE_INVALID` error; zero score/db writes allowed.
3. **Anti-Tie Invariant:** AB tie allowed **only** on dual hard-fail; equal-pass scores must pick deterministic non-tie winner (favor head).
4. **FK-Linked Evidence:** `experiments` -> `eval_runs` via `eval_run_id`; `promotions` -> `eval_runs`; no skill-name-only heuristics.
5. **Validator-Only `db:check`:** Never auto-mutates; missing C3/C5/C6 tables/indexes (e.g., `idx_judge_runs_skill_created_mode`) => hard-fail stop-ship.
6. **Cohort-Scoped Cert:** SQL cert (C7) proves fresh C2-C7 evidence in window (pass+fail AB, promos, canary); historical rows non-authoritative.

## Implementation: cmd-Thin Topology
```text
[CLI] virmux skill <cmd>
  |-- (parse/dispatch/print only)
  v
[Service Seams] internal/skill/{absvc, promosvc, judgeflow, canary, rules}
  |-- (DI: store, trace, artifact, git, clock, id, runner)
  v
[Persistence] runs/<id>/ + sqlite (WAL+FK)
```

## Walkthrough: Golden Path (E2E)
1. **Lint:** `virmux skill lint skills/dd`
2. **Run:** `virmux skill run --fixture case01.json dd` -> RID
3. **Judge:** `virmux skill judge $RID` -> scored
4. **Replay:** `virmux skill replay $RID` -> verified
5. **AB:** `virmux skill ab --judge pairwise --anti-tie dd base..head` -> EID
6. **Promote (Dry):** `virmux skill promote --dry-run dd $EID`
7. **Canary:** `./scripts/canary_run.sh --skill dd --candidate-ref head` -> CID

## Portable Eval Bundles
`virmux export --mode eval --eval-id $EID`
- Captures `eval_runs`, `eval_cases`, `promotions`, `experiments`, `comparisons`, `canary_runs`, `suggest_runs`.
- Deterministic tar ordering + manifest verify.

## Release Oracle: `ship:skills`
- **Gated:** C2-C7 guards must run in order.
- **Artifacts:** `tmp/skill-sql-cert-summary.json`, `tmp/spec05-dod-matrix.json` (DoD mapping spec 549-553).
- **Hard Law:** `ship:skills` isolated/additive; `ship:core` remains zero-skill-ref decisive gate.
