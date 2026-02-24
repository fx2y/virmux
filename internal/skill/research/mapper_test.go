package research

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	yaml "gopkg.in/yaml.v2"
)

func TestMapperRun(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "virmux-mapper-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	runID := "test-run"
	runDir := filepath.Join(tmpDir, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "test-plan",
		Goal: "test goal",
		DimsDidntAsk: []string{"none"},
		Tracks: []Track{
			{ID: "T1", Q: "Query 1", Kind: "deep"},
		},
	}
	planData, _ := yaml.Marshal(plan)
	if err := os.WriteFile(filepath.Join(runDir, "plan.yaml"), planData, 0644); err != nil {
		t.Fatal(err)
	}

	mapper := &DefaultMapper{RunsDir: tmpDir}
	_, err = mapper.Run(context.Background(), MapInput{RunID: runID, TrackID: "T1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify output file
	mapFile := filepath.Join(runDir, "map", "T1.jsonl")
	if _, err := os.Stat(mapFile); os.IsNotExist(err) {
		t.Fatalf("map file not created: %s", mapFile)
	}

	data, err := os.ReadFile(mapFile)
	if err != nil {
		t.Fatal(err)
	}

	var row MapResultRow
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatalf("failed to unmarshal row: %v", err)
	}
	if row.TrackID != "T1" {
		t.Errorf("expected track T1, got %s", row.TrackID)
	}
}
