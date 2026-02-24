package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

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
		if t.Kind == "wide" {
			if len(t.Targets) == 0 || len(t.Attrs) == 0 {
				return fmt.Errorf("%s: wide track %s must include targets and attrs", FailurePlanSchema, t.ID)
			}
			if t.StopRule == "" {
				return fmt.Errorf("%s: wide track %s must include stop_rule (e.g. coverage>=0.8)", FailurePlanSchema, t.ID)
			}
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

type DefaultPlanner struct {
	Hints HintProvider
}

func (p *DefaultPlanner) Compile(ctx context.Context, input PlanInput) (PlanOutput, error) {
	// 1. Get hints if available
	var hints []string
	if p.Hints != nil {
		hints, _ = p.Hints.GetHints(ctx, input.Query)
	}

	// TODO: implement LLM-based plan generation
	// Stubbed implementation with wide/deep classification logic
	tracks := []Track{
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
	}

	// Incorporate hints into plan unknowns or tracks
	unknowns := []string{"unanswered questions"}
	if len(hints) > 0 {
		unknowns = append(unknowns, hints...)
	}

	// Example wide track classification
	isWide := len(input.Query) > 10 // Dummy condition for stub
	if isWide {
		tracks = append(tracks, Track{
			ID:       "track-wide",
			Q:        fmt.Sprintf("Market scan for %s", input.Query),
			Kind:     "wide",
			Targets:  []string{"TargetA", "TargetB", "TargetC"},
			Attrs:    []string{"Attr1", "Attr2", "Attr3"},
			Budget:   PlanBudget{USD: 2.0, Mins: 10},
			StopRule: "coverage>=0.8",
			Deps:     []string{"track-1"},
		})
	}

	tracks = append(tracks, Track{
		ID:   "track-synth",
		Q:    "Synthesize findings",
		Kind: "deep",
		Budget: PlanBudget{USD: 1.0, Mins: 5},
		StopRule: "done",
		Deps: []string{"track-1", "track-2"},
	})

	plan := Plan{
		Goal:         input.Query,
		DimsDidntAsk: []string{"dims you didn't ask"},
		Tracks:       tracks,
		Unknowns:     unknowns,
	}
	plan.PlanID = plan.Hash()
	return PlanOutput{PlanID: plan.PlanID, Plan: &plan}, nil
}

type DefaultHintProvider struct{}

func (h *DefaultHintProvider) GetHints(ctx context.Context, query string) ([]string, error) {
	// Stubbed per-domain hints
	if strings.Contains(strings.ToLower(query), "agent") {
		return []string{"check arXiv 2602.01331v1", "verify Firecracker isolations"}, nil
	}
	return nil, nil
}
