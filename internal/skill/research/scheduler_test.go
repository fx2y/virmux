package research

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
)

type mockMapper struct {
	mu      sync.Mutex
	history []string
}

func (m *mockMapper) Run(ctx context.Context, input MapInput) (MapOutput, error) {
	m.mu.Lock()
	m.history = append(m.history, input.TrackID)
	m.mu.Unlock()
	return MapOutput{RunID: input.RunID, TrackID: input.TrackID}, nil
}

func TestSchedulerTopo(t *testing.T) {
	mapper := &mockMapper{}
	s := &DefaultScheduler{
		MaxConcurrency: 4,
		Mapper:         mapper,
	}

	plan := &Plan{
		PlanID: "test-plan",
		Tracks: []Track{
			{ID: "A", Deps: []string{}},
			{ID: "B", Deps: []string{"A"}},
			{ID: "C", Deps: []string{"A"}},
			{ID: "D", Deps: []string{"B", "C"}},
		},
	}

	states, err := s.Run(context.Background(), plan, "test-run", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(states) != 4 {
		t.Errorf("expected 4 states, got %d", len(states))
	}

	// Verify order
	// A must be first. D must be last. B and C can be in any order but after A and before D.
	history := mapper.history
	if len(history) != 4 {
		t.Fatalf("expected 4 tracks executed, got %d", len(history))
	}

	if history[0] != "A" {
		t.Errorf("A should be executed first, got %s", history[0])
	}
	if history[3] != "D" {
		t.Errorf("D should be executed last, got %s", history[3])
	}

	// B and C check
	mid := history[1:3]
	sort.Strings(mid)
	if mid[0] != "B" || mid[1] != "C" {
		t.Errorf("B and C should be executed between A and D, got %v", mid)
	}
}

func TestSchedulerFailure(t *testing.T) {
	failMapper := &errorMapper{failID: "B"}
	s := &DefaultScheduler{
		MaxConcurrency: 4,
		Mapper:         failMapper,
	}

	plan := &Plan{
		PlanID: "test-plan",
		Tracks: []Track{
			{ID: "A", Deps: []string{}},
			{ID: "B", Deps: []string{"A"}},
			{ID: "C", Deps: []string{"B"}},
		},
	}

	states, _ := s.Run(context.Background(), plan, "test-run", nil)
	// Since we use errgroup, the first error cancels the context of others.
	// But our scheduler loop might have already started some.

	foundFailed := false
	for _, st := range states {
		if st.TrackID == "B" && st.Status == TrackFailed {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Errorf("expected track B to be failed in states")
	}

	// Track C should remain blocked because B failed
	foundBlockedC := false
	for _, st := range states {
		if st.TrackID == "C" && st.Status == TrackBlocked {
			foundBlockedC = true
		}
	}
	if !foundBlockedC {
		t.Errorf("expected track C to be blocked")
	}
}

type errorMapper struct {
	failID string
}

func (m *errorMapper) Run(ctx context.Context, input MapInput) (MapOutput, error) {
	if input.TrackID == m.failID {
		return MapOutput{}, fmt.Errorf("injected error for %s", input.TrackID)
	}
	return MapOutput{RunID: input.RunID, TrackID: input.TrackID}, nil
}
