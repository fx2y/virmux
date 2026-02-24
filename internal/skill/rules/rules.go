package rules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/haris/virmux/internal/skill/judge"
	"github.com/haris/virmux/internal/skill/run"
)

type RuleResult struct {
	ID      string         `json:"id"`
	Value   float64        `json:"value"`
	Pass    bool           `json:"pass"`
	Reason  string         `json:"reason,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

type Rule interface {
	ID() string
	Evaluate(ctx context.Context, e *Engine, ev judge.Evidence, meta skillRunMeta) (RuleResult, error)
}

type Engine struct {
	DBPath  string
	RunsDir string
}

func (e *Engine) Evaluate(ctx context.Context, ev judge.Evidence) ([]RuleResult, error) {
	if strings.TrimSpace(e.DBPath) == "" {
		return nil, errors.New("rule engine requires db path")
	}
	if strings.TrimSpace(ev.RunDir) == "" || strings.TrimSpace(ev.RunID) == "" {
		return nil, errors.New("rule engine requires run dir/id")
	}

	meta, err := readSkillRunMeta(ev.RunDir)
	if err != nil {
		return nil, fmt.Errorf("rule_meta: %w", err)
	}

	allRules := []Rule{
		&ReplayRule{},
		&BudgetToolCallsRule{},
		&JSONSchemaRule{},
		&RequiredSectionsRule{},
	}

	var results []RuleResult
	for _, r := range allRules {
		res, err := r.Evaluate(ctx, e, ev, meta)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.ID(), err)
		}
		results = append(results, res)
	}

	return results, nil
}

type ReplayRule struct{}

func (r *ReplayRule) ID() string { return "rule_replay" }
func (r *ReplayRule) Evaluate(ctx context.Context, e *Engine, ev judge.Evidence, _ skillRunMeta) (RuleResult, error) {
	replayRes, err := run.VerifyReplayHashes(e.DBPath, ev.RunDir, ev.RunID)
	if err != nil {
		msg := err.Error()
		if !strings.Contains(msg, "replay mismatch") {
			return RuleResult{}, fmt.Errorf("rule_replay: %w", err)
		}
		replayRes = run.ReplayReport{Verified: false, Mismatch: msg}
	}
	replayPass := replayRes.Verified
	replayReason := ""
	if !replayRes.Verified {
		replayReason = replayRes.Mismatch
	}
	return RuleResult{
		ID:     r.ID(),
		Value:  boolToFloat(replayPass),
		Pass:   replayPass,
		Reason: replayReason,
	}, nil
}

type BudgetToolCallsRule struct{}

func (r *BudgetToolCallsRule) ID() string { return "rule_budget_tool_calls" }
func (r *BudgetToolCallsRule) Evaluate(_ context.Context, _ *Engine, ev judge.Evidence, meta skillRunMeta) (RuleResult, error) {
	if meta.Budget.ToolCalls <= 0 {
		return RuleResult{ID: r.ID(), Value: 1.0, Pass: true}, nil
	}
	pass := ev.ToolCalls <= meta.Budget.ToolCalls
	reason := ""
	if !pass {
		reason = fmt.Sprintf("tool_calls exceeded budget (%d > %d)", ev.ToolCalls, meta.Budget.ToolCalls)
	}
	return RuleResult{
		ID:     r.ID(),
		Value:  boolToFloat(pass),
		Pass:   pass,
		Reason: reason,
	}, nil
}

type JSONSchemaRule struct{}

func (r *JSONSchemaRule) ID() string { return "rule_json_schema" }
func (r *JSONSchemaRule) Evaluate(_ context.Context, _ *Engine, ev judge.Evidence, meta skillRunMeta) (RuleResult, error) {
	schema, ok := meta.Expect["json_schema"].(map[string]any)
	if !ok {
		return RuleResult{ID: r.ID(), Value: 1.0, Pass: true}, nil
	}
	pass, reason := validateJSONSchema(ev.RunDir, schema)
	return RuleResult{
		ID:     r.ID(),
		Value:  boolToFloat(pass),
		Pass:   pass,
		Reason: reason,
	}, nil
}

type RequiredSectionsRule struct{}

func (r *RequiredSectionsRule) ID() string { return "rule_required_sections" }
func (r *RequiredSectionsRule) Evaluate(_ context.Context, _ *Engine, ev judge.Evidence, meta skillRunMeta) (RuleResult, error) {
	sections, ok := meta.Expect["required_sections"].([]any)
	if !ok || len(sections) == 0 {
		return RuleResult{ID: r.ID(), Value: 1.0, Pass: true}, nil
	}
	// Check any markdown files for these sections (headers)
	artDir := filepath.Join(ev.RunDir, "artifacts")
	entries, err := os.ReadDir(artDir)
	if err != nil {
		return RuleResult{ID: r.ID(), Value: 0.0, Pass: false, Reason: "no artifacts directory"}, nil
	}
	foundAnyMD := false
	missing := []string{}
	for _, s := range sections {
		secStr, ok := s.(string)
		if !ok {
			continue
		}
		found := false
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				foundAnyMD = true
				b, err := os.ReadFile(filepath.Join(artDir, e.Name()))
				if err != nil {
					continue
				}
				if strings.Contains(string(b), "# "+secStr) || strings.Contains(string(b), "## "+secStr) {
					found = true
					break
				}
			}
		}
		if !found {
			missing = append(missing, secStr)
		}
	}
	if !foundAnyMD {
		return RuleResult{ID: r.ID(), Value: 0.0, Pass: false, Reason: "no markdown artifacts found to check sections"}, nil
	}
	if len(missing) > 0 {
		return RuleResult{
			ID:     r.ID(),
			Value:  0.0,
			Pass:   false,
			Reason: fmt.Sprintf("missing required sections: %s", strings.Join(missing, ", ")),
		}, nil
	}
	return RuleResult{ID: r.ID(), Value: 1.0, Pass: true}, nil
}

func boolToFloat(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}

type skillRunMeta struct {
	Budget struct {
		ToolCalls int `json:"tool_calls"`
		Seconds   int `json:"seconds"`
		Tokens    int `json:"tokens"`
	} `json:"budget"`
	Expect map[string]any `json:"expect"`
}

func readSkillRunMeta(runDir string) (skillRunMeta, error) {
	var meta skillRunMeta
	b, err := os.ReadFile(filepath.Join(runDir, "skill-run.json"))
	if err != nil {
		return meta, err
	}
	err = json.Unmarshal(b, &meta)
	return meta, err
}

func validateJSONSchema(runDir string, schema map[string]any) (bool, string) {
	// Simple improved placeholder: check if any .json artifact exists and is valid JSON
	artDir := filepath.Join(runDir, "artifacts")
	entries, err := os.ReadDir(artDir)
	if err != nil {
		return false, "no artifacts directory"
	}
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			found = true
			b, err := os.ReadFile(filepath.Join(artDir, e.Name()))
			if err != nil {
				return false, fmt.Sprintf("read %s: %v", e.Name(), err)
			}
			var v any
			if err := json.Unmarshal(b, &v); err != nil {
				return false, fmt.Sprintf("invalid json in %s: %v", e.Name(), err)
			}
			// In a real implementation we would validate against the schema map[string]any
		}
	}
	if !found {
		return false, "no json artifacts found"
	}
	return true, ""
}
