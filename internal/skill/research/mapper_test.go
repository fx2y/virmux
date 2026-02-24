package research

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/haris/virmux/internal/store"
	yaml "gopkg.in/yaml.v2"
)

func TestMapperRun(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "virmux-mapper-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	runID := "test-run"
	runDir := filepath.Join(tmpDir, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	st.StartRun(context.Background(), store.Run{ID: runID, Task: "test", StartedAt: time.Now()})

	plan := &Plan{
		PlanID:       "test-plan",
		Goal:         "test goal",
		DimsDidntAsk: []string{"d1", "d2", "d3", "d4"},
		Reduce:       ReduceConfig{Outputs: []string{"report.md"}},
		Tracks: []Track{
			{ID: "T1", Q: "Query 1", Kind: "deep"},
		},
	}
	planData, _ := yaml.Marshal(plan)
	if err := os.WriteFile(filepath.Join(runDir, "plan.yaml"), planData, 0644); err != nil {
		t.Fatal(err)
	}

	mapper := &DefaultMapper{RunsDir: tmpDir, Store: st}
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

func TestMapperWide(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "virmux-mapper-wide-*")
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "virmux.sqlite")
	st, _ := store.Open(dbPath)
	defer st.Close()

	runID := "wide-run"
	runDir := filepath.Join(tmpDir, runID)
	os.MkdirAll(runDir, 0755)
	st.StartRun(context.Background(), store.Run{ID: runID, Task: "test", StartedAt: time.Now()})

	plan := &Plan{
		PlanID:       "wide-plan",
		Goal:         "wide goal",
		DimsDidntAsk: []string{"d1", "d2", "d3", "d4"},
		Reduce:       ReduceConfig{Outputs: []string{"report.md"}},
		Tracks: []Track{
			{
				ID: "W1", Q: "Wide Query", Kind: "wide",
				Targets:  []string{"T1", "T2"},
				Attrs:    []string{"A1", "A2"},
				StopRule: "coverage>=0.5",
			},
		},
	}
	planData, _ := yaml.Marshal(plan)
	os.WriteFile(filepath.Join(runDir, "plan.yaml"), planData, 0644)

	mapper := &DefaultMapper{RunsDir: tmpDir, Store: st}
	// Test first run (no cache)
	_, err := mapper.Run(context.Background(), MapInput{RunID: runID, TrackID: "W1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(runDir, "map", "W1.jsonl"))
	lines := len(readFileLines(data))
	// T1xA1, T1xA2, T2xA1, T2xA2 => 4 cells. coverage>=0.5 means at least 2 cells.
	// After 1st cell (T1xA1), ratio is 0/4=0. 0 < 0.5.
	// After 2nd cell (T1xA2), ratio is 1/4=0.25. 0.25 < 0.5.
	// After 3rd cell (T2xA1), ratio is 2/4=0.5. 0.5 >= 0.5.
	// Loop BREAKS BEFORE processing T2xA1?
	// Let's check my code in mapper.go:
	// for _, cell := range cells {
	//    if ledger.Ratio() >= threshold { break }
	//    ... process cell ...
	//    ledger.Completed++
	// }
	// Initially Ratio=0. Process C1. Completed=1.
	// Next loop: Ratio=0.25. Process C2. Completed=2.
	// Next loop: Ratio=0.5. 0.5 >= 0.5. BREAK.
	// So 2 rows expected. Correct.

	if lines != 2 {
		t.Errorf("expected 2 rows due to coverage>=0.5, got %d", lines)
	}

	// Test cache hit
	_, err = mapper.Run(context.Background(), MapInput{RunID: runID, TrackID: "W1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data2, _ := os.ReadFile(filepath.Join(runDir, "map", "W1.jsonl"))
	if !strings.Contains(string(data2), "\"cache\":\"hit\"") {
		t.Errorf("expected cached result in rows")
	}
}

func TestDeepTrackDeterministicRows(t *testing.T) {
	t.Parallel()
	m := &DefaultMapper{}
	track := &Track{ID: "T1", Q: "same query", Kind: "deep"}
	out1, err := m.runDeepTrack(context.Background(), track)
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	out2, err := m.runDeepTrack(context.Background(), track)
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if !reflect.DeepEqual(out1.Rows, out2.Rows) {
		t.Fatalf("deep track rows must be deterministic\nout1=%#v\nout2=%#v", out1.Rows, out2.Rows)
	}
}

func readFileLines(data []byte) []string {
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
