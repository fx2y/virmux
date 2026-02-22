# Contract Map (Dense)

| Seam | Input | Success | Failure | Persisted evidence |
|---|---|---|---|---|
| Host gate | host kernel/dev/userland | `doctor=0` | hard fail, explicit reason | doctor stdout/stderr + exit code |
| Image build | pinned manifest URLs | hash artifact dir created/reused | build/fetch fail | `.cache/ghostfleet/images/<sha>/metadata.json` |
| Image select | deterministic hash | `vm/images.lock` pinned | missing/invalid lock | lockfile diff |
| VM smoke | lock + Firecracker + `/dev/kvm` | serial has `Linux`+`ok` | marker missing / launch fail | trace JSONL + sqlite runs/events |
| Zygote | smoke-booted VM | snapshot files + latest metadata | snapshot API/path fail | zygote metadata + logs |
| Resume | snapshot artifacts | snapshot resume | fallback cold boot | `resume_mode`,`resume_error` in run telemetry |
| Trace validate | JSONL stream | schema-valid | invalid shape/order | `trace:validate` exit |
| DB check | sqlite file | WAL+FK+indexes enforced | pragma/index mismatch | `db:check` exit |
| Slack recv | fixtures/HTTP replay | ingested rows | parse/store fail | sqlite `slack_events` + logs |
| PW smoke | npm+browser deps | headless open succeeds | deps/browser fail | install/smoke logs |

Key principle: every seam must produce machine-checkable evidence, never inferred success.
