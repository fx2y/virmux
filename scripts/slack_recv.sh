#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
addr="127.0.0.1:18080"
mkdir -p "$root/tmp" "$root/runs"

./scripts/slack_fixture.sh

go run ./cmd/virmux slack-server --listen "$addr" --db "$root/runs/virmux.sqlite" >"$root/tmp/slack-server.log" 2>&1 &
server_pid=$!
cleanup() {
  kill "$server_pid" >/dev/null 2>&1 || true
  wait "$server_pid" >/dev/null 2>&1 || true
}
trap cleanup EXIT

for _ in $(seq 1 40); do
  if curl -fsS "http://$addr/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

challenge="$(jq -r '.challenge' "$root/fixtures/slack/url_verification.json")"
resp="$(curl -fsS -X POST -H 'Content-Type: application/json' --data @"$root/fixtures/slack/url_verification.json" "http://$addr/slack/events")"
if [[ "$resp" != "$challenge" ]]; then
  echo "slack:recv: url_verification mismatch: expected=$challenge got=$resp" >&2
  exit 1
fi

curl -fsS -X POST -H 'Content-Type: application/json' --data @"$root/fixtures/slack/message_event.json" "http://$addr/slack/events" >/dev/null

count="$(sqlite3 "$root/runs/virmux.sqlite" "SELECT COUNT(*) FROM slack_events WHERE event_type='slack.event_callback';")"
if [[ "$count" -lt 1 ]]; then
  echo "slack:recv: expected >=1 slack.event_callback row" >&2
  exit 1
fi

echo "slack:recv: OK"
