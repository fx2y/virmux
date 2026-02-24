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
- `doctor` is hard preflight; `FAIL:` lines must include direct remediation.
- Runtime-critical wrappers (`vm_smoke.sh`,`vm_resume.sh`,`vm_zygote.sh`,`vm_smoke_parallel.sh`) run `./scripts/doctor.sh` prelaunch.

- `mise` task graph is contract, not convenience:
- expensive tasks define exact `sources` + `outputs`; skip correctness matters
- task names are public API; rename => same-diff callers/docs/tests
- optional lanes cannot silently couple/redefine core lanes

- Release/cert laws:
- proof must be fresh executable evidence; cache-only green invalid
- SQL certs are cohort + freshness scoped (`label-glob`/`cert-ts`/window); historical rows are non-authoritative
- cleanup audit hard-fails on orphan `firecracker`, stale sockets/fifos, leaked `virmux-tap*`
- `ship:core` uncached + isolated from `skill:*`/optional lanes
- `ship:skills` additive/isolated only

- Image law: `vm/images.lock` is SoT; cache key from canonical pinned bytes; immutable cache dirs; seed marker parity with network build.

- Script parser posture strict: validate tokens/enums/paths early; no silent fallback.
- Repo-script invocations inside guards/certs resolve from script file anchor, not caller cwd.
- Machine-readable output discipline: stdout reserved for one parsable payload; progress/status to stderr.
- Cert/DoD marker hygiene (generalized from spec-06): clear stale tmp proofs at start, scope proofs by `cert_ts`, derive pass cells from executable markers (never hardcoded literals), fail on stale marker reuse/order bugs.
