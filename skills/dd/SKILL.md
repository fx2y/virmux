---
name: dd
description: deterministic shell smoke skill for C1 lane
requires:
  bins: []
  env: []
  config: []
os: [linux]
metadata:
  spec04: c1
---
# Steps

Run one deterministic guest tool call that writes a file in `/dev/virmux-data`
and prints a short success marker.
