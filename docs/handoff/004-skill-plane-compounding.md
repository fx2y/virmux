# Skill Plane: Compounding Artifacts Handoff

Compounding loop for skills-as-artifacts via deterministic VM evidence + host-side gitops.

## 1. Charter & Stance
- **Goal:** Deterministic implementation loop (Lint->Run->Judge->AB->Promote->Refine->Suggest) on existing Virmux evidence plane.
- **Stance:** Contract > Convenience. Evidence > Stdout. Append-only > Mutated.
- **Truth:** Evidence is `runs/<RID>/trace.ndjson` + SQLite rows. Terminal success is host `run.finished` + VSOCK READY/RPC receipts.

## 2. Skill Contract (`skills/<name>/`)
- `SKILL.md`: Sole source-of-truth (SoT). `prompt.md` is compat-only shim.
- `tools.yaml`: Strict integer budgets (`tool_calls`, `seconds`, `tokens`).
- `rubric.yaml`: Mandatory criteria (`format`, `completeness`, `actionability`) + unique IDs + weights.
- `tests/`: JSON fixtures with `tool`, `cmd`, `args`, `expect`, `deterministic: true`.

## 3. Surface & Preflight
- **Canon:** `virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>`.
- **Preflight:**
  ```bash
  mise run doctor # Ensure KVM + /dev/kvm rw
  virmux skill lint --json skills/dd # Zero errors before run
  ```
- **Constraint:** Skill name is strict kebab token; `../` rejected as `SKILL_PATH_ESCAPE`.

## 4. Evidence Map (Ground Truth)
| Command | Primary Files | SQLite Tables |
| :--- | :--- | :--- |
| `skill run` | `meta.json`, `trace.ndjson`, `skill-run.json`, `toolio/*.json` | `runs`, `events`, `artifacts`, `tool_calls` |
| `skill judge`| `score.json` (w/ critique+score) | `scores`, `judge_runs` |
| `skill ab` | `promptfoo.{base,head}.results.json`, `ab-verdict.json` | `eval_runs`, `eval_cases` |
| `skill promote`| git tag `skill/<name>/prod` | `promotions` |
| `skill refine` | `refine.patch`, `refine-pr.md`, `refine-rationale.json` | `refine_runs` |
| `skill suggest`| `suggest-pr.md` | `suggest_runs` |

## 5. Walkthroughs (The Golden Loop)

### PO.11: Deterministic Run + Judge
```bash
# 1. Run with explicit fixture
RID_JSON=$(virmux skill run --agent demo --fixture case01.json dd)
RID=$(jq -r '.run_id' <<<"$RID_JSON")

# 2. Judge evidence against rubric
virmux skill judge "$RID"

# 3. Replay to verify determinism
virmux skill replay "$RID" # Verified=true
```

### PO.12: AB Eval + Promotion
```bash
# 1. Compare refs over head-fixture set (frozen fixture SoT)
virmux skill ab dd base..head --cohort po-demo-01

# 2. Promote based on passing Eval ID
virmux skill promote dd <eval_id> --tag skill/dd/prod
```

### PO.13: Refine (Evidence-to-Branch)
```bash
# Generate patch/PR from scored run evidence
virmux skill refine "$RID"
# Result: Branch refine/dd/$RID + artifacts in runs/$RID/
```

### PO.14: Suggest (Motif-to-Skill)
```bash
# Mine repeats/motifs across scored runs
virmux skill suggest --min-repeats 3 --min-score-p50 0.8
# Result: Branch suggest/dd-<motif> + new skills/suggest-* scaffold
```

## 6. QA & Oracle (`ship:skills`)
- **Isolation:** `ship:core` must NOT reference `skill:*` or `ship:skills`.
- **Oracle:** `mise run ship:skills` must pass uncached.
- **SQL Cert:** `qa-skill-c3-%` cohort must prove `>=1 pass, >=1 fail, >=1 promo`.
- **Typed Failures:** `TOOL_DENIED`, `BUDGET_EXCEEDED`, `REPLAY_MISMATCH`, `AB_REGRESSION`.
  - *Note:* Budget breach still MUST emit run evidence (trace/sqlite).

## 7. FDE Daily Loop (Forensics)
1. **Capture RID:** All pivots start from `runs/<RID>`.
2. **Trace check:** `virmux trace:validate` ensures schema compliance.
3. **Artifact check:** `virmux replay` ensures tool IO hashes match SQLite records.
4. **SQL Board:** 
   ```sql
   SELECT seq, tool, input_hash, output_hash FROM tool_calls WHERE run_id='<RID>' ORDER BY seq;
   SELECT skill, score, pass FROM scores WHERE run_id='<RID>';
   ```

## 8. Anti-Patterns
- **No Parallel Storage:** Do not use host-only FS scans; use SQLite inventory.
- **No Silent Success:** Failures in `skill.judge.started` MUST block scoring inserts.
- **No Path Drift:** `localSkillSHA` hashes canonical rel-paths+bytes; no host-path salt.
- **No Branch Chaining:** `suggest` re-anchors each candidate to captured base HEAD.
- **No Absolute Paths:** Refine/Suggest artifacts use run-relative refs only.
