package research

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/haris/virmux/internal/store"
)

type DefaultReducer struct {
	RunsDir string
	Store   *store.Store
}

func (r *DefaultReducer) Run(ctx context.Context, input ReduceInput) (ReduceOutput, error) {
	// 1. Gather all map rows
	mapDir := filepath.Join(r.RunsDir, input.RunID, "map")
	ents, err := os.ReadDir(mapDir)
	if err != nil {
		return ReduceOutput{}, fmt.Errorf("read map dir: %w", err)
	}

	var allRows []MapResultRow
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(mapDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var row MapResultRow
			if err := json.Unmarshal([]byte(line), &row); err == nil {
				allRows = append(allRows, row)
			}
		}
	}

	// 2. Filter/Demote uncited rows
	var cited []MapResultRow
	var uncited []MapResultRow
	for _, row := range allRows {
		if len(row.EvidenceIDs) > 0 {
			cited = append(cited, row)
		} else {
			uncited = append(uncited, row)
		}
	}

	// 3. Generate artifacts
	reduceDir := filepath.Join(r.RunsDir, input.RunID, "reduce")
	if err := os.MkdirAll(reduceDir, 0755); err != nil {
		return ReduceOutput{}, fmt.Errorf("mkdir reduce: %w", err)
	}

	mismatches := make(map[string][]string)
	mismatchPath := filepath.Join(r.RunsDir, input.RunID, "mismatch.json")
	if b, err := os.ReadFile(mismatchPath); err == nil {
		_ = json.Unmarshal(b, &mismatches)
	}

	if err := r.writeCSV(reduceDir, cited); err != nil {
		return ReduceOutput{}, err
	}
	if err := r.writeReport(reduceDir, cited, uncited, mismatches); err != nil {
		return ReduceOutput{}, err
	}
	if err := r.writeSlides(reduceDir, cited); err != nil {
		return ReduceOutput{}, err
	}

	return ReduceOutput{RunID: input.RunID}, nil
}

func (r *DefaultReducer) writeCSV(dir string, rows []MapResultRow) error {
	f, err := os.Create(filepath.Join(dir, "table.csv"))
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	// Extract headers from Data keys
	headerMap := make(map[string]struct{})
	for _, row := range rows {
		for k := range row.Data {
			headerMap[k] = struct{}{}
		}
	}
	headers := []string{"track_id"}
	for k := range headerMap {
		headers = append(headers, k)
	}
	sort.Strings(headers[1:])

	if err := w.Write(headers); err != nil {
		return err
	}

	for _, row := range rows {
		record := make([]string, len(headers))
		record[0] = row.TrackID
		for i := 1; i < len(headers); i++ {
			if val, ok := row.Data[headers[i]]; ok {
				record[i] = fmt.Sprintf("%v", val)
			}
		}
		if err := w.Write(record); err != nil {
			return err
		}
	}
	return nil
}

func (r *DefaultReducer) writeReport(dir string, cited, uncited []MapResultRow, mismatches map[string][]string) error {
	f, err := os.Create(filepath.Join(dir, "report.md"))
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "# Research Report\n\n")
	fmt.Fprintf(f, "## Executive Summary\n\n")
	if len(cited) > 0 {
		fmt.Fprintf(f, "Found %d cited claims.\n\n", len(cited))
	} else {
		fmt.Fprintf(f, "No cited claims found.\n\n")
	}

	fmt.Fprintf(f, "## Contradictions\n\n")
	if len(mismatches) > 0 {
		fmt.Fprintf(f, "The following tracks showed mismatches during replay:\n\n")
		for trackID, ms := range mismatches {
			fmt.Fprintf(f, "- **%s**: %s\n", trackID, strings.Join(ms, "; "))
		}
	} else {
		fmt.Fprintf(f, "None.\n")
	}
	fmt.Fprintf(f, "\n")

	fmt.Fprintf(f, "## Findings\n\n")
	for _, row := range cited {
		fmt.Fprintf(f, "- **%s**: %v\n", row.TrackID, row.Data)
		if len(row.Evidence) > 0 {
			fmt.Fprintf(f, "  - *Evidence*: %s\n", strings.Join(row.Evidence, ", "))
		}
	}

	if len(uncited) > 0 {
		fmt.Fprintf(f, "\n## Quality Section (Uncited Data)\n\n")
		fmt.Fprintf(f, "The following %d rows were excluded due to lack of evidence:\n\n", len(uncited))
		for _, row := range uncited {
			fmt.Fprintf(f, "- %s: %v\n", row.TrackID, row.Data)
		}
	}

	return nil
}

func (r *DefaultReducer) writeSlides(dir string, rows []MapResultRow) error {
	f, err := os.Create(filepath.Join(dir, "slides.md"))
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "# Research Findings\n\n---\n\n")
	for i, row := range rows {
		fmt.Fprintf(f, "## Slide %d: %s\n\n", i+1, row.TrackID)
		fmt.Fprintf(f, "%v\n\n---\n\n", row.Data)
	}
	return nil
}
