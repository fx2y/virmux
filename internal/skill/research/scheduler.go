package research

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

type DefaultScheduler struct {
	MaxConcurrency int64
	Mapper         Mapper
	Emitter        func(event string, payload map[string]any) error
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

	// If we are only running a subset, mark everything else as "Done" if it was already successful
	if len(onlyMap) > 0 {
		for _, t := range plan.Tracks {
			if !onlyMap[t.ID] {
				// Check if artifact exists
				mapDir := filepath.Join(s.Mapper.(*DefaultMapper).RunsDir, runID, "map")
				trackPath := filepath.Join(mapDir, fmt.Sprintf("%s.jsonl", t.ID))
				if _, err := os.Stat(trackPath); err == nil {
					states[t.ID].Status = TrackDone
					// Update inDegree for dependents
					for _, dependent := range adj[t.ID] {
						inDegree[dependent]--
					}
				} else {
					// It's not in our 'only' list AND it's not already done.
					// This might be an error or we should just treat it as blocked?
					// For replay, usually we only replay what we want.
					states[t.ID].Status = TrackBlocked
					states[t.ID].Error = "not in rerun set and no artifact found"
				}
			}
		}
	}

	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	g, ctx := errgroup.WithContext(ctx)

	completedCount := 0
	totalTracks := len(plan.Tracks)

	// Count already Done/Blocked tracks
	for _, state := range states {
		if state.Status == TrackDone || state.Status == TrackBlocked {
			completedCount++
		}
	}

	for {
		mu.Lock()
		if completedCount == totalTracks {
			mu.Unlock()
			break
		}

		var ready []string
		for id, state := range states {
			if state.Status == TrackPending && inDegree[id] == 0 {
				ready = append(ready, id)
				state.Status = TrackRunning
			}
		}
		mu.Unlock()

		if len(ready) == 0 {
			mu.Lock()
			if completedCount < totalTracks {
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
					return err
				}
				defer sem.Release(1)

				// Initial attempt
				s.emit("research.map.track.started", map[string]any{"track_id": trackID, "run_id": runID})
				res, err := s.Mapper.Run(ctx, MapInput{RunID: runID, TrackID: trackID})
				
				// Basic retry once for timeout or no results (stubbed condition)
				if (err != nil || res.Retryable) && s.isRetryable(err, res) {
					s.emit("research.map.track.retry", map[string]any{
						"track_id": trackID, 
						"run_id": runID, 
						"error": fmt.Sprintf("%v", err),
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

				completedCount++
				cond.Broadcast()
				return nil
			})
		}
	}

	_ = g.Wait()
	
	result := make([]TrackState, 0, len(states))
	for _, id := range plan.Tracks {
		result = append(result, *states[id.ID])
	}

	return result, nil
}
