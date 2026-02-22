# Operational Snippets

## Daily gate
```bash
mise run doctor
mise run ci:fast
mise run vm:smoke
mise run trace:validate ::: db:check
```

## Perf/race/resume
```bash
mise run vm:smoke:parallel
mise run vm:zygote
mise run vm:resume
mise run bench:snapshot  # trend resume_ms
```

## Integrations
```bash
mise run slack:recv
mise run pw:install
mise run pw:smoke
```

## Read telemetry quickly
```bash
# last runs + resume mode/error
sqlite3 .cache/ghostfleet/events.db \
  "select id, resume_mode, coalesce(resume_error,'') from runs order by id desc limit 10;"

# trace tail
tail -n 20 .cache/ghostfleet/traces/latest.jsonl
```

## Triage matrix
- `doctor` fails on `devkvm`: fix `/dev/kvm` perms/ownership first.
- smoke missing `ok`: serial command path broken (`ttyS0` wiring/regression).
- resume fallback spike: snapshot compatibility drift; inspect `resume_error` frequency.
- `db:check` fail: WAL/FK/index drift; treat as contract break.
