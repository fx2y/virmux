package rules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/haris/virmux/internal/skill/judge"
)

func TestRuleEngine(t *testing.T) {
	tmp, err := os.MkdirTemp("", "rules-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dbPath := filepath.Join(tmp, "virmux.sqlite")
	// For now we don't need a real DB if we don't test VerifyReplayHashes or we mock it.
	// But let's at least check if Evaluate runs.

	e := &Engine{
		DBPath:  dbPath,
		RunsDir: tmp,
	}

	// Create a fake skill-run.json
	runDir := filepath.Join(tmp, "run-1")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	meta := `{"budget":{"tool_calls":5}}`
	if err := os.WriteFile(filepath.Join(runDir, "skill-run.json"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

	ev := judge.Evidence{
		RunID:     "run-1",
		RunDir:    runDir,
		ToolCalls: 3,
	}

	results, err := e.Evaluate(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}

	foundBudget := false
	for _, r := range results {
		if r.ID == "rule_budget_tool_calls" {
			foundBudget = true
			if !r.Pass {
				t.Errorf("expected budget rule to pass, got fail")
			}
		}
	}
	if !foundBudget {
		t.Errorf("rule_budget_tool_calls not found in results")
	}
}
