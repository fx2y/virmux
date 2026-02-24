package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/haris/virmux/internal/store"
)

type MapResultRow struct {
	TrackID     string         `json:"track_id"`
	OK          bool           `json:"ok"`
	Data        map[string]any `json:"data,omitempty"`
	Error       string         `json:"error,omitempty"`
	Evidence    []string       `json:"evidence,omitempty"`
	EvidenceIDs []int64        `json:"evidence_ids,omitempty"`
}

type MapTrackOutput struct {
	TrackID       string         `json:"track_id"`
	FailureReason string         `json:"failure_reason,omitempty"`
	NextQueries   []string       `json:"next_queries,omitempty"`
	Rows          []MapResultRow `json:"rows"`
}

// GridCell represents a single target x attribute combination.
type GridCell struct {
	Target string `json:"target"`
	Attr   string `json:"attr"`
}

// CoverageLedger tracks the status of wide-search grid cells.
type CoverageLedger struct {
	Total      int               `json:"total"`
	Completed  int               `json:"completed"`
	Missing    []string          `json:"missing"`
	CellStatus map[string]string `json:"cell_status"` // cell_id -> status (found|missing|conflict)
}

func (c *CoverageLedger) Ratio() float64 {
	if c.Total == 0 {
		return 0
	}
	return float64(c.Completed) / float64(c.Total)
}

// CacheEntry stores a cached retrieval result.
type CacheEntry struct {
	QueryHash string         `json:"query_hash"`
	URL       string         `json:"url"`
	Data      map[string]any `json:"data"`
	Evidence  []string       `json:"evidence"`
}

type DefaultMapper struct {
	RunsDir  string
	CacheDir string
	Store    *store.Store
	Cache    sync.Map // (query_hash+url) -> CacheEntry (in-memory overlay)
}

func (m *DefaultMapper) getCacheKey(query, url string) string {
	sum := sha256.Sum256([]byte(query + "\n" + url))
	return hex.EncodeToString(sum[:])
}

func (m *DefaultMapper) Run(ctx context.Context, input MapInput) (MapOutput, error) {
	// 1. Load plan from RunsDir/RunID/plan.yaml
	planPath := filepath.Join(m.RunsDir, input.RunID, "plan.yaml")
	planData, err := os.ReadFile(planPath)
	if err != nil {
		return MapOutput{}, fmt.Errorf("read plan: %w", err)
	}
	plan, err := ParsePlan(planData)
	if err != nil {
		return MapOutput{}, err
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

	// 3. Execute the track
	var output MapTrackOutput
	if track.Kind == "wide" {
		output, err = m.runWideTrack(ctx, track)
	} else {
		output, err = m.runDeepTrack(ctx, track)
	}
	if err != nil {
		return MapOutput{}, err
	}

	// 3.5 Resolve and Link Evidence
	if m.Store != nil {
		for i := range output.Rows {
			row := &output.Rows[i]
			for _, evStr := range row.Evidence {
				// Simple heuristic: if it looks like a URL, use it as URL, otherwise as claim
				claim := "Claim from " + row.TrackID
				url := ""
				if strings.HasPrefix(evStr, "http") {
					url = evStr
				} else {
					claim = evStr
				}
				evID, err := m.Store.InsertEvidence(ctx, store.Evidence{
					RunID:      input.RunID,
					Claim:      claim,
					URL:        url,
					TS:         time.Now(),
					SourceHash: m.getCacheKey(evStr, ""),
				})
				if err == nil {
					row.EvidenceIDs = append(row.EvidenceIDs, evID)
					_ = m.Store.InsertRowEvidence(ctx, store.RowEvidence{
						RunID:      input.RunID,
						TrackID:    row.TrackID,
						RowIdx:     i,
						EvidenceID: evID,
					})
				}
			}
		}
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

func (m *DefaultMapper) runWideTrack(ctx context.Context, track *Track) (MapTrackOutput, error) {
	output := MapTrackOutput{TrackID: track.ID}
	// Wide search grid expansion
	cells := make([]GridCell, 0, len(track.Targets)*len(track.Attrs))
	for _, target := range track.Targets {
		for _, attr := range track.Attrs {
			cells = append(cells, GridCell{Target: target, Attr: attr})
		}
	}

	ledger := CoverageLedger{
		Total:      len(cells),
		CellStatus: make(map[string]string),
	}

	// Parse stop_rule (e.g. coverage>=0.8)
	threshold := 0.8
	if strings.HasPrefix(track.StopRule, "coverage>=") {
		if val, err := strconv.ParseFloat(track.StopRule[10:], 64); err == nil {
			threshold = val
		}
	}

	for _, cell := range cells {
		if ledger.Ratio() >= threshold {
			break
		}
		cellID := fmt.Sprintf("%s:%s", cell.Target, cell.Attr)

		// Cache check
		cacheKey := m.getCacheKey(track.Q, cellID)
		cached := m.checkCache(cacheKey)
		if cached != nil {
			ledger.Completed++
			ledger.CellStatus[cellID] = "found"
			data := make(map[string]any)
			for k, v := range cached.Data {
				data[k] = v
			}
			data["cache"] = "hit"
			output.Rows = append(output.Rows, MapResultRow{
				TrackID:  track.ID,
				OK:       true,
				Data:     data,
				Evidence: cached.Evidence,
			})
			continue
		}

		// Mock tool call for wide search
		row := MapResultRow{
			TrackID: track.ID,
			OK:      true,
			Data:    map[string]any{"target": cell.Target, "attr": cell.Attr, "result": fmt.Sprintf("result for %s", cellID)},
		}
		if ledger.Completed%3 != 0 {
			row.Evidence = []string{fmt.Sprintf("https://example.com/source/%s", cellID)}
		}
		output.Rows = append(output.Rows, row)
		ledger.Completed++
		ledger.CellStatus[cellID] = "found"

		// Populate cache
		m.saveCache(cacheKey, CacheEntry{QueryHash: cacheKey, Data: row.Data})
	}
	return output, nil
}

func (m *DefaultMapper) runDeepTrack(ctx context.Context, track *Track) (MapTrackOutput, error) {
	return MapTrackOutput{
		TrackID: track.ID,
		Rows: []MapResultRow{
			{
				TrackID:  track.ID,
				OK:       true,
				Data:     map[string]any{"result": fmt.Sprintf("stub result for %s", track.Q)},
				Evidence: []string{"https://example.com/deep/result"},
			},
		},
	}, nil
}

func (m *DefaultMapper) checkCache(key string) *CacheEntry {
	if val, ok := m.Cache.Load(key); ok {
		return val.(*CacheEntry)
	}
	if m.CacheDir != "" {
		p := filepath.Join(m.CacheDir, key+".json")
		if b, err := os.ReadFile(p); err == nil {
			var ce CacheEntry
			if err := json.Unmarshal(b, &ce); err == nil {
				m.Cache.Store(key, &ce)
				return &ce
			}
		}
	}
	return nil
}

func (m *DefaultMapper) saveCache(key string, ce CacheEntry) {
	m.Cache.Store(key, &ce)
	if m.CacheDir != "" {
		_ = os.MkdirAll(m.CacheDir, 0755)
		p := filepath.Join(m.CacheDir, key+".json")
		b, _ := json.Marshal(ce)
		_ = os.WriteFile(p, b, 0644)
	}
}
