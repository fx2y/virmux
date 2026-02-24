# Showcase 006: Spec-06 Research Wedge Live E2E Drillbook

Goal: get real value from current `virmux research` lane today by proving pipeline/evidence behavior (`plan->map->reduce->replay(+timeline)`) end-to-end, while avoiding false claims.

Date truth: as of `2026-02-24`, fresh `bash scripts/research_cert.sh` can pass, but spec-06 is still `NOT shipped` (`s06.exit`) due open P1 product/contract gaps.

Law: demo contracts, not intelligence. Grade by `runs/<id>/* + runs/virmux.sqlite + trace.ndjson`, not prose quality.

## 0) Hard Reality (stop if you need more than this)
- Real now: `research run|plan|map|reduce|replay|timeline`, wrapper traces, target artifacts, evidence rows, replay selector fail-closed, nondet exception path, cert scripts.
- Not real now: planner compiler, spec-complete schema, timeout/budget/retry enforcement, spec-correct query+url cache key, strong evidence semantics, hardened `ship:research`.
- Operational value now: demo wedge plumbing, verify evidence plane continuity, rehearse replay/triage/cert workflows, catch regressions.
- Must-not-claim: production research agent, live factual retrieval quality, ship-ready lane.

## 1) Mental Model (save 30m)
- `research run` = composite convenience; wrapper run == target run.
- `research plan|map|reduce|replay` = wrapper commands; wrapper run writes trace/events, target run holds `plan/map/reduce/mismatch` artifacts.
- Probe sqlite/artifacts on target run for standalone `map|reduce|replay`.
- `timeline` exists and is useful, but canon drift exists (`s06.p2.canon.drift.timeline`); treat as practical surface, not frozen canon.

## 2) Preflight (2m)
```bash
set -euo pipefail
cd /home/haris/projects/virmux
pwd
go version
jq --version
sqlite3 --version
rg --version
test -w runs
```

Pass:
- all tools present
- repo root
- `runs/` writable

Note:
- `research_cert.sh` clears old `tmp/research-*.ok` / cert summaries.
- `spec06_dod_matrix.sh` now requires fresh `--cert-ts`-scoped proofs.

## 3) Query Shape (current stub planner exploit)
Use a long query containing `agent` to trigger current stub wide behavior.

```bash
Q='agent market landscape 2026'
echo "$Q"
```

Why:
- current planner adds `track-wide` when query length is high
- current planner hints path keys off substring `agent`
- this is stub behavior, not planner quality (`s06.p1.plan.stub`)

## 4) PO Fast Showcase (10-15m, truthful)
Use this when you need a coherent live demo without overselling.

### 4.1 One-shot composite run
```bash
go run ./cmd/virmux research run --query "$Q" --label demo-s06-$(date +%s) | tee /tmp/s06.run.json
RUN_ID="$(jq -r .run_id /tmp/s06.run.json)"
test -d "runs/$RUN_ID"
echo "$RUN_ID"
```

Expect:
- stdout JSON summary (`task=research:run`, `status=ok`)
- `runs/$RUN_ID/{plan.yaml,map/,reduce/,trace.ndjson}`

### 4.2 Filesystem proof pack
```bash
ls -1 "runs/$RUN_ID" | sort
sed -n '1,220p' "runs/$RUN_ID/plan.yaml"
ls -1 "runs/$RUN_ID/map"/*.jsonl | xargs -n1 basename | sort
for f in runs/$RUN_ID/map/*.jsonl; do echo "== $(basename "$f")"; wc -l <"$f"; done
ls -1 "runs/$RUN_ID/reduce"
sed -n '1,140p' "runs/$RUN_ID/reduce/report.md"
```

Expect:
- `plan.yaml`, `map/`, `reduce/`, `trace.ndjson` (+ compat `trace.jsonl` symlink)
- plan usually includes `track-1`, `track-2`, `track-synth`, and `track-wide` for long query
- wide map file usually `~8` rows (3x3 grid stops near `coverage>=0.8`)
- reduce emits `report.md`, `slides.md`, `table.csv`

### 4.3 Timeline (operator-facing, not canon-frozen)
```bash
go run ./cmd/virmux research timeline --run "$RUN_ID"
```

Expect:
- `research.plan.created`
- `research.map.track.started|done|failed|blocked`
- later replay events if you replay

### 4.4 SQL proof pack (artifact/evidence SoT checks)
```bash
sqlite3 runs/virmux.sqlite "select task,status,count(*) from runs where id='$RUN_ID' group by task,status;"
sqlite3 runs/virmux.sqlite "select count(*) from evidence where run_id='$RUN_ID';"
sqlite3 runs/virmux.sqlite "select count(*) from row_evidence where run_id='$RUN_ID';"
sqlite3 runs/virmux.sqlite "select count(*), sum(path like '%/plan.yaml'), sum(path like '%/map/%'), sum(path like '%/reduce/%') from artifacts where run_id='$RUN_ID';"
```

Pass:
- `research:run|ok`
- `evidence>=1`
- `row_evidence>=1`
- artifact rows include `plan.yaml`,`map/*`,`reduce/*`

### 4.5 Replay happy path (selective rerun)
```bash
go run ./cmd/virmux research replay --run "$RUN_ID" --only track-1 --label replay-s06-$(date +%s) | tee /tmp/s06.replay.json
RPLAY="$(jq -r .run_id /tmp/s06.replay.json)"
go run ./cmd/virmux research timeline --run "$RPLAY"
if [ -f "runs/$RUN_ID/mismatch.json" ]; then cat "runs/$RUN_ID/mismatch.json"; else echo no-mismatch-file; fi
```

Pass:
- replay wrapper succeeds
- replay timeline shows `research.replay.started|done`
- deterministic deep track usually yields `no-mismatch-file`

### 4.6 Truthful PO script (what to say)
- “This proves plan/map/reduce/replay wiring on the existing trace/sqlite/export substrate.”
- “Planner/retrieval/evidence semantics are still partial/stubbed.”
- “Lane is cert-runnable but not shipped (`s06.exit`).”

## 5) QA Golden Path (stage-split, better for debugging)
Use split mode to isolate stage regressions and wrapper-vs-target confusion.

### 5.1 Plan
```bash
go run ./cmd/virmux research plan --query "$Q" --label qa-s06-$(date +%s) | tee /tmp/s06.plan.json
PLAN_RUN="$(jq -r .run_id /tmp/s06.plan.json)"
test -f "runs/$PLAN_RUN/plan.yaml"
echo "$PLAN_RUN"
```

### 5.2 Map (stdout caveat)
`research map` currently mixes human lines + final JSON on stdout (`s06.p2.map.stdout.mixed-json`). Parse last line only.

```bash
go run ./cmd/virmux research map --run "$PLAN_RUN" --label qa-s06-map | tee /tmp/s06.map.out
tail -n1 /tmp/s06.map.out | jq .
MAP_WRAP="$(tail -n1 /tmp/s06.map.out | jq -r .run_id)"
echo "$MAP_WRAP"
```

Pass:
- no unknown `--only` flag error (if used)
- target map files written under `runs/$PLAN_RUN/map/`

### 5.3 Reduce
```bash
go run ./cmd/virmux research reduce --run "$PLAN_RUN" --label qa-s06-reduce | tee /tmp/s06.reduce.out
tail -n1 /tmp/s06.reduce.out | jq . >/dev/null || true
ls -1 "runs/$PLAN_RUN/reduce"
```

Pass:
- target reduce files written under `runs/$PLAN_RUN/reduce/`

### 5.4 Wrapper-vs-target check (critical)
```bash
sqlite3 runs/virmux.sqlite "select id,task,label,status from runs where id in ('$PLAN_RUN','$MAP_WRAP') order by started_at;"
sqlite3 runs/virmux.sqlite "select count(*) from artifacts where run_id='$PLAN_RUN';"
sqlite3 runs/virmux.sqlite "select count(*) from artifacts where run_id='$MAP_WRAP';"
```

Interpretation:
- `PLAN_RUN` is target artifact SoT for split path
- wrapper runs mostly carry trace/events, not target artifacts

## 6) Contract Probes (fast, high-signal)
### 6.1 Plan-first ordering
```bash
awk '/research.plan.created|research.map.track.started/{print NR,$0}' "runs/$RUN_ID/trace.ndjson"
```

Pass:
- first `research.plan.created` line appears before any `research.map.track.started`

### 6.2 Contradictions section always present
```bash
rg -n '^## Contradictions$|^None\.$' "runs/$RUN_ID/reduce/report.md"
```

Pass:
- header exists
- clean path shows explicit `None.`

### 6.3 Wide/cache behavior (implementation demo, not contract proof)
```bash
wc -l "runs/$RUN_ID/map/track-wide.jsonl" || true
grep -n '"cache":"hit"' "runs/$RUN_ID/map/track-wide.jsonl" || true
```

Interpretation:
- line count near `8` suggests coverage stop behavior
- cache-hit rows prove local cache path only
- does not prove spec-correct query+url cache key (`s06.p1.cache.key.wrong`)

### 6.4 Evidence quality reality check
```bash
sqlite3 runs/virmux.sqlite "select claim,url,quote_span,confidence from evidence where run_id='$RUN_ID' limit 5;"
```

Interpretation:
- presence proves pipeline path
- semantics remain placeholder-ish (`s06.p1.evidence.weak`)

## 7) Replay Drill Set (happy + fail-closed + nondet bypass)
### 7.1 Invalid selector must fail closed
```bash
go run ./cmd/virmux research replay --run "$RUN_ID" --only nope 2>&1 | tee /tmp/s06.replay.err
test ${PIPESTATUS[0]} -ne 0
rg -n 'RERUN_SELECTOR_INVALID' /tmp/s06.replay.err
```

Pass:
- non-zero exit
- typed failure `RERUN_SELECTOR_INVALID`

### 7.2 Nondet exception demo (do not mutate original target)
Clone the run and revision the cloned plan only.

```bash
RUN2="${RUN_ID}-nondet-demo"
rm -rf "runs/$RUN2"
cp -a "runs/$RUN_ID" "runs/$RUN2"
cp "runs/$RUN2/plan.yaml" "runs/$RUN2/plan.rev1.yaml"
sed '/id: track-1/a \\  deterministic: false' "runs/$RUN2/plan.rev1.yaml" > "runs/$RUN2/plan.rev2.yaml"
cp "runs/$RUN2/plan.rev2.yaml" "runs/$RUN2/plan.yaml"
echo "$RUN2"
```

Replay clone:
```bash
go run ./cmd/virmux research replay --run "$RUN2" --only track-1 --label replay-nondet-s06 | tee /tmp/s06.replay.nondet.json
RPN="$(jq -r .run_id /tmp/s06.replay.nondet.json)"
go run ./cmd/virmux research timeline --run "$RPN" | rg 'research.replay.nondet_exception|research.replay.mismatch'
```

Pass:
- replay succeeds
- timeline includes `research.replay.nondet_exception`

### 7.3 Re-reduce after replay
```bash
go run ./cmd/virmux research reduce --run "$RUN_ID" --label reduce-after-replay | tee /tmp/s06.reduce2.out
rg -n '^## Contradictions$|^None\.$' "runs/$RUN_ID/reduce/report.md"
```

Use:
- refresh report after replay/mismatch changes
- triage contradictions via target `mismatch.json` + target `reduce/report.md`

## 8) Adversarial Drills (worth running first)
These test fail-closed behavior that is actually implemented.

### 8.1 Strict parse on execution path
```bash
cp "runs/$RUN_ID/plan.yaml" /tmp/p.yaml
printf '\nbad_key: 1\n' >> "runs/$RUN_ID/plan.yaml"
go run ./cmd/virmux research map --run "$RUN_ID" 2>&1 | tee /tmp/s06.strict.err || true
mv /tmp/p.yaml "runs/$RUN_ID/plan.yaml"
rg -n 'PLAN_SCHEMA_INVALID' /tmp/s06.strict.err
```

Pass:
- execution path rejects unknown key via strict parse (`ParsePlan`)

### 8.2 Zero-progress subset deadlock regression guard
```bash
go test ./internal/skill/research -run TestSchedulerOnlyMissingDependencyDoesNotDeadlock
```

### 8.3 Scheduler infra error propagation guard
```bash
go test ./internal/skill/research -run TestSchedulerReturnsWorkerInfraError
```

### 8.4 `research map --only` parser support
```bash
go run ./cmd/virmux research map --only track-1 --run "$RUN_ID" >/dev/null
```

Pass:
- no unknown-flag parser error (`s06.p0.map.only.missing` closed)

## 9) FDE Ops Pattern (wrapper-target discipline)
### 9.1 Recommended operating loop
- Use `research run` to create the target run.
- Use standalone `replay --run <target>` for reruns.
- Read replay wrapper trace for execution chronology.
- Read target `mismatch.json` + target `reduce/report.md` for result deltas.
- Probe sqlite artifacts/evidence on target run id.

### 9.2 Triage matrix (symptom -> first probe)
| Symptom | First Probe |
|---|---|
| replay command failed | verify `--only` ids in target `plan.yaml` |
| replay “did nothing” | inspect `runs/<target>/map_backup/*` + target `map/*` |
| no contradictions shown | check target `mismatch.json` first |
| timeline empty | ensure you opened wrapper run `trace.ndjson`; filter `research.` |
| artifact rows “missing” | compare target FS vs sqlite; remember wrapper/target split |

### 9.3 Minimal manual evidence pack (if sqlite suspect)
- `runs/<target>/plan.yaml`
- `runs/<target>/map/*.jsonl`
- `runs/<target>/reduce/report.md`
- `runs/<target>/trace.ndjson`
- `runs/<target>/mismatch.json` (if present)

## 10) Cert / Integration Stack (live scripts)
This is the real integration value today: scriptable proof chain for wedge plumbing.

### 10.1 Fast path
```bash
mise run research:cert
test -f tmp/research-cert.ok
```

Truth:
- useful for regression smoke
- not ship declaration (`s06.exit`)

### 10.2 Manual cert path (verbose)
```bash
bash scripts/research_cert.sh
```

If red:
- note failing step number
- rerun that step standalone
- map to `spec-0/06-tasks.jsonl` IDs

### 10.3 SQL cert (freshness-scoped)
```bash
CERT_TS="$(jq -r .cert_ts tmp/research-sql-cert-summary.json)"
bash scripts/research_sql_cert.sh --label-glob 'research-cert-%' --cert-ts "$CERT_TS"
cat tmp/research-sql-cert-summary.json
```

Truth:
- proves fresh cohort counters
- does not prove contract completeness

### 10.4 Docs/canon drift guard
```bash
bash scripts/research_docs_drift.sh --cert-ts "$CERT_TS"
test -f tmp/research-docs-drift.ok
```

### 10.5 Portability roundtrip (real integration)
```bash
bash scripts/research_portability.sh --cert-ts "$CERT_TS"
test -f tmp/research-portability.ok
```

Warning:
- script deletes local run dir + DB row before import as proof; use throwaway labels only

### 10.6 DoD matrix (fresh proof required)
```bash
bash scripts/spec06_dod_matrix.sh --cert-ts "$(jq -r .cert_ts tmp/research-sql-cert-summary.json)"
ls -1 tmp/spec06-dod-matrix.json tmp/spec06-residual-risk.md
```

Pass:
- exits `0` only if all cells have fresh cert-ts-matched proofs

### 10.7 `ship:research` caveat
- `mise run ship:research` now runs `scripts/ship_research.sh` (cert -> DoD cert-ts gate -> cleanup audit -> ship summary)
- lane remains non-authoritative for release while open P1 product/contract gaps persist (`s06.exit`)

## 11) Scenario Bank (copy/paste; truthful)
| ID | Scenario | Command | Pass signal |
|---|---|---|---|
| S01 | composite run | `go run ./cmd/virmux research run --query "$Q"` | JSON `status=ok` |
| S02 | run dir proof | `ls runs/$RUN_ID` | has `plan.yaml,map,reduce,trace.ndjson` |
| S03 | plan inspect | `sed -n '1,220p' runs/$RUN_ID/plan.yaml` | stub tracks visible |
| S04 | map files | `ls runs/$RUN_ID/map/*.jsonl` | per-track files exist |
| S05 | reduce artifacts | `ls runs/$RUN_ID/reduce` | `report.md/slides.md/table.csv` |
| S06 | timeline | `go run ./cmd/virmux research timeline --run "$RUN_ID"` | `research.*` events |
| S07 | plan-first order | `awk ... trace.ndjson` | `plan.created` before map start |
| S08 | evidence count | `sqlite3 ... count(*) from evidence` | `>=1` |
| S09 | row_evidence count | `sqlite3 ... count(*) from row_evidence` | `>=1` |
| S10 | artifact inventory | `sqlite3 ... artifacts by run_id` | plan/map/reduce rows present |
| S11 | split plan | `research plan --query "$Q"` | wrapper `research:plan` ok |
| S12 | split map | `research map --run "$PLAN_RUN"` | target `map/*` written |
| S13 | split map parse | `tail -n1 /tmp/s06.map.out | jq .` | final JSON parses |
| S14 | split reduce | `research reduce --run "$PLAN_RUN"` | target `reduce/*` written |
| S15 | replay happy | `research replay --run "$RUN_ID" --only track-1` | wrapper replay ok |
| S16 | replay invalid selector | `research replay --only nope` | `RERUN_SELECTOR_INVALID` |
| S17 | nondet bypass | clone plan + set `deterministic:false` + replay | `research.replay.nondet_exception` |
| S18 | re-reduce | `research reduce --run "$RUN_ID"` | refreshed report |
| S19 | contradictions section | `rg '^## Contradictions$|^None\\.$' report.md` | always present |
| S20 | strict parse fail | append unknown key to `plan.yaml` + map | `PLAN_SCHEMA_INVALID` |
| S21 | deadlock guard | `go test ...DoesNotDeadlock` | PASS |
| S22 | err propagation guard | `go test ...WorkerInfraError` | PASS |
| S23 | map `--only` flag | `research map --only track-1 --run "$RUN_ID"` | parser accepts |
| S24 | cache behavior demo | `grep '"cache":"hit"' track-wide.jsonl` | optional hit rows |
| S25 | evidence quality probe | `sqlite3 ... select claim,quote_span,confidence` | rows; placeholder-ish |
| S26 | cert fast | `mise run research:cert` | `tmp/research-cert.ok` |
| S27 | SQL cert | `CERT_TS=...; bash scripts/research_sql_cert.sh --cert-ts "$CERT_TS"` | summary json + ok |
| S28 | docs drift | `bash scripts/research_docs_drift.sh --cert-ts "$CERT_TS"` | ok marker |
| S29 | portability | `bash scripts/research_portability.sh --cert-ts "$CERT_TS"` | ok marker |
| S30 | DoD matrix | `bash scripts/spec06_dod_matrix.sh --cert-ts ...` | matrix + residual risk |

## 12) What To Record (QA/FDE discipline)
- Tag every observed pass/fail to `spec-0/06-tasks.jsonl` IDs.
- Add new task row if you find a new failure class.
- Do not file prose-only “seems wrong” bugs without command + artifact/sql proof.

High-value open IDs to keep referencing:
- `s06.exit`
- `s06.p1.plan.stub`
- `s06.p1.plan.schema.underfit`
- `s06.p1.timeout.budget.retry.absent`
- `s06.p1.worker.contract.missing`
- `s06.p1.cache.key.wrong`
- `s06.p1.coverage.underimplemented`
- `s06.p1.evidence.weak`
- `s06.p1.uncited.policy.partial`
- `s06.p1.replay.parity.shallow`
- `s06.p1.guard.matrix.missing.tests`
- `s06.p1.research.scripts.untested`
- `s06.p1.shiplane.weak`

## 13) Executive Narrative (truthful, short)
Virmux currently demonstrates a contract-first research wedge on the existing run/trace/sqlite/export substrate: it persists a plan artifact, runs dependency-aware map tracks (including stubbed wide-grid coverage behavior), reduces outputs into deterministic report artifacts, and supports selective replay with mismatch/nondet trace events. The lane is operational for demos/QA/FDE and cert smoke, but remains unshipped due open P1 planner/schema/budget/cache/evidence/parity/guard/ship-lane gaps.

## 14) If You Need Ship-Grade Confidence Next
1. Replace planner stub with real compiler + deeper schema/budget validation.
2. Implement timeout/budget/retry enforcement with typed failures.
3. Fix cache key + coverage ledger semantics to spec contract.
4. Harden evidence extraction/linking and explicit uncited row model.
5. Extend replay parity to reduce artifact hashes.
6. Materialize real guard-matrix tests + script tests.
7. Build isolated `ship:research` lane with cleanup audit + freshness-scoped cert orchestration.

## 15) Compounding Law Reminder
Any behavior edits in `cmd/`, `internal/`, `scripts/`, `mise.toml`, `vm/` while following this drillbook must ship both:
- one executable guard
- one learning capture row
