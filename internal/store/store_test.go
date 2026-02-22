package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreSchemaAndFK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "virmux.sqlite")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	run := Run{
		ID:        "run-1",
		Task:      "vm:smoke",
		Label:     "test",
		ImageSHA:  "abc",
		StartedAt: time.Now(),
	}
	if err := s.StartRun(ctx, run); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := s.InsertEvent(ctx, run.ID, "run.started", `{}`, time.Now()); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if err := s.InsertEvent(ctx, "missing", "bad", `{}`, time.Now()); err == nil {
		t.Fatalf("expected fk error for unknown run_id")
	}
	if err := s.FinishRun(ctx, run.ID, "ok", 10, 0, "runs/run-1/trace.jsonl", time.Now()); err != nil {
		t.Fatalf("finish run: %v", err)
	}
}
