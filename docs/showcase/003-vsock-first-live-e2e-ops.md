# Showcase 003: Vsock-First Live E2E Ops (Evidence > Stdout)

Goal: real value in `15-60m` via repeatable proof, not terminal vibes.

Decisive lane: `mise run ship:core`

## 0) Non-negotiables (break any => stop)
- Host: Ubuntu 24.04 bare-metal, `/dev/kvm` rw.
- Runtime: Firecracker via go-sdk only.
- Image selector: `vm/images.lock`; cache: `.cache/ghostfleet/images/<sha>/` write-once.
- Rootfs RO (`rootflags=noload`); mutable bytes only `volumes/<agent>.ext4` (`/dev/virmux-data` in guest, `/data` in tools).
- Mandatory VM boundaries: `vm.boot.started`,`vm.exec.injected`,`vm.exit.observed`.
- Vsock tool path contract: `CONNECT/OK` + `READY v0 tools=...` + framed RPC.
- Evidence order invariant: trace emit first, sqlite insert second.
- Canonical trace: `runs/<id>/trace.ndjson` (`trace.jsonl` is compat symlink only).
- Resume invariant: one snapshot attempt; any fault => `StopVMM+Wait` then cold fallback.
- Every `vm:resume` terminal payload must include non-null `resume_mode`,`resume_source`,`resume_error`.
- Cleanup invariant: zero orphan `firecracker`, zero stale `firecracker.sock`/`vsock*.sock`/`*.fifo`, zero leaked `virmux-tap*`.

## 1) 20m Value Loop (PO) - boot -> tool -> persist -> resume -> replay
Use unique cohort IDs every run.

```bash
set -euo pipefail
cd /home/haris/projects/virmux
TAG="po03-$(date -u +%Y%m%dT%H%M%SZ)"
AG="${TAG}-a"

mise run doctor
mise run image:stamp

go run ./cmd/virmux vm-smoke  --agent "$AG" --label "$TAG-smoke"

go run ./cmd/virmux vm-run    --agent "$AG" --label "$TAG-write" --vsock-cid 3 \
  --tool fs.write --tool-args-json '{"path":"/data/po03.txt","bytes":"po03"}'

go run ./cmd/virmux vm-run    --agent "$AG" --label "$TAG-read" --vsock-cid 3 \
  --tool fs.read --tool-args-json '{"path":"/data/po03.txt"}'

go run ./cmd/virmux vm-zygote --agent "$AG" --label "$TAG-zygote"
go run ./cmd/virmux vm-resume --agent "$AG" --label "$TAG-resume"

RID="$(sqlite3 runs/virmux.sqlite "select id from runs where label='$TAG-read' order by started_at desc limit 1;")"
go run ./cmd/virmux export --run-id "$RID" --out "runs/$RID.tar.zst"
go run ./cmd/virmux import --bundle "runs/$RID.tar.zst" --db runs/imported.sqlite --runs-dir runs/imported

mise run vm:cleanup:audit
```

Proof (run all):

```bash
sqlite3 runs/virmux.sqlite "
select task,label,agent_id,status,boot_ms,resume_ms
from runs where label like '$TAG-%' order by started_at;"

sqlite3 runs/virmux.sqlite "
select e.kind
from events e join runs r on r.id=e.run_id
where r.label='$TAG-read'
order by e.id;"

sqlite3 runs/virmux.sqlite "
select seq,tool,req_id,input_hash,output_hash,error_code
from tool_calls
where run_id='$RID'
order by seq;"

sqlite3 runs/virmux.sqlite "
select path,sha256,bytes
from artifacts
where run_id='$RID'
order by id;"

sqlite3 runs/virmux.sqlite "
select json_extract(payload,'$.resume_mode'),
       json_extract(payload,'$.resume_source'),
       json_extract(payload,'$.resume_error')
from events
where run_id=(select id from runs where label='$TAG-resume' order by started_at desc limit 1)
  and kind='run.finished';"

sqlite3 runs/imported.sqlite "
select id,task,label,status,source_bundle
from runs
where id='$RID';"
```

Hard pass:
- all `$TAG-*` runs `status='ok'`
- `$TAG-read` has `vm.guest.ready` then `vm.tool.result`
- `tool_calls` row exists with non-empty `input_hash`,`output_hash`
- `$TAG-resume` has `resume_mode='snapshot_resume'`
- import row exists and `source_bundle` non-empty
- `mise run vm:cleanup:audit` returns OK

## 2) QA Ship/No-Ship (<=60m)
Default:

```bash
mise run ship:core
```

Must print: `ship:core: OK cert_tag=qa-cert-...`

If red, run matrix in order:

```bash
mise run ci:fast
mise run doctor:test:missing-artifact
mise run doctor:test:socket-probe
mise run image:test:checksum-mismatch
mise run vm:boot:contract
mise run vm:vsock:chaos
mise run vm:test:tool-shell ::: vm:test:tool-fs ::: vm:test:no-leak ::: vm:test:tool-http
mise run vm:test:agent-persistence
mise run vm:test:resume-fallback-nosnap
mise run vm:test:resume-snapshot-success
mise run vm:test:export-roundtrip
mise run vm:test:watchdog
mise run vm:test:crash-partial-export
mise run bench:snapshot 5
mise run trace:validate ::: db:check
VIRMUX_CERT_LABEL_GLOB='qa-cert-%' ./scripts/sql_cert_contract.sh
mise run vm:cleanup:audit
```

Perf gate (reject otherwise):
- `total_samples==iterations`
- `snapshot_resume_count==iterations`
- `fallback_count==0`
- `p50_ms<=3500`
- `p95_ms<=6000`

## 3) FDE Fast Loops (copy/paste drills)

```bash
set -euo pipefail
TAG="fde03-$(date -u +%Y%m%dT%H%M%SZ)"
AG="${TAG}-a"
```

| ID | Intent | Command | Expected |
|---|---|---|---|
| F01 | shell tool success | `go run ./cmd/virmux vm-run --agent "$AG" --label "$TAG-shell" --vsock-cid 3 --tool shell.exec --cmd 'echo hi-fde'` | `vm.tool.result.ok=true` |
| F02 | fs write | `go run ./cmd/virmux vm-run --agent "$AG" --label "$TAG-fsw" --vsock-cid 3 --tool fs.write --tool-args-json '{"path":"/data/fde.txt","bytes":"fde"}'` | run `ok` |
| F03 | fs read | `go run ./cmd/virmux vm-run --agent "$AG" --label "$TAG-fsr" --vsock-cid 3 --tool fs.read --tool-args-json '{"path":"/data/fde.txt"}'` | `data.bytes=="fde"` |
| F04 | deny outside /data | `go run ./cmd/virmux vm-run --agent "$AG" --label "$TAG-deny-path" --vsock-cid 3 --tool fs.write --tool-args-json '{"path":"/etc/pwn","bytes":"x"}' || true` | result `error.code=="DENIED"` |
| F05 | allowlist deny | `go run ./cmd/virmux vm-run --agent "$AG" --label "$TAG-deny-allow" --vsock-cid 3 --tool shell.exec --allow fs.read --cmd 'echo blocked' || true` | run `failed`, `error_code='DENIED'` |
| F06 | timeout typing | `go run ./cmd/virmux vm-run --agent "$AG" --label "$TAG-timeout" --vsock-cid 3 --tool shell.exec --tool-args-json '{"cmd":"sleep 2","timeout_ms":10}' || true` | `error_code='TIMEOUT'` |
| F07 | snapshot fast-path | `go run ./cmd/virmux vm-zygote --agent "$AG" --label "$TAG-zygote" && go run ./cmd/virmux vm-resume --agent "$AG" --label "$TAG-resume-ok"` | `resume_mode='snapshot_resume'` |
| F08 | forced fallback | `go run ./cmd/virmux vm-resume --agent "$AG" --label "$TAG-resume-bad" --mem-path /tmp/miss.mem --state-path /tmp/miss.state` | `status=ok`, `resume_mode='fallback_cold_boot'`, `resume_error!=''` |
| F09 | export replay | `RID=$(sqlite3 runs/virmux.sqlite "select id from runs where label='$TAG-fsr' order by started_at desc limit 1;"); go run ./cmd/virmux export --run-id "$RID" --out "runs/$RID.tar.zst"; go run ./cmd/virmux import --bundle "runs/$RID.tar.zst" --db runs/imported-fde.sqlite --runs-dir runs/imported-fde` | imported run present |

Quick forensic pack:

```bash
RID="$(sqlite3 runs/virmux.sqlite "select id from runs where label like '$TAG-%' order by started_at desc limit 1;")"
sqlite3 runs/virmux.sqlite "select id,task,label,status,error_code,boot_ms,resume_ms from runs where label like '$TAG-%' order by started_at;"
sqlite3 runs/virmux.sqlite "select kind,count(*) from events where run_id='$RID' group by kind order by kind;"
sqlite3 runs/virmux.sqlite "select seq,tool,input_hash,output_hash,stdout_ref,stderr_ref,error_code from tool_calls where run_id='$RID' order by seq;"
sqlite3 runs/virmux.sqlite "select path,sha256,bytes from artifacts where run_id='$RID' order by id;"
```

## 4) Live Integrations (optional lanes, isolated)
Core contract does not depend on these unless explicitly promoted.

```bash
mise run slack:recv
sqlite3 runs/virmux.sqlite "select count(*) as slack_events from slack_events;"

mise run vm:net:probe
cat runs/.last-vm-net-probe

mise run pw:install
mise run pw:install:status
mise run pw:smoke
cat tmp/pw-install-status.json
```

Interpretation:
- `slack:recv` must increase `slack_events`
- `vm:net:probe` may `skip` on unprivileged hosts (not core fail)
- `pw:*` validates host browser lane only (not VM contract)

## 5) Evidence-first SQL Contract Checks

```bash
# boundary events present on VM lanes
sqlite3 runs/virmux.sqlite "
select r.label,
sum(e.kind='vm.boot.started') as boot_started,
sum(e.kind='vm.exec.injected') as exec_injected,
sum(e.kind='vm.exit.observed') as exit_observed
from runs r join events e on e.run_id=r.id
where r.task in ('vm:smoke','vm:run','vm:zygote','vm:resume')
group by r.id
having boot_started<1 or exec_injected<1 or exit_observed<1;"

# loss counters must stay zero in cert cohort
sqlite3 runs/virmux.sqlite "
select count(*)
from events e join runs r on r.id=e.run_id
where e.kind='run.finished'
  and r.label like 'qa-cert-%'
  and (cast(json_extract(e.payload,'$.lost_logs') as integer)>0
    or cast(json_extract(e.payload,'$.lost_metrics') as integer)>0);"

# resume telemetry non-null in cert cohort
sqlite3 runs/virmux.sqlite "
select count(*)
from events e join runs r on r.id=e.run_id
where e.kind='run.finished' and r.task='vm:resume' and r.label like 'qa-cert-%'
  and (json_extract(e.payload,'$.resume_mode') is null
    or json_extract(e.payload,'$.resume_source') is null
    or json_extract(e.payload,'$.resume_error') is null);"
```

All three queries must return `0`.

## 6) Scenario Bank (dense copy/paste catalog)
| ID | Scenario | Command | Pass condition |
|---|---|---|---|
| S01 | host gate | `mise run doctor` | PASS |
| S02 | lock stamp | `mise run image:stamp` | `vm/images.lock` updated |
| S03 | smoke baseline | `mise run vm:smoke -- --label s03` | run `ok` + markers |
| S04 | boot contract | `mise run vm:boot:contract` | `tmp/vm-boot-contract.ok` |
| S05 | vsock chaos | `mise run vm:vsock:chaos` | `tmp/vm-vsock-chaos.ok` |
| S06 | shell tool | `mise run vm:test:tool-shell` | `tmp/vm-test-tool-shell.ok` |
| S07 | fs tool | `mise run vm:test:tool-fs` | `tmp/vm-test-tool-fs.ok` |
| S08 | deny leak | `mise run vm:test:no-leak` | `tmp/vm-test-no-leak.ok` |
| S09 | http tool contract | `mise run vm:test:tool-http` | `tmp/vm-test-tool-http.ok` |
| S10 | agent persistence | `mise run vm:test:agent-persistence` | guard OK |
| S11 | resume fallback guard | `mise run vm:test:resume-fallback-nosnap` | guard OK |
| S12 | resume snapshot guard | `mise run vm:test:resume-snapshot-success` | guard OK |
| S13 | trace validator | `mise run trace:validate` | `.trace-validate.ok` |
| S14 | db invariants | `mise run db:check` | `.db-check.ok` |
| S15 | export roundtrip | `mise run vm:test:export-roundtrip` | guard OK |
| S16 | watchdog | `mise run vm:test:watchdog` | guard OK |
| S17 | crash partial export | `mise run vm:test:crash-partial-export` | guard OK |
| S18 | perf bench | `mise run bench:snapshot 5` | SLO + pure snapshot cohort |
| S19 | cleanup audit | `mise run vm:cleanup:audit` | `tmp/cleanup-audit.ok` |
| S20 | SQL contract | `VIRMUX_CERT_LABEL_GLOB='qa-cert-%' ./scripts/sql_cert_contract.sh` | script OK |
| S21 | deterministic export | `go run ./cmd/virmux export --run-id <rid> --out runs/<rid>.tar.zst` | bundle created |
| S22 | safe import | `go run ./cmd/virmux import --bundle runs/<rid>.tar.zst --db runs/import.sqlite --runs-dir runs/imported` | imported row + `source_bundle` |
| S23 | slack ingest | `mise run slack:recv` | `slack_events` increments |
| S24 | net probe optional | `mise run vm:net:probe` | `ok` or explicit `skip` |
| S25 | playwright optional | `mise run pw:install && mise run pw:smoke` | `tmp/pw-smoke.ok` |
| S26 | full oracle | `mise run ship:core` | `ship:core: OK cert_tag=...` |

## 7) Triage Map (symptom -> first probe)
| Symptom | Probe | Likely class | Action |
|---|---|---|---|
| doctor fail | `mise run doctor` + `/dev/kvm` perms | host floor broken | fix host first |
| no `vm.guest.ready` | run trace + events for run_id | READY mismatch / transport break | inspect `READY v0 tools=` line + `vm:vsock:chaos` |
| connect retries exhaust | `tmp/vsock-chaos-report.json` + `handshake_ms` | early race / ack/read path | rerun G2; inspect `connect_attempts` |
| tool success claim but no refs | `tool_calls` + `artifacts` rows | ref hydration/materialization regression | fail run; do not trust stdout |
| resume unexpected fallback | `run.finished` `resume_*` keys | snapshot resolve/load/wait fault | inspect `resume_source`,`resume_error` |
| stale socket/fifo/tap | `mise run vm:cleanup:audit` | teardown drift | stop ship until clean |
| DB/trace disagreement | `mise run trace:validate ::: db:check` | schema/hash/order drift | fix evidence plane before cert |

## 8) Acceptance Definition
Ship only when one fresh cohort proves all:
- G0 host baseline
- G1 boot telemetry truth (`lost_*==0`)
- G2 vsock chaos + bounded handshake
- G3 tool policy (shell/fs/http/no-leak)
- G4 trace+db+export determinism
- G5 watchdog + crash partial export + cleanup audit
- cohort-scoped SQL cert
- perf gate

One-liner oracle:

```bash
mise run ship:core
```
