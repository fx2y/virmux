#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
status="$root/tmp/pw-install-status.json"

if [[ ! -f "$status" ]]; then
  echo "pw:install:status: missing status file $status" >&2
  exit 1
fi

jq -e '
  .installed_at and
  (.mode == "with_deps" or .mode == "browser_only") and
  (.audit_high|type)=="number" and
  (.audit_moderate|type)=="number" and
  (.audit_low|type)=="number" and
  (.audit_critical|type)=="number" and
  (.audit_total|type)=="number"
' "$status" >/dev/null

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/pw-install-status.ok"
echo "pw:install:status: OK"
