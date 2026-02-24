package rules

import (
	"context"
	"encoding/json"
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

type Engine struct {
	DBPath  string
	RunsDir string
}

func (e *Engine) Evaluate(ctx context.Context, ev judge.Evidence) ([]RuleResult, error) {
	var results []RuleResult

	// 1. Rule Replay
	replayRes, err := run.VerifyReplayHashes(e.DBPath, ev.RunDir, ev.RunID)
	replayPass := err == nil && replayRes.Verified
	replayReason := ""
	if err != nil {
		replayReason = err.Error()
	} else if !replayRes.Verified {
		replayReason = replayRes.Mismatch
	}
	results = append(results, RuleResult{
		ID:     "rule_replay",
		Value:  boolToFloat(replayPass),
		Pass:   replayPass,
		Reason: replayReason,
	})

	// 2. Budget Ceilings (Time/Cost)
	// We load skill-run.json to get actual usage and budget
	meta, err := readSkillRunMeta(ev.RunDir)
	if err == nil {
		// Time ceiling
		if meta.Budget.Seconds > 0 {
			// We need to know how long the run took.
			// This info is in runs table, but we might not have it easily here.
			// For now we can check trace for timestamps or just skip if not easily available.
			// Actually, let's look at internal/skill/run/core.go BudgetTracker.
		}
		// Tool calls ceiling
		if meta.Budget.ToolCalls > 0 {
			pass := ev.ToolCalls <= meta.Budget.ToolCalls
			reason := ""
			if !pass {
				reason = fmt.Sprintf("tool_calls exceeded budget (%d > %d)", ev.ToolCalls, meta.Budget.ToolCalls)
			}
			results = append(results, RuleResult{
				ID:     "rule_budget_tool_calls",
				Value:  boolToFloat(pass),
				Pass:   pass,
				Reason: reason,
			})
		}
	}

	// 3. JSON Schema / Required Sections
	// If expect.json_schema exists in skill-run.json, we can validate artifacts
	if meta.Expect != nil {
		if schema, ok := meta.Expect["json_schema"].(map[string]any); ok {
			pass, reason := validateJSONSchema(ev.RunDir, schema)
			results = append(results, RuleResult{
				ID:     "rule_json_schema",
				Value:  boolToFloat(pass),
				Pass:   pass,
				Reason: reason,
			})
		}
	}

	return results, nil
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
	// Simple placeholder for now: check if any .json artifact exists and is valid JSON
	// In real implementation we'd use a json schema validator
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
			// TODO: actual schema validation
		}
	}
	if !found {
		return false, "no json artifacts found"
	}
	return true, ""
}
