# Research Plane (Spec-06) Ops Handoff

## 0. Mission: The Research Wedge
Deliver a contract-first research pipeline (**Plan -> Map -> Reduce**) on existing virmux evidence substrate. Prioritize **determinism and trace integrity** over LLM "intelligence". Lane is currently a **Functional Wedge** with heavy stubs.

## 1. Truth Planes (SoT)
1. **Trace**: `runs/<id>/trace.ndjson` (append-only, research.* events).
2. **FS Artifacts**: `plan.yaml`, `map/*.jsonl`, `reduce/{report.md,table.csv}`, `mismatch.json`.
3. **SQLite**: `runs` (research:* tasks), `artifacts` (inventory), `evidence` (claims), `row_evidence` (links).

## 2. Command Canon (`virmux research`)
| Command | Wrapper Task | Purpose |
| :--- | :--- | :--- |
| `run` | `research:run` | Composite (Plan+Map+Reduce) in one run dir. |
| `plan` | `research:plan` | Compiles query to `plan.yaml`. |
| `map` | `research:map` | Executes scheduler on target `--run` artifacts. |
| `reduce` | `research:reduce` | Pure synthesis of map rows into report/table. |
| `replay` | `research:replay` | Selective rerun (`--only`) + `mismatch.json`. |
| `timeline` | (None) | Ops triage: human-readable events from `trace.ndjson`. |

## 3. Walkthrough: Golden Path (PO/Demo)
```bash
# 1. Composite Run (Fastest)
go run ./cmd/virmux research run --query "market landscape" --label demo-1
RUN_ID=$(jq -r .run_id tmp/virmux.run.json)

# 2. Inspect Artifacts
ls runs/$RUN_ID/{plan.yaml,map/,reduce/}
cat runs/$RUN_ID/reduce/report.md

# 3. Timeline Triage
go run ./cmd/virmux research timeline --run $RUN_ID
```

## 4. Walkthrough: Stage-by-Stage (QA/FDE)
```bash
# 1. Plan First
go run ./cmd/virmux research plan --query "agent query" --label qa-1
PLAN_RUN=$(jq -r .run_id tmp/virmux.run.json)

# 2. Map Standalone (Target same run dir)
go run ./cmd/virmux research map --run $PLAN_RUN --label qa-map

# 3. Reduce Standalone
go run ./cmd/virmux research reduce --run $PLAN_RUN --label qa-reduce
```

## 5. Walkthrough: Replay & Nondet (Ops)
```bash
# 1. Selective Replay (Happy Path)
go run ./cmd/virmux research replay --run $RUN_ID --only track-1

# 2. Force Nondet (Revision Artifact Pattern)
# Clone run -> edit plan.yaml 'deterministic: false' -> replay clone
RUN2="${RUN_ID}-clone"; cp -a runs/$RUN_ID runs/$RUN2
sed -i '/id: track-1/a \  deterministic: false' runs/$RUN2/plan.yaml
go run ./cmd/virmux research replay --run $RUN2 --only track-1
```

## 6. Gap Map (Fake vs Real)
| Feature | State | Truth |
| :--- | :--- | :--- |
| **Planner** | **STUB** | Canned tracks; dummy `track-wide` classifier. |
| **Mapper** | **STUB** | Simulated worker rows; weak evidence extraction. |
| **Scheduler** | **REAL** | Concurrent topo-batches; fail-closed on deps. |
| **Replay** | **REAL** | Parity-diff; selective rerun; fail-closed selector. |
| **Evidence** | **PARTIAL** | Tables exist; semantic content is placeholder. |
| **Cache** | **WEAK** | Key is `query+cellID`, not `query+url` (Spec contract). |

## 7. Dev Ops: Certification
**Do NOT ship** without fresh executable proof. Green `research_cert.sh` is mandatory but insufficient for release (see `s06.exit`).
```bash
# Full E2E Cert
mise run research:cert

# SQL Counter/Freshness Cert
bash scripts/research_sql_cert.sh --label-glob 'research-cert-%' --cert-ts $(date -u +%FT%TZ)

# DoD Matrix Generation (Requires --cert-ts)
bash scripts/spec06_dod_matrix.sh --cert-ts <current-cert-ts>
```

## 8. Implementation Seams (`internal/skill/research/`)
- `Planner`: Compiler logic (Query -> DAG).
- `Scheduler`: Execution orchestration (concurrent batches).
- `Mapper`: Individual track/worker execution.
- `Reducer`: Pure synthesis (no tool calls allowed).
- `Contracts`: Interface definitions and `FailureCode` registry.

## 9. Hard Rules (Expert Peers)
1. **Fail Closed**: Unknown `--only` selector must hard-fail (`RERUN_SELECTOR_INVALID`).
2. **Plan First**: Never execute a tool before `plan.yaml` is written and hash-frozen.
3. **No Drift**: Canon command surface is `virmux research`, not `ghostfleet`.
4. **Pure Reduce**: Reducer must never call tools or touch the network.
5. **Freshness Only**: SQL claims without `--cert-ts` and fresh JSON markers are non-authoritative.
