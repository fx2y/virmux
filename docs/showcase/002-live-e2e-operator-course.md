# Showcase 002: Live E2E Operator Course (Contract > Convenience)

Purpose: extract real value fast from current `virmux` implementation without violating determinism.

Rule zero: never certify from stdout. Certify from `runs/virmux.sqlite` + `runs/*/trace.jsonl` + artifacts rows.

## 0) Non-negotiables (read once, obey forever)
- Host: Ubuntu 24.04 bare-metal, `/dev/kvm` rw, Firecracker via go-sdk only.
- Input immutability: selected image only from `vm/images.lock`; cache under `.cache/ghostfleet/images/<sha>/` is write-once.
- State plane: mutable bytes only in `volumes/<agent>.ext4` mounted at `/dev/virmux-data`; rootfs is RO (`rootflags=noload`).
- Resume contract: try snapshot once; any resolve/load/wait fault => deterministic cold fallback.
- Resume telemetry contract: every `vm:resume` terminal event has non-null `resume_mode`,`resume_source`,`resume_error`.
- Boundary events required for VM triage: `vm.boot.started`,`vm.exec.injected`,`vm.exit.observed`.
- Data-plane order invariant: trace emit first, sqlite insert second.
- Cleanup invariant: no orphan `firecracker`, no stale `runs/**/firecracker.sock`, no leaked `virmux-tap*`.

## 1) Bootstrap once per session
```bash
set -euo pipefail
cd /home/haris/projects/virmux
mise run doctor
mise run image:stamp
```
Pass signals:
- `doctor: PASS`
- `vm/images.lock` non-empty
- lock-selected artifacts exist and lock-selected `firecracker` executable

## 2) 15-minute PO value loop (boot -> persist -> resume -> evidence)
Use unique identity to avoid cross-run ambiguity.

```bash
set -euo pipefail
AG="poA-$(date -u +%Y%m%dT%H%M%SZ)"

go run ./cmd/virmux vm-smoke  --agent "$AG" --label po-smoke

go run ./cmd/virmux vm-run    --agent "$AG" --label po-write \
  --cmd 'echo po >/dev/virmux-data/demo.txt; sync'

go run ./cmd/virmux vm-run    --agent "$AG" --label po-read \
  --cmd 'cat /dev/virmux-data/demo.txt'

go run ./cmd/virmux vm-zygote --agent "$AG" --label po-zygote

go run ./cmd/virmux vm-resume --agent "$AG" --label po-resume
```

Evidence checklist:
```bash
sqlite3 runs/virmux.sqlite "
select id,task,label,agent_id,status,boot_ms,resume_ms
from runs
where label like 'po-%'
order by started_at;"

sqlite3 runs/virmux.sqlite "
select json_extract(payload,'$.resume_mode'),
       json_extract(payload,'$.resume_source'),
       json_extract(payload,'$.resume_error')
from events
where run_id=(select id from runs where label='po-resume' order by started_at desc limit 1)
  and kind='run.finished';"

RID=$(sqlite3 runs/virmux.sqlite "select id from runs where label='po-read' order by started_at desc limit 1;")
sqlite3 runs/virmux.sqlite "select path,sha256,bytes from artifacts where run_id='$RID' order by id;"
```
Hard pass:
- `po-read` serial contains `po`
- `po-resume` `resume_mode='snapshot_resume'`
- artifacts rows exist for serial/stderr/trace

## 3) 45-minute QA cert loop (ship/no-ship)
Run this exact order. Any red => stop-ship.

```bash
set -euo pipefail
mise run ci:fast
mise run doctor:test:missing-artifact
mise run doctor:test:socket-probe
mise run image:test:checksum-mismatch
mise run vm:smoke
mise run vm:test:agent-persistence
mise run vm:test:resume-fallback-nosnap
mise run vm:test:resume-snapshot-success
mise run vm:zygote -- --label qa-cert-zygote
mise run vm:resume -- --label qa-cert-resume
mise run bench:snapshot
mise run trace:validate ::: db:check ::: qa:sql-contract ::: vm:cleanup:audit
```

Mandatory SQL cert assertions (cohort-scoped; append-only safe):
```bash
sqlite3 runs/virmux.sqlite "
select count(*)
from runs
where task='vm:resume' and status='ok' and label like 'qa-cert-%';"

sqlite3 runs/virmux.sqlite "
select count(*)
from events e
join runs r on r.id=e.run_id
where e.kind='run.finished'
  and r.task='vm:resume'
  and r.label like 'qa-cert-%'
  and (
    json_extract(e.payload,'$.resume_mode') is null or
    json_extract(e.payload,'$.resume_source') is null or
    json_extract(e.payload,'$.resume_error') is null
  );"
```
Expected: first query `>0`; second query `0`.

Perf gate (`runs/bench-snapshot-summary.json`) must satisfy:
- `total_samples==iterations`
- `snapshot_resume_count>=1`
- `p50_ms<=3500`
- `p95_ms<=6000`

## 4) FDE rapid loops (experiment, abuse, verify)
### 4.1 Persist/recall loop
```bash
TAG="fde-$(date -u +%Y%m%dT%H%M%SZ)"
AG="${TAG}-A"

go run ./cmd/virmux vm-run --agent "$AG" --label "${TAG}-write" \
  --cmd 'echo fde >/dev/virmux-data/fde.txt; sync'

go run ./cmd/virmux vm-run --agent "$AG" --label "${TAG}-read" \
  --cmd 'cat /dev/virmux-data/fde.txt'
```

### 4.2 Resume fast-path vs fallback safety-net
```bash
TAG="fde-$(date -u +%Y%m%dT%H%M%SZ)"
AG="${TAG}-R"

go run ./cmd/virmux vm-zygote --agent "$AG" --label "${TAG}-zygote"

go run ./cmd/virmux vm-resume --agent "$AG" --label "${TAG}-resume-ok"

go run ./cmd/virmux vm-resume --agent "$AG" --label "${TAG}-resume-bad" \
  --mem-path /tmp/missing.mem --state-path /tmp/missing.state
```
Expect:
- `${TAG}-resume-ok` => `resume_mode=snapshot_resume`
- `${TAG}-resume-bad` => `status=ok`, `resume_mode=fallback_cold_boot`, `resume_source=snapshot_resume_error`, `resume_error!=''`

### 4.3 Experimental vsock lane (non-core)
```bash
TAG="fde-$(date -u +%Y%m%dT%H%M%SZ)"
AG="${TAG}-V"

go run ./cmd/virmux vm-run --agent "$AG" --label "${TAG}-vsock" \
  --vsock-cid 52 --cmd 'echo vsock-probe'
```
Verify metadata artifact row (non-regular path policy):
```bash
RID=$(sqlite3 runs/virmux.sqlite "select id from runs where label='${TAG}-vsock' order by started_at desc limit 1;")
sqlite3 runs/virmux.sqlite "select path,sha256,bytes from artifacts where run_id='$RID' and path like '%vsock%';"
```
Expect `sha256` prefixed `meta:` and `bytes=0` for socket endpoints.

## 5) Scenario bank (copy/paste catalog)
All scenarios produce value only if you read evidence, not terminal noise.

| ID | Scenario | Command | Pass condition |
|---|---|---|---|
| S01 | host gate | `mise run doctor` | `doctor: PASS` |
| S02 | immutable image pin | `mise run image:stamp` | `vm/images.lock` updated; artifacts resolve |
| S03 | smoke baseline | `mise run vm:smoke -- --label s03` | run `status=ok`; markers `Linux`+`ok` |
| S04 | custom guest cmd | `go run ./cmd/virmux vm-run --label s04 --cmd 'uname -a'` | run `status=ok`; serial has uname |
| S05 | per-agent write | `go run ./cmd/virmux vm-run --agent A --label s05w --cmd 'echo hi >/dev/virmux-data/hi.txt; sync'` | run `status=ok` |
| S06 | same-agent read | `go run ./cmd/virmux vm-run --agent A --label s06r --cmd 'cat /dev/virmux-data/hi.txt'` | serial has `hi` |
| S07 | cross-agent isolation | `go run ./cmd/virmux vm-run --agent B --label s07 --cmd 'test ! -f /dev/virmux-data/hi.txt'` | run `status=ok` |
| S08 | zygote produce snapshot | `mise run vm:zygote -- --agent S8 --label s08` | snapshot files + latest metadata |
| S09 | resume fast-path | `mise run vm:resume -- --agent S8 --label s09` | `resume_mode=snapshot_resume` |
| S10 | nosnapshot fallback | `mise run vm:test:resume-fallback-nosnap` | guard green |
| S11 | bad mem/state fallback | `go run ./cmd/virmux vm-resume --agent S8 --label s11 --mem-path /tmp/x --state-path /tmp/y` | fallback telemetry present |
| S12 | boundary events | SQL on last run kinds | contains boot/injected/exit |
| S13 | trace schema | `mise run trace:validate` | `.trace-validate.ok` |
| S14 | db invariants | `mise run db:check` | WAL+FK+indexes pass |
| S15 | artifact registry | SQL artifacts by run_id | serial/stderr/trace rows exist |
| S16 | checksum drift guard | `mise run image:test:checksum-mismatch` | guard green (fails on mismatch, test passes) |
| S17 | cleanup invariant | `mise run vm:cleanup:audit` | `.cleanup-audit.ok` |
| S18 | perf budget | `./scripts/bench_snapshot.sh 5` | budget + sample constraints pass |
| S19 | optional tap probe | `mise run vm:net:probe` | `ok` if root, else explicit `skipped` |
| S20 | slack fixture ingest | `mise run slack:recv` | `slack_events` rows increase |
| S21 | playwright host lane | `mise run pw:install && mise run pw:smoke` | browser smoke green |

## 6) SQL-first forensic pack
Set a target run once, then interrogate everything from sqlite.

```bash
RID=$(sqlite3 runs/virmux.sqlite "select id from runs order by started_at desc limit 1;")
```

### 6.1 Runtime identity + lineage
```bash
sqlite3 runs/virmux.sqlite "
select id,task,label,agent_id,status,image_sha,kernel_sha,rootfs_sha,snapshot_id,cost_est,started_at,finished_at
from runs
where id='$RID';"
```

### 6.2 Event topology
```bash
sqlite3 runs/virmux.sqlite "
select kind,count(*)
from events
where run_id='$RID'
group by kind
order by kind;"
```
Must include boundary kinds for VM tasks.

### 6.3 Resume truth (authoritative)
```bash
sqlite3 runs/virmux.sqlite "
select json_extract(payload,'$.status') status,
       json_extract(payload,'$.resume_mode') resume_mode,
       json_extract(payload,'$.resume_source') resume_source,
       json_extract(payload,'$.resume_error') resume_error,
       json_extract(payload,'$.mem_path') mem_path,
       json_extract(payload,'$.state_path') state_path
from events
where run_id='$RID' and kind='run.finished';"
```

### 6.4 Artifact inventory (no ad-hoc scans first)
```bash
sqlite3 runs/virmux.sqlite "
select path,sha256,bytes
from artifacts
where run_id='$RID'
order by id;"
```
Policy reminder:
- regular files => content hash
- sockets/fifo/symlink/dir => metadata row (`sha256=meta:*`,`bytes=0`)

## 7) Failure-to-probe map (opinionated triage)
| Symptom | First probe | Likely class | Action |
|---|---|---|---|
| doctor `/dev/kvm` fail | `ls -l /dev/kvm; groups` | host perms | fix host/user; rerun doctor |
| resume all fallback | `select resume_mode,count(*) ...` | snapshot path dead | run zygote same agent; inspect resume_error/source |
| resume 400 unsupported | inspect run.finished + vm start path | snapshot wiring regression | ensure snapshot handler path active; keep fallback safety |
| smoke misses markers | check `serial.log` for `__cmd_end__`,`Linux`,`ok` | cmd inject/boot path | inspect boundary events, serial chunks |
| trace/db mismatch | `mise run trace:validate ::: db:check` | dual-write or schema drift | fix emitter/store order or schema/indexes |
| stale sockets/procs | `mise run vm:cleanup:audit` + `cat tmp/cleanup-audit.log` | lifecycle cleanup regression | enforce StopVMM+Wait+sock remove |
| false-red SQL cert | inspect label scope | append-only history contamination | cohort-scope cert labels (`qa-cert-%`) |

## 8) Optional live integration lanes (isolated from core VM contract)
- `vm:net:probe`: privileged-only tap setup probe; skip is valid when unprivileged.
- `slack:recv`: local HTTP receiver + fixture replay; persists to `slack_events`.
- `pw:smoke`: host browser sanity for UI lane tooling.

Run all:
```bash
mise run vm:net:probe
mise run slack:recv
mise run pw:install
mise run pw:install:status
mise run pw:smoke

sqlite3 runs/virmux.sqlite "select count(*) from slack_events;"
cat runs/.last-vm-net-probe
cat tmp/pw-install-status.json
```
Interpretation rule: optional lane red does not redefine core VM contract unless release explicitly requires it.

## 9) Final ship command (full core lane)
```bash
mise run ship:core
```
Ship only if all green and evidence exists:
- `runs/virmux.sqlite`
- `runs/*/trace.jsonl`
- `runs/bench-snapshot-summary.json`
- `tmp/doctor.ok`
- `tmp/cleanup-audit.log`
- `tmp/ship-core-summary.json`
