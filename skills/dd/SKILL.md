---
description: deterministic shell smoke skill for C1 lane
metadata:
  spec04: c1
name: dd
os:
- linux
requires:
  bins: []
  config: []
  env: []
---
# Steps

Run one deterministic guest tool call that writes a file in `/dev/virmux-data`
and prints a short success marker.

<!-- virmux-refine:start -->
## Refinement Notes
- run `1771906396060939609-skillrun`: tighten `format` by adding one explicit acceptance bullet and one concrete output check.
<!-- virmux-refine:end -->
