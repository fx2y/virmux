# virmux

Firecracker-first local harness with deterministic `mise` task DAG.

## Daily loop

- `mise run doctor`
- `mise run ci:fast`
- `mise run vm:smoke`
- `mise run trace:validate ::: db:check`

## Image pipeline

- `mise run image:build`
- `mise run image:stamp`

Artifacts are immutable by content hash under `.cache/ghostfleet/images/<sha>/`, and `vm/images.lock` pins active image SHA.

## VM loops

- `mise run vm:smoke`
- `mise run vm:smoke:parallel`
- `mise run vm:zygote`
- `mise run vm:resume`
- `mise run bench:snapshot`

## Slack loopback

- `mise run slack:recv`

## Playwright host smoke

- `mise run pw:install`
- `mise run pw:smoke`
