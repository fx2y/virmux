---
paths:
  - "scripts/**/*.sh"
  - "mise.toml"
  - "vm/image-src/manifest.json"
  - "vm/images.lock"
---
# Script + DAG Rules
- Bash scripts start with `#!/usr/bin/env bash` + `set -euo pipefail`.
- Reuse `scripts/common.sh` helpers (`repo_root`, hash/lock helpers); no duplicated repo-root logic.
- `doctor` remains bootstrap-safe: never require `vm/images.lock` in its source set.
- `doctor` failures must be hard, explicit, and actionable; no soft warnings for required prerequisites.
- Expensive tasks must declare precise `sources` + `outputs`; skip-on-unchanged is required, not optional.
- Image pipeline is content-addressed only: hash(manifest + build scripts) -> immutable cache dir -> `vm/images.lock`.
- Never edit cached artifacts in place; write new sha dir and repoint lock.
- Approved fallbacks only:
- `pw:install`: `--with-deps` then browser-only retry.
- `vm:resume`: snapshot-first then fallback cold boot with telemetry.
- Task names are API surface; renames require updating docs/scripts/callers in same diff.
