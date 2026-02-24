package research

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/haris/virmux/internal/store"
	yaml "gopkg.in/yaml.v2"
)

type DefaultReplay struct {
	RunsDir   string
	Store     *store.Store
	Scheduler Scheduler
	Emitter   func(event string, payload map[string]any) error
}

func (r *DefaultReplay) emit(event string, payload map[string]any) {
	if r.Emitter != nil {
		_ = r.Emitter(event, payload)
	}
}

func (r *DefaultReplay) Run(ctx context.Context, input ReplayInput) (ReplayOutput, error) {
	// 1. Load plan from RunsDir/RunID/plan.yaml
	planPath := filepath.Join(r.RunsDir, input.RunID, "plan.yaml")
	planData, err := os.ReadFile(planPath)
	if err != nil {
		return ReplayOutput{}, fmt.Errorf("read plan: %w", err)
	}
	var plan Plan
	if err := yaml.Unmarshal(planData, &plan); err != nil {
		return ReplayOutput{}, fmt.Errorf("unmarshal plan: %w", err)
	}

	r.emit("research.replay.started", map[string]any{
		"run_id":  input.RunID,
		"plan_id": plan.PlanID,
		"only":    input.Only,
	})

	// 2. Determine which tracks to rerun
	var rerunIDs []string
	if len(input.Only) > 0 {
		rerunIDs = input.Only
	} else {
		failed, err := r.findFailedTracks(input.RunID, &plan)
		if err != nil {
			return ReplayOutput{}, err
		}
		for _, t := range failed {
			rerunIDs = append(rerunIDs, t.ID)
		}
	}

	if len(rerunIDs) == 0 {
		r.emit("research.replay.done", map[string]any{"run_id": input.RunID, "status": "no_tracks_to_rerun"})
		return ReplayOutput{RunID: input.RunID}, nil
	}

	// 3. Backup old map results for parity check
	mapDir := filepath.Join(r.RunsDir, input.RunID, "map")
	backupDir := filepath.Join(r.RunsDir, input.RunID, "map_backup")
	_ = os.MkdirAll(backupDir, 0755)
	for _, id := range rerunIDs {
		oldPath := filepath.Join(mapDir, id+".jsonl")
		if _, err := os.Stat(oldPath); err == nil {
			_ = os.Rename(oldPath, filepath.Join(backupDir, id+".jsonl"))
		}
	}

	// 4. Run scheduler
	states, err := r.Scheduler.Run(ctx, &plan, input.RunID, rerunIDs)
	if err != nil {
		return ReplayOutput{}, err
	}

	// 5. Parity check
	allMismatches := make(map[string][]string)
	trackMap := make(map[string]Track)
	for _, t := range plan.Tracks {
		trackMap[t.ID] = t
	}

	for _, id := range rerunIDs {
		track, ok := trackMap[id]
		if ok && track.Deterministic != nil && !*track.Deterministic {
			r.emit("research.replay.nondet_exception", map[string]any{
				"track_id": id,
			})
			continue
		}

		newPath := filepath.Join(mapDir, id+".jsonl")
		oldPath := filepath.Join(backupDir, id+".jsonl")
		
		if _, err := os.Stat(oldPath); err == nil {
			mismatches := r.compareFiles(oldPath, newPath)
			if len(mismatches) > 0 {
				allMismatches[id] = mismatches
				r.emit("research.replay.mismatch", map[string]any{
					"track_id":   id,
					"mismatches": mismatches,
				})
			}
		}
	}

	if len(allMismatches) > 0 {
		mismatchPath := filepath.Join(r.RunsDir, input.RunID, "mismatch.json")
		b, _ := json.MarshalIndent(allMismatches, "", "  ")
		_ = os.WriteFile(mismatchPath, b, 0644)
	}

	r.emit("research.replay.done", map[string]any{"run_id": input.RunID, "status": "ok", "results": states})
	return ReplayOutput{RunID: input.RunID}, nil
}

func (r *DefaultReplay) findFailedTracks(runID string, plan *Plan) ([]Track, error) {
	var failed []Track
	mapDir := filepath.Join(r.RunsDir, runID, "map")
	for _, t := range plan.Tracks {
		p := filepath.Join(mapDir, t.ID+".jsonl")
		if _, err := os.Stat(p); os.IsNotExist(err) {
			failed = append(failed, t)
			continue
		}
	}
	return failed, nil
}

func (r *DefaultReplay) compareFiles(oldPath, newPath string) []string {
	oldLines := r.readRows(oldPath)
	newLines := r.readRows(newPath)
	
	var mismatches []string
	if len(oldLines) != len(newLines) {
		mismatches = append(mismatches, fmt.Sprintf("row count mismatch: old=%d, new=%d", len(oldLines), len(newLines)))
		return mismatches
	}

	for i := range oldLines {
		// Compare ignoring evidence_ids
		delete(oldLines[i], "evidence_ids")
		delete(newLines[i], "evidence_ids")
		
		if !reflect.DeepEqual(oldLines[i], newLines[i]) {
			mismatches = append(mismatches, fmt.Sprintf("row %d data mismatch", i))
		}
	}
	return mismatches
}

func (r *DefaultReplay) readRows(path string) []map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var rows []map[string]any
	for _, line := range lines {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err == nil {
			rows = append(rows, row)
		}
	}
	return rows
}
