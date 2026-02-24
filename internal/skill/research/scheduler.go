package research

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

type DefaultScheduler struct {
	MaxConcurrency      int64
	Mapper              Mapper
	Emitter             func(event string, payload map[string]any) error
	TrackArtifactExists func(runID, trackID string) (bool, error)
}

func (s *DefaultScheduler) isRetryable(err error, res MapOutput) bool {
	if res.Retryable {
		return true
	}
	if err == nil {
		return false
	}
	// Check for common retryable error patterns (stubbed)
	return false
}

func (s *DefaultScheduler) emit(event string, payload map[string]any) {
	if s.Emitter != nil {
		_ = s.Emitter(event, payload)
	}
}

func (s *DefaultScheduler) hasTrackArtifact(runID, trackID string) (bool, error) {
	if s.TrackArtifactExists == nil {
		return false, nil
	}
	return s.TrackArtifactExists(runID, trackID)
}

func (s *DefaultScheduler) Build(ctx context.Context, input ScheduleInput) (ScheduleOutput, error) {
	// For now, this just returns the PlanID.
	// In a more complex implementation, it might pre-calculate the batches.
	return ScheduleOutput{PlanID: input.PlanID}, nil
}

// Run executes the plan's tracks according to their dependencies.
func (s *DefaultScheduler) Run(ctx context.Context, plan *Plan, runID string, only []string) ([]TrackState, error) {
	if s.MaxConcurrency <= 0 {
		s.MaxConcurrency = 4
	}
	sem := semaphore.NewWeighted(s.MaxConcurrency)

	return s.runConcurrent(ctx, plan, runID, sem, only)
}

func (s *DefaultScheduler) runConcurrent(ctx context.Context, plan *Plan, runID string, sem *semaphore.Weighted, only []string) ([]TrackState, error) {
	states := make(map[string]*TrackState)
	adj := make(map[string][]string)
	inDegree := make(map[string]int)

	onlyMap := make(map[string]bool)
	for _, id := range only {
		onlyMap[id] = true
	}

	for _, t := range plan.Tracks {
		states[t.ID] = &TrackState{TrackID: t.ID, Status: TrackPending}
		for _, dep := range t.Deps {
			adj[dep] = append(adj[dep], t.ID)
			inDegree[t.ID]++
		}
	}

	if len(onlyMap) > 0 {
		for id := range onlyMap {
			if _, ok := states[id]; !ok {
				return nil, Failure{Code: FailureRerunSelector, Message: fmt.Sprintf("track %q not in plan", id)}
			}
		}
	}

	completedCount := 0

	// If we are only running a subset, mark everything else as "Done" if it was already successful
	if len(onlyMap) > 0 {
		for _, t := range plan.Tracks {
			if !onlyMap[t.ID] {
				ok, err := s.hasTrackArtifact(runID, t.ID)
				if err != nil {
					return nil, err
				}
				if ok {
					states[t.ID].Status = TrackDone
					// Update inDegree for dependents
					for _, dependent := range adj[t.ID] {
						inDegree[dependent]--
					}
				} else {
					states[t.ID].Status = TrackBlocked
					states[t.ID].Error = "not in rerun set and no artifact found"
				}
				completedCount++
			}
		}
	}

	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	g, ctx := errgroup.WithContext(ctx)

	totalTracks := len(plan.Tracks)
	runningCount := 0

	for {
		mu.Lock()
		if completedCount == totalTracks {
			mu.Unlock()
			break
		}

		var ready []string
		for _, t := range plan.Tracks {
			id := t.ID
			state := states[id]
			if state.Status == TrackPending && inDegree[id] == 0 {
				ready = append(ready, id)
				state.Status = TrackRunning
			}
		}
		runningCount += len(ready)
		mu.Unlock()

		if len(ready) == 0 {
			mu.Lock()
			if completedCount < totalTracks {
				if runningCount == 0 {
					for _, t := range plan.Tracks {
						st := states[t.ID]
						if st.Status != TrackPending {
							continue
						}
						st.Status = TrackBlocked
						st.Error = fmt.Sprintf("unsatisfied dependencies: %v", t.Deps)
						completedCount++
						s.emit("research.map.track.blocked", map[string]any{"track_id": t.ID, "reason": "unsatisfied_dependencies"})
					}
					mu.Unlock()
					continue
				}
				cond.Wait()
				mu.Unlock()
				continue
			}
			mu.Unlock()
			break
		}

		for _, id := range ready {
			trackID := id
			g.Go(func() error {
				if err := sem.Acquire(ctx, 1); err != nil {
					mu.Lock()
					defer mu.Unlock()
					if states[trackID].Status == TrackRunning {
						states[trackID].Status = TrackFailed
						states[trackID].Error = err.Error()
						completedCount++
					}
					runningCount--
					cond.Broadcast()
					return err
				}
				defer sem.Release(1)

				// Initial attempt
				s.emit("research.map.track.started", map[string]any{"track_id": trackID, "run_id": runID})
				res, err := s.Mapper.Run(ctx, MapInput{RunID: runID, TrackID: trackID})

				// Basic retry once for timeout or no results (stubbed condition)
				if (err != nil || res.Retryable) && s.isRetryable(err, res) {
					s.emit("research.map.track.retry", map[string]any{
						"track_id":  trackID,
						"run_id":    runID,
						"error":     fmt.Sprintf("%v", err),
						"retryable": res.Retryable,
					})
					res, err = s.Mapper.Run(ctx, MapInput{RunID: runID, TrackID: trackID})
				}

				mu.Lock()
				defer mu.Unlock()

				if err != nil {
					states[trackID].Status = TrackFailed
					states[trackID].Error = err.Error()
					s.emit("research.map.track.failed", map[string]any{"track_id": trackID, "error": err.Error()})

					// Cascade failure to dependents
					var cascade func(string)
					cascade = func(id string) {
						for _, depID := range adj[id] {
							if states[depID].Status == TrackPending {
								states[depID].Status = TrackBlocked
								states[depID].Error = fmt.Sprintf("dependency %s failed", id)
								s.emit("research.map.track.blocked", map[string]any{"track_id": depID, "dependency": id})
								completedCount++
								cascade(depID)
							}
						}
					}
					cascade(trackID)
				} else {
					states[trackID].Status = TrackDone
					s.emit("research.map.track.done", map[string]any{"track_id": trackID})
					for _, dependent := range adj[trackID] {
						inDegree[dependent]--
					}
				}

				runningCount--
				completedCount++
				cond.Broadcast()
				return nil
			})
		}
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	result := make([]TrackState, 0, len(states))
	for _, id := range plan.Tracks {
		result = append(result, *states[id.ID])
	}

	return result, nil
}
