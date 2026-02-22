# RFC-000 Contracts (Dense Annex)

| Seam | Input | Success | Failure | Evidence |
|---|---|---|---|---|
| Host gate | host deps + KVM | `doctor=0` | explicit hard fail | doctor log + exit |
| VM run | image lock + cmd | serial/output observed | launch/serial fail | run row + stdout + trace |
| Persistence | agent volume ptr | file visible next run | missing/mount drift | artifact diff |
| Skill run | skill dir + input | output artifact + scoreable trace | schema/tool breach | trace + error event |
| Judge | rubric + artifact | score json + critique | rubric/model failure | score row |
| A/B | baseline,candidate,set | delta stats | invalid config | experiment row |
| Promotion | gate predicates | role ref updated | gate fail | promotion row |
| Slack role | event + trigger | draft/post by gate | parser/gate fail | slack_events + role logs |
| MCP client | server ref + method | tool result | protocol/transient fail | tool_call event |
| MCP server | inbound rpc | valid result/error | auth/shape fail | request log |

Run state machine:
```text
CREATED -> BOOTING -> RUNNING -> (SUCCEEDED | FAILED | TIMEOUT | KILLED)
```
Invariant: terminal states immutable.
