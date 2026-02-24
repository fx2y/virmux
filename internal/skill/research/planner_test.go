package research

import (
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
	p.DimsDidntAsk = []string{"test-dim"}
	if err := p.Validate(); err != nil {
		t.Errorf("expected no error for valid plan, got %v", err)
	}
}

func TestParsePlanStrict(t *testing.T) {
	yml := `
goal: test
dims_you_didnt_ask: ["dim1"]
tracks:
- id: 1
  q: q
  kind: deep
unknown_key: true
`
	_, err := ParsePlan([]byte(yml))
	if err == nil {
		t.Errorf("expected error for unknown key")
	}
}
