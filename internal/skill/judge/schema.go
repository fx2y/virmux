package judge

import (
	"encoding/json"
	"fmt"
)

const SchemaVersion = "v1"

type JudgeOutput struct {
	Version   string           `json:"version"`
	Score     float64          `json:"score"`
	Pass      bool             `json:"pass"`
	Critique  []string         `json:"critique"`
	Criterion []CriterionScore `json:"criterion"`
}

func ValidateOutput(b []byte) (JudgeOutput, error) {
	var out JudgeOutput
	if err := json.Unmarshal(b, &out); err != nil {
		return out, fmt.Errorf("invalid judge json: %w", err)
	}
	if out.Version != SchemaVersion {
		return out, fmt.Errorf("unsupported judge schema version: %s", out.Version)
	}
	if len(out.Criterion) == 0 {
		return out, fmt.Errorf("judge output missing criterion")
	}
	ids := make(map[string]bool)
	for _, c := range out.Criterion {
		if ids[c.ID] {
			return out, fmt.Errorf("duplicate criterion id: %s", c.ID)
		}
		ids[c.ID] = true
		if c.Value < 0 || c.Value > 1 {
			return out, fmt.Errorf("criterion %s value out of range [0,1]: %f", c.ID, c.Value)
		}
	}
	return out, nil
}
