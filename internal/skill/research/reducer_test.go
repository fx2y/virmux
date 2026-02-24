package research

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haris/virmux/internal/store"
)

func TestReducerRun(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "virmux-reducer-test-*")
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "virmux.sqlite")
	st, _ := store.Open(dbPath)
	defer st.Close()

	runID := "reduce-run"
	runDir := filepath.Join(tmpDir, runID)
	os.MkdirAll(runDir, 0755)
	st.StartRun(context.Background(), store.Run{ID: runID, Task: "test", StartedAt: time.Now()})

	// Create mock map file
	mapDir := filepath.Join(runDir, "map")
	os.MkdirAll(mapDir, 0755)
	rows := []MapResultRow{
		{TrackID: "T1", OK: true, Data: map[string]any{"res": "cited"}, EvidenceIDs: []int64{1}, Evidence: []string{"source1"}},
		{TrackID: "T2", OK: true, Data: map[string]any{"res": "uncited"}},
	}
	f, _ := os.Create(filepath.Join(mapDir, "T1.jsonl"))
	for _, r := range rows {
		b, _ := json.Marshal(r)
		f.Write(append(b, '\n'))
	}
	f.Close()

	reducer := &DefaultReducer{RunsDir: tmpDir, Store: st}
	_, err := reducer.Run(context.Background(), ReduceInput{RunID: runID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify artifacts
	reduceDir := filepath.Join(runDir, "reduce")
	if _, err := os.Stat(filepath.Join(reduceDir, "table.csv")); os.IsNotExist(err) {
		t.Error("table.csv not created")
	}
	if _, err := os.Stat(filepath.Join(reduceDir, "report.md")); os.IsNotExist(err) {
		t.Error("report.md not created")
	}
	if _, err := os.Stat(filepath.Join(reduceDir, "slides.md")); os.IsNotExist(err) {
		t.Error("slides.md not created")
	}

	report, _ := os.ReadFile(filepath.Join(reduceDir, "report.md"))
	if !strings.Contains(string(report), "Found 1 cited claims") {
		t.Error("report should have 1 cited claim")
	}
	if !strings.Contains(string(report), "Quality Section (Uncited Data)") {
		t.Error("report should have quality section for uncited data")
	}
}
