package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateLine(t *testing.T) {
	t.Parallel()
	good := []byte(`{"ts":"2026-02-22T00:00:00Z","run_id":"r1","task":"vm:smoke","event":"run.started","payload":{}}`)
	if err := ValidateLine(good); err != nil {
		t.Fatalf("expected valid line: %v", err)
	}

	bad := []byte(`{"run_id":"r1"}`)
	if err := ValidateLine(bad); err == nil {
		t.Fatalf("expected invalid line")
	}
}

func TestWriterReopenAppends(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "trace.jsonl")

	w1, err := NewWriter(path)
	if err != nil {
		t.Fatalf("new writer #1: %v", err)
	}
	if err := w1.Emit("r1", "vm:smoke", "run.started", map[string]any{"n": 1}); err != nil {
		t.Fatalf("emit #1: %v", err)
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("close #1: %v", err)
	}

	w2, err := NewWriter(path)
	if err != nil {
		t.Fatalf("new writer #2: %v", err)
	}
	if err := w2.Emit("r1", "vm:smoke", "run.finished", map[string]any{"n": 2}); err != nil {
		t.Fatalf("emit #2: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("close #2: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 trace lines after reopen, got %d (%q)", len(lines), string(data))
	}
}
