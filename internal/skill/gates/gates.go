package gates

import (
	"encoding/json"
	"fmt"

	"github.com/haris/virmux/internal/store"
)

type Verdict struct {
	Pass    bool           `json:"pass"`
	Reason  string         `json:"reason"`
	Refusal string         `json:"refusal,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// GateEval implements reusable evaluation ordering: hard(fr,schema,replay) then soft(p50,p90,cost,disagreement).
func GateEval(run store.EvalRun) Verdict {
	doc, err := parseVerdictJSON(run.VerdictJSON)
	if err != nil {
		return Verdict{
			Pass:    false,
			Reason:  fmt.Sprintf("JUDGE_INVALID: invalid eval verdict json: %v", err),
			Refusal: "JUDGE_INVALID",
		}
	}
	details := map[string]any{
		"score_p50_delta": run.ScoreP50Delta,
		"fail_rate_delta": run.FailRateDelta,
		"cost_delta":      run.CostDelta,
	}

	// Hard gates (fail_rate/schema/replay) from persisted verdict doc.
	for gate, pass := range doc.Hard {
		if pass {
			continue
		}
		return Verdict{
			Pass:    false,
			Reason:  fmt.Sprintf("AB_REGRESSION: hard gate %s failed", gate),
			Refusal: "AB_REGRESSION",
			Details: details,
		}
	}
	// Conservative fallback when explicit hard gates are absent.
	if run.FailRateDelta > 0 {
		return Verdict{
			Pass:    false,
			Reason:  "AB_REGRESSION: fail_rate_delta > 0",
			Refusal: "AB_REGRESSION",
			Details: details,
		}
	}
	if !run.Pass || !doc.Pass {
		return Verdict{
			Pass:    false,
			Reason:  "AB_REGRESSION: eval run did not pass overall verdict",
			Refusal: "AB_REGRESSION",
			Details: details,
		}
	}

	// Soft gates are currently evaluated in AB verdict generation and carried here for audit only.
	if len(doc.Soft) > 0 {
		details["soft"] = doc.Soft
	}

	return Verdict{
		Pass:    true,
		Reason:  "All gates passed",
		Details: details,
	}
}

type verdictDoc struct {
	Pass bool            `json:"pass"`
	Hard map[string]bool `json:"hard,omitempty"`
	Soft map[string]any  `json:"soft,omitempty"`
}

func parseVerdictJSON(raw string) (verdictDoc, error) {
	if raw == "" {
		return verdictDoc{}, fmt.Errorf("empty verdict json")
	}
	var doc verdictDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return verdictDoc{}, err
	}
	return doc, nil
}
