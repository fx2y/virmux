---
paths:
  - "scripts/doctor.sh"
  - "scripts/vm_*.sh"
  - "scripts/bench_snapshot.sh"
  - "scripts/trace_validate.sh"
  - "scripts/db_check.sh"
  - "scripts/slack_*.sh"
  - "scripts/pw_*.sh"
  - "internal/vm/*.go"
  - "internal/store/*.go"
  - "internal/trace/*.go"
  - "internal/slack/*.go"
  - "internal/vm/**/*.go"
  - "internal/store/**/*.go"
  - "internal/trace/**/*.go"
  - "internal/slack/**/*.go"
---
# Failure -> Fix Playbook
- Stop-ship invariant set:
- host preflight red (`doctor`) => stop.
- missing smoke markers `Linux`+`ok` on smoke bridge => stop.
- missing boundary events (`vm.boot.started`,`vm.exec.injected`,`vm.exit.observed`) => stop.
- missing/non-nullable resume telemetry (`resume_mode`,`resume_source`,`resume_error`) => stop.
- evidence integrity red (`trace:validate`,`db:check`,cohort SQL cert) => stop.
- cleanup red (orphan proc/stale sock/vsock/fifo/tap) => stop.
- cert/proof from cached lanes only => stop.

- Triage priority (always in this order):
1. `runs/virmux.sqlite` rows (`runs`,`events`,`tool_calls`,`artifacts`).
2. `runs/<id>/meta.json` + `trace.ndjson`.
3. run-local artifacts (`serial.log`,`fc.log`,`fc.metrics.log`,tool refs,sockets).
4. stdout/stderr.

- Symptom -> likely breach -> first probe:
- `doctor` CPU/KVM failures -> wrong host/permissions -> `/sys/module/kvm`, `/dev/kvm`, group membership.
- artifact/firecracker mismatch in `doctor` -> lock/image drift -> verify `vm/images.lock` target bytes/checksums.
- missing smoke markers -> serial wrapper/parse regression -> inspect `runs/<id>/serial.log` segment markers.
- vsock CONNECT retries exhaust -> transport path/handshake drift -> inspect `tmp/vsock-chaos-report.json`, `handshake_ms`, retry class.
- READY parse fails -> protocol spoof/drift -> confirm strict prefix `READY v0 tools=`.
- resume always fallback on healthy zygote -> snapshot restore path broken -> verify SDK snapshot handler + snapshot lineage.
- resume hard-fails instead of fallback -> policy regression -> enforce `StopVMM+Wait` then cold boot.
- run stuck `status=running` post-fault -> finalization atomicity regression -> verify terminal event/status persist path.
- trace validator red -> schema/order drift -> restore append-only + required keys/types.
- db check red -> WAL/FK/index drift -> repair schema/init/migration, no bypass.
- tool succeeded but refs missing -> host materialization gap -> verify `vm.tool.result` refs + run artifact rows.
- export/import failure -> manifest/safe-extract regression -> verify deterministic pack + symlink/path escape guards.
- bench pass with fallback runs -> false-green perf cert -> require pure snapshot cohort.
- cleanup false-green -> weak process/socket probes -> enforce exact-match probes (`pgrep -x firecracker`) and hard failures.

- Anti-patterns (hard no):
- certifying with unscoped SQL on append-only historical DB.
- counting fallback cold boots as snapshot perf success.
- accepting run success when evidence rows/refs are missing.
- manual fs archaeology before SQL inventory.
- doc/spec updates decoupled from behavior changes.
