package research

import (
	"context"
	"strings"
	"testing"
)

func TestPlanHash(t *testing.T) {
	p1 := &Plan{Goal: "test"}
	p2 := &Plan{Goal: "test"}
	if p1.Hash() != p2.Hash() {
		t.Errorf("expected same hash for identical plans, got %s vs %s", p1.Hash(), p2.Hash())
	}
	p3 := &Plan{Goal: "test 2"}
	if p1.Hash() == p3.Hash() {
		t.Errorf("expected different hash for different plans")
	}
}

func TestPlanValidate(t *testing.T) {
	p := &Plan{}
	if err := p.Validate(); err == nil {
		t.Errorf("expected error for empty plan")
	}
	p.Goal = "test"
	if err := p.Validate(); err == nil {
		t.Errorf("expected error for no tracks")
	}
	p.Tracks = []Track{{ID: "1", Q: "q", Kind: "deep"}}
	if err := p.Validate(); err == nil {
		t.Errorf("expected error for missing dims_you_didnt_ask")
	}
	p.DimsDidntAsk = []string{"d1", "d2", "d3", "d4"}
	if err := p.Validate(); err == nil {
		t.Errorf("expected error for missing reduce.outputs")
	}
	p.Reduce = ReduceConfig{Outputs: []string{"report.md"}}
	if err := p.Validate(); err != nil {
		t.Errorf("expected no error for valid plan, got %v", err)
	}
}

func TestPlanValidateWide(t *testing.T) {
	p := &Plan{
		Goal:         "test",
		DimsDidntAsk: []string{"d1", "d2", "d3", "d4"},
		Reduce:       ReduceConfig{Outputs: []string{"report.md"}},
	}
	p.Tracks = []Track{{ID: "1", Q: "q", Kind: "wide"}}
	if err := p.Validate(); err == nil {
		t.Errorf("expected error for wide track missing targets/attrs")
	}
	p.Tracks[0].Targets = []string{"T1"}
	p.Tracks[0].Attrs = []string{"A1"}
	if err := p.Validate(); err == nil {
		t.Errorf("expected error for wide track missing stop_rule")
	}
	p.Tracks[0].StopRule = "coverage>=0.5"
	if err := p.Validate(); err != nil {
		t.Errorf("expected no error for valid wide plan, got %v", err)
	}
}

func TestPlanValidateDimsFloor(t *testing.T) {
	p := &Plan{
		Goal:         "test",
		DimsDidntAsk: []string{"d1", "d2", "d3"},
		Reduce:       ReduceConfig{Outputs: []string{"report.md"}},
		Tracks:       []Track{{ID: "1", Q: "q", Kind: "deep"}},
	}
	if err := p.Validate(); err == nil {
		t.Fatalf("expected dims floor validation error")
	}
}

func TestDefaultPlannerCompile(t *testing.T) {
	p := &DefaultPlanner{Hints: &DefaultHintProvider{}}
	out, err := p.Compile(context.Background(), PlanInput{Query: "agent research"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.PlanID == "" {
		t.Errorf("expected plan_id")
	}
	// Check hints were incorporated
	found := false
	for _, u := range out.Plan.Unknowns {
		if strings.Contains(u, "arXiv") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected hint about arXiv in unknowns")
	}
}
