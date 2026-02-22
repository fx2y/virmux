#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
mkdir -p "$root/fixtures/slack"

cat > "$root/fixtures/slack/url_verification.json" <<'JSON'
{"token":"fixture-token","challenge":"fixture-challenge-123","type":"url_verification"}
JSON

cat > "$root/fixtures/slack/message_event.json" <<'JSON'
{"token":"fixture-token","type":"event_callback","event":{"type":"message","channel":"C123","user":"U123","text":"hello virmux","ts":"1700000000.000100"}}
JSON

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/fixtures/slack/.fixture-stamp"
