# Contract Matrix (Plane x Gate)
| Plane | Guard cmd | Evidence | Stop-ship trigger |
|---|---|---|---|
| Host | `./scripts/doctor.sh` | `tmp/doctor.ok` | missing KVM/artifact/socket liveness |
| Boot | `mise run vm:smoke` | `runs/*/serial.log` | missing `Linux`/`ok` markers |
| Data | `mise run trace:validate ::: db:check` | `.trace-validate.ok`,`.db-check.ok` | trace/sqlite drift |
| State | `mise run vm:test:agent-persistence` | `agents/*.json`,`volumes/*.ext4` | cross-agent bleed/ephemeral volume |
| Resume | `mise run vm:test:resume-*` | run.finished payload | null canonical keys / fallback-only regression |
| Perf | `mise run bench:snapshot 5` | `runs/bench-snapshot-summary.json` | p50/p95 budget fail or no snapshot sample |
| Cleanup | `mise run vm:cleanup:audit` | `tmp/cleanup-audit.log` | orphan process/socket/tap leak |
| Ship | `mise run ship:core` | `tmp/ship-core-summary.json` | cached/no-fresh-row certification |
