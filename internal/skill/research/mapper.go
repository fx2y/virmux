package research

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	yaml "gopkg.in/yaml.v2"
)

type MapResultRow struct {
	TrackID    string         `json:"track_id"`
	OK         bool           `json:"ok"`
	Data       map[string]any `json:"data,omitempty"`
	Error      string         `json:"error,omitempty"`
	Evidence   []string       `json:"evidence,omitempty"`
}

type MapTrackOutput struct {
	TrackID       string         `json:"track_id"`
	FailureReason string         `json:"failure_reason,omitempty"`
	NextQueries   []string       `json:"next_queries,omitempty"`
	Rows          []MapResultRow `json:"rows"`
}

type DefaultMapper struct {
	RunsDir string
}

func (m *DefaultMapper) Run(ctx context.Context, input MapInput) (MapOutput, error) {
	// 1. Load plan from RunsDir/RunID/plan.yaml
	planPath := filepath.Join(m.RunsDir, input.RunID, "plan.yaml")
	planData, err := os.ReadFile(planPath)
	if err != nil {
		return MapOutput{}, fmt.Errorf("read plan: %w", err)
	}
	var plan Plan
	if err := yaml.Unmarshal(planData, &plan); err != nil {
		return MapOutput{}, fmt.Errorf("unmarshal plan: %w", err)
	}

	// 2. Find the track
	var track *Track
	for _, t := range plan.Tracks {
		if t.ID == input.TrackID {
			track = &t
			break
		}
	}
	if track == nil {
		return MapOutput{}, fmt.Errorf("track %s not found in plan", input.TrackID)
	}

	// 3. Execute the track (for C2, we'll just stub the tool execution for now
	// or call a provided Runner if we have one. Since we are in the internal package,
	// we shouldn't depend on the VM/main logic directly. 
	// The caller should provide a way to run the track. 
	// But let's follow the Deliverables of C2: "worker IO schema", "map file writer".

	// Mocking track execution for now to satisfy C2 requirements of "virmux research map"
	// In the real implementation, this would call agentd tools.

	output := MapTrackOutput{
		TrackID: track.ID,
		Rows: []MapResultRow{
			{
				TrackID: track.ID,
				OK:      true,
				Data:    map[string]any{"result": fmt.Sprintf("stub result for %s", track.Q)},
			},
		},
	}

	// 4. Write runs/<id>/map/<track>.jsonl
	mapDir := filepath.Join(m.RunsDir, input.RunID, "map")
	if err := os.MkdirAll(mapDir, 0755); err != nil {
		return MapOutput{}, fmt.Errorf("mkdir map: %w", err)
	}
	trackPath := filepath.Join(mapDir, fmt.Sprintf("%s.jsonl", track.ID))
	f, err := os.Create(trackPath)
	if err != nil {
		return MapOutput{}, fmt.Errorf("create track map file: %w", err)
	}
	defer f.Close()

	for _, row := range output.Rows {
		b, _ := json.Marshal(row)
		if _, err := f.Write(append(b, '\n')); err != nil {
			return MapOutput{}, fmt.Errorf("write track row: %w", err)
		}
	}

	return MapOutput{RunID: input.RunID, TrackID: input.TrackID}, nil
}
