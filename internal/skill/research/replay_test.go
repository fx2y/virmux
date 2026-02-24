package research

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fakeReplayScheduler struct {
	called bool
}

func (f *fakeReplayScheduler) Build(context.Context, ScheduleInput) (ScheduleOutput, error) {
	return ScheduleOutput{}, nil
}

func (f *fakeReplayScheduler) Run(ctx context.Context, plan *Plan, runID string, only []string) ([]TrackState, error) {
	f.called = true
	return nil, nil
}

func TestReplayRejectsUnknownOnlyTrack(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runID := "r1"
	runDir := filepath.Join(tmp, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planYAML := []byte(`
plan_id: p1
goal: q
dims_you_didnt_ask: [a]
tracks:
  - id: track-1
    q: foo
    kind: deep
reduce:
  outputs: [report.md]
`)
	if err := os.WriteFile(filepath.Join(runDir, "plan.yaml"), planYAML, 0o644); err != nil {
		t.Fatal(err)
	}

	scheduler := &fakeReplayScheduler{}
	r := &DefaultReplay{
		RunsDir:   tmp,
		Scheduler: scheduler,
	}

	_, err := r.Run(context.Background(), ReplayInput{RunID: runID, Only: []string{"missing-track"}})
	if err == nil {
		t.Fatalf("expected selector validation error")
	}
	f, ok := err.(Failure)
	if !ok {
		t.Fatalf("expected typed Failure, got %T (%v)", err, err)
	}
	if f.Code != FailureRerunSelector {
		t.Fatalf("expected %s, got %s", FailureRerunSelector, f.Code)
	}
	if scheduler.called {
		t.Fatalf("scheduler must not run on invalid selector")
	}
}
