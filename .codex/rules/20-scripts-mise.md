---
paths:
  - "scripts/**/*.sh"
  - "mise.toml"
  - "vm/image-src/manifest.json"
  - "vm/images.lock"
---
# Script + DAG Rules
- Bash scripts: `#!/usr/bin/env bash` + `set -euo pipefail`.
- Reuse `scripts/common.sh`; no duplicate helper forks.

- `doctor` is hard preflight; outputs actionable `FAIL:` remediation.
- Runtime-critical wrappers (`vm_smoke.sh`,`vm_resume.sh`,`vm_zygote.sh`,`vm_smoke_parallel.sh`) must run `./scripts/doctor.sh` prelaunch.

- Expensive tasks must define precise `sources` + `outputs`; skip correctness is contract.
- Release proof must be fresh evidence; cache-only green is invalid.

- Image contract: lock=`vm/images.lock`; cache=`.cache/ghostfleet/images/<sha>/`; pinned/verified source bytes; canonical key; immutable cache dirs; bounded lock + stale-owner recovery; `image:seed` markers parity with network build.

- `ship:core` must run uncached core gates and emit cohort artifacts.
- SQL cert on append-only DB must be cohort-scoped unless explicit legacy backfill.
- Cleanup audit hard-fails on any orphan `firecracker`, stale `firecracker.sock|vsock*.sock|*.fifo`, leaked `virmux-tap*`.

- `ship:skills` is isolated/additive (fresh C2..C7 evidence + docs-drift + cleanup).
- `ship:core` must contain zero `skill:*`/`ship:skills` refs.
- Optional lanes (`vm:net:probe`,`slack:recv`,`pw:*`,`skill:*`) cannot redefine core unless explicitly promoted.

- Cert scope/freshness: queries must be cohort + time-window scoped; historical rows are non-authoritative for cutover.
- Script parser posture strict: token/enum inputs validated early; no silent fallback.
- Task names are public API; renames require same-diff caller/docs/test updates.
