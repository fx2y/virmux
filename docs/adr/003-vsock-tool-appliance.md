# ADR 003: Vsock-first Tool Appliance (Spec-03)

**Status:** Accepted  
**Context:** Spec-0/03 (HTN Architecture)  
**Vision:** MicroVM as a stateless tool-exec appliance; host as orchestrator+recorder.

## 1. Core Mandates
- **Contract > Convenience:** Determinism is the only product.
- **Evidence > Stdout:** If it's not in `trace.ndjson` or SQLite, it didn't happen.
- **Vsock-first:** Retire serial for data-plane; use `virtio-vsock` for all tool I/O.
- **Fail Closed:** Violations (lost logs, checksum drift, path escape) are hard stop-ships.

## 2. Transport Architecture (L3)
Replace unreliable serial stream with framed RPC over AF_VSOCK.

- **Handshake:** Host `CONNECT <port>
` -> Guest `OK <port>
`.
- **Readiness:** Guest emits `READY v0 tools=shell,fs,http
` on first connect.
- **Framing:** `u32le length` + `JSON payload`.
- **Mux:** Synchronous `req_id` mapping per tool call.
- **Resilience:** Bounded retry on `CONNECT` to dodge guest init race; fail fast on `ErrConnectAck`.

```text
Host (virmux)                     Guest (agentd)
      |--- UNIX:v.sock:CONNECT 52 --->|
      |<--------- OK 52 --------------|
      |<-- READY v0 tools=shell,fs ---|
      |--- [len]{"req":1,"tool":..} ->|
      |<-- [len]{"req":1,"ok":true} --|
```

## 3. Data Plane & Evidence (L4)
Dual-write strategy ensures queryability and stream integrity.

- **Order:** `trace emit -> sqlite insert`. Never reverse.
- **Trace:** Append-only `trace.ndjson` (compatible `trace.jsonl` symlink).
- **SQLite:** WAL+FK required. Schema: `runs`, `events`, `artifacts`, `tool_calls`.
- **Tool Receipts:** Every call generates a `tool_calls` row with `input_hash`, `output_hash`, and `artifact_refs`.
- **Artifacts:** `lstat`-first. Regular files = content hash; non-regular = `meta:*, bytes=0`.

## 4. Guest Runtime (agentd) & Security
Dumb executor with strict side-effect bounds.

- **Tools:** `shell.exec`, `fs.read/write`, `http.fetch`. Deny by default.
- **Filesystem:** Rootfs **RO** (`rootflags=noload`). Only `/data` is RW.
- **Path Guard:** `/data` writes must canonicalize (`Clean` + `Abs`) and pass prefix check to prevent symlink escapes.
- **Persistence:** Volume mounts at `/dev/virmux-data` -> `/data`. No cross-agent leakage.

## 5. Lifecycle & Reliability
Snapshot-resume with deterministic fallback.

- **Precedence:** `mem/state` > `agent.last_snapshot_id` > `latest.json`.
- **Resume Policy:** Try snapshot once. Any fault (resolve/load/wait) => `StopVMM+Wait` then cold boot.
- **Watchdog:** 3s stall on shutdown/wait => `SIGKILL` + `vm.watchdog.kill` event.
- **Telemetry:** `lost_logs`, `lost_metrics`, `handshake_ms` are mandatory in `run.finished`.

## 6. Verification & Ship Gates (L5)
No-ship if any gate fails.

- **G0 Host:** `doctor` + image checksum verification.
- **G1 Boot:** `vm:boot:contract` (zero lost counters).
- **G2 Transport:** `vm:vsock:chaos` (handshake p95 <= 2s).
- **G3 Tool:** Shell/FS/HTTP/No-leak guards.
- **G4 Evidence:** Trace/DB validators + Export/Import roundtrip.
- **G5 Cleanup:** Zero orphan procs, stale socks, or leaked taps.
- **SQL Cert:** **Cohort-scoped** only (`label like 'qa-cert-%'`). Global counts are forbidden.

## 7. Snippet Library (The "Tacit" Map)
- **S1:** `8250.nr_uarts=0` (Disable serial spam).
- **S2:** `READY v0 tools=...` (Strict prefix banner).
- **S3:** `trace emit -> sqlite insert` (Sacred order).
- **S4:** `tar --sort=name --mtime='@0'` (Deterministic export).
- **S5:** `StopVMM+Wait` (Safe fallback trigger).
