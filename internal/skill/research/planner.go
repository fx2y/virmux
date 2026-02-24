package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	yaml "gopkg.in/yaml.v2"
)

type PlanBudget struct {
	USD  float64 `yaml:"usd"`
	Mins int     `yaml:"mins"`
}

type TrackSchema struct {
	Rows []string `yaml:"rows"`
}

type Track struct {
	ID       string      `yaml:"id"`
	Q        string      `yaml:"q"`
	Kind     string      `yaml:"kind"` // "deep" or "wide"
	Targets  []string    `yaml:"targets,omitempty"`
	Attrs    []string    `yaml:"attrs,omitempty"`
	Schema   TrackSchema `yaml:"schema"`
	Budget   PlanBudget  `yaml:"budget"`
	StopRule string      `yaml:"stop_rule"`
	Deps     []string    `yaml:"deps"`
}

type ReduceConfig struct {
	Outputs []string `yaml:"outputs"`
	Rubric  []string `yaml:"rubric"`
}

type Plan struct {
	PlanID       string       `yaml:"plan_id"`
	Goal         string       `yaml:"goal"`
	Assumptions  []string     `yaml:"assumptions"`
	Unknowns     []string     `yaml:"unknowns"`
	DimsDidntAsk []string     `yaml:"dims_you_didnt_ask"`
	Tracks       []Track      `yaml:"tracks"`
	Reduce       ReduceConfig `yaml:"reduce"`
	Revision     int          `yaml:"revision,omitempty"`
}

// Hash returns the deterministic SHA256 of the plan body (excluding PlanID itself).
func (p *Plan) Hash() string {
	clone := *p
	clone.PlanID = ""
	b, _ := yaml.Marshal(clone)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Validate ensures the plan meets hard constraints.
func (p *Plan) Validate() error {
	if p.Goal == "" {
		return fmt.Errorf("%s: goal is empty", FailurePlanSchema)
	}
	if len(p.Tracks) == 0 {
		return fmt.Errorf("%s: no tracks defined", FailurePlanSchema)
	}
	if len(p.DimsDidntAsk) == 0 {
		return fmt.Errorf("%s: dims_you_didnt_ask is mandatory", FailurePlanSchema)
	}
	for _, t := range p.Tracks {
		if t.ID == "" {
			return fmt.Errorf("%s: track.id required", FailurePlanSchema)
		}
		if t.Q == "" {
			return fmt.Errorf("%s: track.q required", FailurePlanSchema)
		}
		if t.Kind != "deep" && t.Kind != "wide" {
			return fmt.Errorf("%s: track.kind must be deep or wide", FailurePlanSchema)
		}
	}
	return nil
}

// ParsePlan strictly unmarshals YAML into a Plan.
func ParsePlan(data []byte) (*Plan, error) {
	var p Plan
	if err := yaml.UnmarshalStrict(data, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", FailurePlanSchema, err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

type DefaultPlanner struct{}

func (p *DefaultPlanner) Compile(ctx context.Context, input PlanInput) (PlanOutput, error) {
	// TODO: implement LLM-based plan generation
	plan := Plan{
		Goal:         input.Query,
		DimsDidntAsk: []string{"dims you didn't ask"},
		Tracks: []Track{
			{
				ID:   "track-1",
				Q:    fmt.Sprintf("Research history of %s", input.Query),
				Kind: "deep",
				Budget: PlanBudget{USD: 1.0, Mins: 5},
				StopRule: "found 1 source",
			},
			{
				ID:   "track-2",
				Q:    fmt.Sprintf("Research current state of %s", input.Query),
				Kind: "deep",
				Budget: PlanBudget{USD: 1.0, Mins: 5},
				StopRule: "found 1 source",
			},
			{
				ID:   "track-3",
				Q:    "Synthesize findings",
				Kind: "deep",
				Budget: PlanBudget{USD: 1.0, Mins: 5},
				StopRule: "done",
				Deps: []string{"track-1", "track-2"},
			},
		},
	}
	plan.PlanID = plan.Hash()
	return PlanOutput{PlanID: plan.PlanID, Plan: &plan}, nil
}
