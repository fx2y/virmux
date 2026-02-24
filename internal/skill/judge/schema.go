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
	// TODO: add more strict validation (unique ids, value ranges)
	return out, nil
}
