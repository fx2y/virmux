---
paths:
  - "scripts/**/*.sh"
  - "mise.toml"
  - "vm/image-src/manifest.json"
  - "vm/images.lock"
---
# Script + DAG Rules
- Bash scripts require `#!/usr/bin/env bash` and `set -euo pipefail`.
- Reuse `scripts/common.sh`; do not fork duplicate helpers for root/hash/lock/labels.

- `doctor` is hard preflight:
- must run on cold clone.
- must emit explicit `FAIL:` with direct remediation.
- must accept built-in KVM detection via `/sys/module/kvm`.
- must verify lock-selected artifact triplet + executable Firecracker + AF_UNIX bind/unlink.

- Runtime-critical wrappers (`vm_smoke.sh`,`vm_resume.sh`,`vm_zygote.sh`,`vm_smoke_parallel.sh`) must invoke `./scripts/doctor.sh` prelaunch even if `mise` cache would skip upstream tasks.
- Expensive tasks must declare precise `sources`+`outputs`; incremental skip correctness is contractual.

- Image pipeline contract:
- selector is `vm/images.lock`; cache root is `.cache/ghostfleet/images/<sha>/`.
- manifest pins checksums (`kernel`,`rootfs_squashfs`,`firecracker_tgz`); downloaded bytes are verified pre-build.
- key calc must be canonical and deterministic across env/tool variance.
- cache dirs are immutable/write-once; never mutate in place.
- concurrent image builds must serialize with bounded lock wait + stale-owner recovery.
- air-gapped seed path (`image:seed`) must produce the same cache shape markers as network build (`.complete`, `.manifest-built`).

- Ship/cert contract:
- release proof must be fresh-run evidence; cache-only pass is invalid.
- `ship:core` must execute uncached core gates and emit cohort tag/artifacts.
- SQL cert on append-only DB must be cohort-scoped unless legacy rows are backfilled.

- Perf/correctness gates:
- `bench:snapshot` is hard gate (not trend log): `total_samples==iterations`, `snapshot_resume_count==iterations`, `fallback_count==0`, p50/p95 within budget.
- cleanup audit is hard gate: zero orphan `firecracker` procs; zero stale `firecracker.sock`,`vsock*.sock`,`*.fifo`; zero leaked `virmux-tap*`.

- Optional lanes (`vm:net:probe`,`slack:recv`,`pw:*`) stay isolated unless explicitly promoted to core.
- Task names are public API; renames require same-diff updates to callers/docs/tests.
