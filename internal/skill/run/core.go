package run

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/haris/virmux/internal/store"
	"github.com/haris/virmux/internal/trace"
)

type Budget struct {
	ToolCalls int `json:"tool_calls"`
	Seconds   int `json:"seconds"`
	Tokens    int `json:"tokens"`
}

type Fixture struct {
	ID            string         `json:"id"`
	Tool          string         `json:"tool"`
	Cmd           string         `json:"cmd"`
	Args          map[string]any `json:"args"`
	Deterministic bool           `json:"deterministic"`
	Input         map[string]any `json:"input,omitempty"`
	Expect        map[string]any `json:"expect,omitempty"`
}

type BudgetTracker struct {
	Max     Budget
	started time.Time
	used    struct {
		ToolCalls int
		Tokens    int
	}
}

type BudgetError struct {
	Kind string
	Msg  string
}

func (e BudgetError) Error() string {
	if strings.TrimSpace(e.Msg) == "" {
		return "BUDGET_EXCEEDED"
	}
	return "BUDGET_EXCEEDED: " + e.Msg
}

func LoadFixture(path string) (Fixture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, fmt.Errorf("read fixture: %w", err)
	}
	var f Fixture
	if err := json.Unmarshal(b, &f); err != nil {
		return Fixture{}, fmt.Errorf("parse fixture json: %w", err)
	}
	if strings.TrimSpace(f.ID) == "" {
		return Fixture{}, errors.New("fixture.id required")
	}
	if f.Tool == "" {
		f.Tool = "shell.exec"
	}
	if f.Args == nil {
		f.Args = map[string]any{}
	}
	if f.Tool == "shell.exec" && strings.TrimSpace(f.Cmd) != "" {
		if _, ok := f.Args["cmd"]; !ok {
			f.Args["cmd"] = f.Cmd
		}
	}
	if _, ok := f.Args["cmd"]; f.Tool == "shell.exec" && !ok {
		return Fixture{}, errors.New("fixture shell.exec requires cmd or args.cmd")
	}
	return f, nil
}

func NewBudgetTracker(b Budget, started time.Time) *BudgetTracker {
	if started.IsZero() {
		started = time.Now().UTC()
	}
	return &BudgetTracker{Max: b, started: started}
}

func (b *BudgetTracker) BeforeToolCall(tool string) error {
	_ = tool
	if b == nil {
		return nil
	}
	if b.Max.ToolCalls > 0 && b.used.ToolCalls+1 > b.Max.ToolCalls {
		return BudgetError{Kind: "tool_calls", Msg: fmt.Sprintf("tool_calls exceeded (%d>%d)", b.used.ToolCalls+1, b.Max.ToolCalls)}
	}
	if b.Max.ToolCalls == 0 {
		return BudgetError{Kind: "tool_calls", Msg: "tool_calls exceeded (1>0)"}
	}
	b.used.ToolCalls++
	return nil
}

func (b *BudgetTracker) CheckElapsed(now time.Time) error {
	if b == nil || b.Max.Seconds <= 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if now.Sub(b.started) > time.Duration(b.Max.Seconds)*time.Second {
		return BudgetError{Kind: "seconds", Msg: fmt.Sprintf("seconds exceeded (> %ds)", b.Max.Seconds)}
	}
	return nil
}

func (b *BudgetTracker) CountToken(n int) error {
	if b == nil || b.Max.Tokens <= 0 {
		return nil
	}
	if n < 0 {
		n = 0
	}
	if b.used.Tokens+n > b.Max.Tokens {
		return BudgetError{Kind: "tokens", Msg: fmt.Sprintf("tokens exceeded (%d>%d)", b.used.Tokens+n, b.Max.Tokens)}
	}
	b.used.Tokens += n
	return nil
}

type ToolHashRow struct {
	Seq        int64  `json:"seq"`
	Tool       string `json:"tool"`
	InputHash  string `json:"input_hash"`
	OutputHash string `json:"output_hash"`
	ErrorCode  string `json:"error_code"`
}

type ReplayReport struct {
	RunID     string        `json:"run_id"`
	TracePath string        `json:"trace_path"`
	ToolCalls int           `json:"tool_calls"`
	Verified  bool          `json:"verified"`
	Rows      []ToolHashRow `json:"rows,omitempty"`
}

func VerifyReplayHashes(dbPath, runDir, runID string) (ReplayReport, error) {
	tracePath := filepath.Join(runDir, "trace.ndjson")
	traceRows, err := loadTraceToolHashes(tracePath)
	if err != nil {
		return ReplayReport{}, err
	}
	fileRows, err := loadToolIOHashes(runDir)
	if err != nil {
		return ReplayReport{}, err
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return ReplayReport{}, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	dbRows, err := loadDBToolHashes(st, runID)
	if err != nil {
		return ReplayReport{}, err
	}
	if len(dbRows) != len(fileRows) {
		return ReplayReport{}, fmt.Errorf("replay mismatch count db=%d file=%d", len(dbRows), len(fileRows))
	}
	if len(dbRows) != len(traceRows) {
		return ReplayReport{}, fmt.Errorf("replay mismatch count db=%d trace=%d", len(dbRows), len(traceRows))
	}
	for i := range dbRows {
		d, f := dbRows[i], fileRows[i]
		if d.Seq != f.Seq || d.Tool != f.Tool || d.InputHash != f.InputHash || d.OutputHash != f.OutputHash {
			return ReplayReport{}, fmt.Errorf("replay mismatch seq=%d db=%+v file=%+v", d.Seq, d, f)
		}
		if traceRows[i].Seq != d.Seq || traceRows[i].Tool != d.Tool || traceRows[i].InputHash != d.InputHash {
			return ReplayReport{}, fmt.Errorf("replay mismatch trace seq=%d trace=%+v db=%+v", d.Seq, traceRows[i], d)
		}
	}
	return ReplayReport{
		RunID: runID, TracePath: tracePath, ToolCalls: len(dbRows), Verified: true, Rows: dbRows,
	}, nil
}

func loadTraceToolHashes(tracePath string) ([]ToolHashRow, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		return nil, fmt.Errorf("open trace: %w", err)
	}
	defer f.Close()
	var rows []ToolHashRow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e trace.Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("parse trace line: %w", err)
		}
		if e.Type != "tool" {
			continue
		}
		rows = append(rows, ToolHashRow{
			Seq:       toolSeqFromTrace(e),
			Tool:      e.Tool,
			InputHash: e.ArgsHash,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan trace: %w", err)
	}
	return rows, nil
}

func toolSeqFromTrace(e trace.Entry) int64 {
	if e.Payload != nil {
		if v, ok := e.Payload["tool_seq"]; ok {
			switch x := v.(type) {
			case float64:
				if x > 0 {
					return int64(x)
				}
			case int64:
				if x > 0 {
					return x
				}
			case int:
				if x > 0 {
					return int64(x)
				}
			}
		}
	}
	return e.Seq
}

func loadToolIOHashes(runDir string) ([]ToolHashRow, error) {
	reqs, err := filepath.Glob(filepath.Join(runDir, "toolio", "*.req.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(reqs)
	rows := make([]ToolHashRow, 0, len(reqs))
	for _, reqPath := range reqs {
		base := strings.TrimSuffix(filepath.Base(reqPath), ".req.json")
		resPath := filepath.Join(runDir, "toolio", base+".res.json")
		reqBytes, err := os.ReadFile(reqPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", reqPath, err)
		}
		resBytes, err := os.ReadFile(resPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", resPath, err)
		}
		reqBytes = bytes.TrimSuffix(reqBytes, []byte{'\n'})
		resBytes = bytes.TrimSuffix(resBytes, []byte{'\n'})
		var req struct {
			ReqID int64  `json:"req"`
			Tool  string `json:"tool"`
		}
		if err := json.Unmarshal(reqBytes, &req); err != nil {
			return nil, fmt.Errorf("parse req %s: %w", reqPath, err)
		}
		rows = append(rows, ToolHashRow{
			Seq:        req.ReqID,
			Tool:       req.Tool,
			InputHash:  trace.SHA256Hex(reqBytes),
			OutputHash: trace.SHA256Hex(resBytes),
		})
	}
	return rows, nil
}

func loadDBToolHashes(st *store.Store, runID string) ([]ToolHashRow, error) {
	rows, err := st.DB().Query(`SELECT seq,tool,input_hash,output_hash,error_code FROM tool_calls WHERE run_id=? ORDER BY seq`, runID)
	if err != nil {
		return nil, fmt.Errorf("query tool_calls: %w", err)
	}
	defer rows.Close()
	var out []ToolHashRow
	for rows.Next() {
		var r ToolHashRow
		if err := rows.Scan(&r.Seq, &r.Tool, &r.InputHash, &r.OutputHash, &r.ErrorCode); err != nil {
			return nil, fmt.Errorf("scan tool_calls: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows tool_calls: %w", err)
	}
	return out, nil
}

func EnsureScorePlaceholder(runDir string, payload map[string]any) (string, error) {
	scorePath := filepath.Join(runDir, "score.json")
	if payload == nil {
		payload = map[string]any{}
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal score placeholder: %w", err)
	}
	if err := os.WriteFile(scorePath, append(b, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write score placeholder: %w", err)
	}
	return scorePath, nil
}
