# Handoff: Vsock-First Tool Appliance (Spec-0/03)

Handoff for work stream 003. Focus: deterministic tool execution via vsock, append-only evidence, and replayable bundles.

## Core Mandates
- **Contract > Convenience**: Invariants (Lost counters=0, Mandatory boundary events) are stop-ship.
- **Determinism > Throughput**: Replayable state (Image lock, RO rootfs, Fixed dual-write order) is sacred.
- **Evidence > Stdout**: `runs/virmux.sqlite` + `trace.ndjson` + `artifacts/` are the only truth.
- **Fail Closed**: Any ambiguity (Handshake fail, Tool deny, Checksum drift) => Hard stop.

## System Layers (L0-L5)
- **L0 Host**: Ubuntu 24.04, `/dev/kvm` RW. `mise run doctor` is the mandatory preflight.
- **L1 Image**: `vm/images.lock` selects immutable `.cache/ghostfleet/images/<sha>`. Cache is write-once.
- **L2 VM**: Boundary events mandatory: `vm.boot.started` -> `vm.exec.injected` -> `vm.exit.observed`.
- **L3 Transport**: Vsock-first. `CONNECT <port>` -> `OK <port>` -> `READY v0 tools=...` -> Framed RPC.
- **L4 Evidence**: `trace.ndjson` (primary) + `trace.jsonl` (symlink) -> `sqlite` (events, artifacts, tool_calls).
- **L5 Release**: `mise run ship:core` executes uncached cohort-scoped SQL cert + cleanup audit.

## Vsock Tool Plane (C2/C3)
### Handshake Contract
1. Host connects to guest CID/Port.
2. Host sends `CONNECT <port>
`.
3. Guest replies `OK <port>
` (Wait for `OK ` prefix).
4. Guest sends `READY v0 tools=shell.exec,fs.read,fs.write,http.fetch
`.
5. Host parses caps and starts RPC.

### Tool RPC
- **Frame**: `u32le length` + `json payload`.
- **Registry**: `internal/agentd` defines allowlisted tools. Deny-by-default.
- **FS Guard**: `/data` (actually `/dev/virmux-data`) is the only RW path. `lstat` walk to prevent symlink escape.
- **Errors**: `TIMEOUT`, `DENIED`, `DISCONNECT`, `CRASH`, `INTERNAL` are stable host-visible classes.

## Evidence & Export (C4)
- **Dual-Write**: `trace.Emit()` -> `st.InsertEvent()`. Never reverse.
- **Artifacts**: Registry is sqlite-first. Regular files = hash; Non-regular (sock/fifo/dir) = `sha256=meta:*`, `bytes=0`.
- **Deterministic Export**: `tar --sort=name --mtime='@0' --owner=0 --group=0`. Replayable on fresh host.
- **Import**: Verifies manifest + rehashes artifacts before DB insert.

## Lifecycle & Resume (C5)
- **Resume Policy**: Attempt snapshot once. Any fault => `StopVMM+Wait` then cold fallback.
- **Telemetry**: `run.finished` must carry `resume_mode`, `resume_source`, `resume_error`.
- **Watchdog**: Wait machine with 3s probe. `SIGKILL` on unresponsive FC socket. Emit `vm.watchdog.kill`.

## Operator Walkthroughs

### 1. The "Happy Path" (PO/FDE)
```bash
# 1. Preflight
mise run doctor && mise run image:stamp

# 2. Write tool
go run ./cmd/virmux vm-run --agent po03 --label po03-write --vsock-cid 52 
  --tool fs.write --tool-args-json '{"path":"/data/po03.txt","bytes":"po03"}'

# 3. Read tool + Check Evidence
RID=$(sqlite3 runs/virmux.sqlite "select id from runs where label='po03-write' limit 1;")
sqlite3 runs/virmux.sqlite "select tool,output_hash,error_code from tool_calls where run_id='$RID';"

# 4. Resume Test
mise run vm:zygote -- --agent po03
mise run vm:resume -- --agent po03 --label po03-resume
```

### 2. The "QA Gate" (Certification)
```bash
# Execute core ship lane (uncached)
mise run ship:core

# Assert SQL Cert (Cohort-scoped)
# VIRMUX_CERT_LABEL_GLOB='qa-cert-%' ./scripts/sql_cert_contract.sh

# Manual Audit
mise run vm:cleanup:audit
# Check tmp/cleanup-audit.log for zero orphans.
```

### 3. The "Chaos Probe" (QA/FDE)
```bash
# 1. Trigger Vsock Chaos (Dialer Stress)
mise run vm:vsock:chaos

# 2. Inspect Handshake Latency
sqlite3 runs/virmux.sqlite "select avg(handshake_ms) from runs where task='vm:run' and label='vsock-chaos';"

# 3. Simulate Resume Fault
go run ./cmd/virmux vm-resume --agent chaos --state-path /tmp/missing --label chaos-fail
# Expect run.finished.resume_mode=fallback_cold_boot
```

## Snippets & Tacit Knowledge
- **Vsock Dial**: Use `internal/transport/vsock.DialWithRetry`. Budget: 12 attempts, quadratic backoff.
- **Tool Result**: Persist `vm.tool.result` event + `tool_calls` row + run-local `artifacts/*.out` refs.
- **FIFO Loss**: `lost_logs > 0` is a stop-ship violation. Backpressure is fatal.
- **NDJSON**: Escape `
` in payloads. `trace_validate` enforces one-line-per-event.
- **Image Rotation**: Guest code edits rotate `vm/images.lock` automatically via `mise` DAG.

## Triage Map
- **Doctor Fails**: Check `/dev/kvm` perms + lock-selected artifact triplet.
- **No Guest Ready**: Check vsock CID + `READY` prefix mismatch in `serial.log`.
- **Connect Exhausted**: Inspect `tmp/vsock-chaos-report.json` + `handshake_ms`.
- **Resume Fallback**: Inspect `run.finished.resume_error`. Check if zygote state files exist.
- **Leak Found**: `pgrep -x firecracker`. Audit `runs/*/vsock*.sock` and `*.fifo`.

---
*Opinion: If it isn't in SQLite, it didn't happen. If it isn't in a bundle, it isn't portable. If it isn't in the doctor, it isn't supported.*
