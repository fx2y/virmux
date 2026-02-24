package gates

import (
	"strings"
	"testing"

	"github.com/haris/virmux/internal/store"
)

func TestGateEvalRefusesInvalidVerdictJSON(t *testing.T) {
	t.Parallel()
	v := GateEval(store.EvalRun{VerdictJSON: "{", Pass: true})
	if v.Pass {
		t.Fatalf("expected failure")
	}
	if v.Refusal != "JUDGE_INVALID" {
		t.Fatalf("expected JUDGE_INVALID, got %s", v.Refusal)
	}
}

func TestGateEvalRefusesHardGateFailure(t *testing.T) {
	t.Parallel()
	v := GateEval(store.EvalRun{
		Pass:        true,
		VerdictJSON: `{"pass":true,"hard":{"schema":true,"replay":false}}`,
	})
	if v.Pass {
		t.Fatalf("expected failure")
	}
	if v.Refusal != "AB_REGRESSION" || !strings.Contains(v.Reason, "replay") {
		t.Fatalf("unexpected refusal: %+v", v)
	}
}

func TestGateEvalRefusesFailRateRegressionWithoutHardMap(t *testing.T) {
	t.Parallel()
	v := GateEval(store.EvalRun{
		Pass:          true,
		FailRateDelta: 0.01,
		VerdictJSON:   `{"pass":true}`,
	})
	if v.Pass {
		t.Fatalf("expected failure")
	}
	if v.Refusal != "AB_REGRESSION" {
		t.Fatalf("unexpected refusal: %+v", v)
	}
}

func TestGateEvalPassesWhenHardGatesPass(t *testing.T) {
	t.Parallel()
	v := GateEval(store.EvalRun{
		Pass:          true,
		FailRateDelta: -0.2,
		ScoreP50Delta: 0.3,
		CostDelta:     0.1,
		VerdictJSON:   `{"pass":true,"hard":{"schema":true,"replay":true,"fail_rate":true},"soft":{"p50":0.3}}`,
	})
	if !v.Pass {
		t.Fatalf("expected pass: %+v", v)
	}
}
