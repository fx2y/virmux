# Research Plane Contracts

## 1. Plan Schema (plan.yaml)
```yaml
id: <sha256>
goal: string
tracks:
  - id: string
    kind: deep|wide
    deps: [uuid]
    query: string
    deterministic: bool # default true
    budget:
      tool_calls: int
      seconds: int
reduce:
  outputs: [report.md, table.csv, slides.md]
```

## 2. Map Output (map/<track_id>.jsonl)
```json
{"track_id": "...", "ok": true, "data": {...}, "evidence_ids": ["..."]}
```

## 3. Evidence Table (SQLite)
| Column | Type | Note |
| :--- | :--- | :--- |
| `id` | UUID | PK |
| `run_id` | UUID | FK |
| `claim` | TEXT | Extracted premise |
| `url` | TEXT | Source URI |
| `quote_span` | TEXT | Direct evidence |
| `confidence` | FLOAT | 0.0-1.0 |
