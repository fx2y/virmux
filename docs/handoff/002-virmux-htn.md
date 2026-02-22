# Virmux HTN Handoff: Firecracker + RunSpec + Artifacts (Cycle 0..4)

Expert-grade, ultra-terse procedural guide for Virmux core (HTN 02).

## 1. The Stack (L0-L5)
- **L0 Host:** Ubuntu 24.04 bare-metal. `/dev/kvm` rw. `firecracker-go-sdk` only.
- **L1 Image:** `vm/images.lock` pins image SHA. Immutable cache: `.cache/ghostfleet/images/<sha>`.
- **L2 Run:** `vm-run|smoke|zygote|resume`. Serial-first (ttyS0). `poweroff -f` terminal.
- **L3 State:** `agents/<id>.json` (meta) + `volumes/<id>.ext4` (durable `/dev/vdb` -> `/dev/virmux-data`).
- **L4 Data:** `runs/virmux.sqlite` + `runs/<id>/trace.jsonl`. Artifact registry (hashed files + meta-only sockets).
- **L5 Ops:** `bench:snapshot` SLOs. `cleanup:audit` leak probes.

## 2. Hard Laws (Contracts)
1. **Resume Invariant:** Try snapshot once. Fault (metadata/API/load) -> **Mandatory** cold-boot fallback.
2. **Telemetry Totality:** `run.finished` for `vm:resume` MUST have `resume_mode`, `resume_source`, `resume_error`.
3. **Immutability:** Rootfs is RO (`rootflags=noload`). Mutable bytes ONLY on `/dev/virmux-data`.
4. **Data Integrity:** Dual-write `trace emit -> sqlite insert`. Trace is append-only.
5. **Artifact Policy:** Hash regular files. Sockets/FIFOs = `meta:*` rows.

## 3. Developer Loop (15-Min Showcase)
Standard dev-cycle: Build -> Boot -> Persist -> Resume -> Query.

```bash
# 0. Prep
mise run doctor image:stamp

# 1. Smoke (Generic)
mise run vm:smoke -- --label dev-smoke

# 2. Persist (Agent A)
go run ./cmd/virmux vm-run --agent A --label dev-w --cmd 'echo hi >/dev/virmux-data/f.txt; sync'
go run ./cmd/virmux vm-run --agent A --label dev-r --cmd 'cat /dev/virmux-data/f.txt' # Expect 'hi'

# 3. Zygote + Resume (Fast path)
mise run vm:zygote -- --agent R --label dev-zygote
mise run vm:resume -- --agent R --label dev-resume # Expect resume_mode=snapshot_resume

# 4. Fallback (Fault tolerance)
go run ./cmd/virmux vm-resume --agent R --label dev-fallback --mem-path /tmp/junk # Expect status=ok, resume_mode=fallback_cold_boot
```

## 4. Triage & Forensics (SQL)
Forget `ls`. Use the DB.

```sql
-- Recent runs overview
SELECT id, task, status, boot_ms, resume_ms, cost_est FROM runs ORDER BY started_at DESC LIMIT 10;

-- Resume telemetry audit
SELECT 
  json_extract(payload, '$.resume_mode') as mode,
  json_extract(payload, '$.resume_source') as src,
  json_extract(payload, '$.resume_error') as err
FROM events WHERE kind='run.finished' AND run_id IN (SELECT id FROM runs WHERE task='vm:resume');

-- Artifact inventory for a run
SELECT path, sha256, bytes FROM artifacts WHERE run_id='<RUN_ID>';

-- Event sequence (Phase triage)
SELECT kind, payload FROM events WHERE run_id='<RUN_ID>' ORDER BY id;
```

## 5. QA Certification Path (The Stop-Ship Rules)
Green-light means ALL pass. No exceptions.

1. **Fast CI:** `mise run ci:fast` (fmt/lint/unit).
2. **Integrity:** `mise run trace:validate ::: db:check ::: qa:sql-contract`.
3. **Persistence:** `mise run vm:test:agent-persistence`.
4. **Resume Guards:** `mise run vm:test:resume-fallback-nosnap ::: vm:test:resume-snapshot-success`.
5. **Perf Gate:** `mise run bench:snapshot 5`.
   - `snapshot_resume_count >= 1`.
   - `p50 <= 3500ms`, `p95 <= 6000ms`.
6. **Leak Audit:** `mise run vm:cleanup:audit`. Zero orphan procs/sockets/taps.

## 6. Current Observations (Cycle 4 Closure)
- **Snapshot Resume:** FIXED via `WithSnapshot` SDK opt. No more illegal `PATCH` 400s.
- **Image Pipeline:** FIXED. Source bytes pinned in `manifest.json`. Cache-key includes source hashes.
- **Vsock Seam:** Experimental. Added via `--vsock-cid`. Artifacts store `meta:socket` for host UDS.
- **DB Migrations:** Additive only. `cost_est` and `snapshot_id` are first-class columns.

## 7. Expert Cheat Sheet
- **Guest Mounting:** `mount /dev/vdb /dev/virmux-data` (handled by internal serial script).
- **Run ID:** `UnixNano-TaskName`. Unique, monotonic.
- **Logs:** `runs/<id>/serial.log` (Guest), `firecracker.stderr.log` (VMM).
- **Socket Path:** `runs/<id>/firecracker.sock`. Cleaned up on session close.
