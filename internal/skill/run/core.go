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

// ResolveFixturePath keeps fixture path selection deterministic while allowing
// both repo-relative and skill-relative fixture arguments.
func ResolveFixturePath(skillDir, fixtureArg string) string {
	raw := strings.TrimSpace(fixtureArg)
	if raw == "" {
		return raw
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	candidates := make([]string, 0, 3)
	add := func(p string) {
		p = filepath.Clean(p)
		for _, seen := range candidates {
			if seen == p {
				return
			}
		}
		candidates = append(candidates, p)
	}
	add(raw)
	add(filepath.Join(skillDir, raw))
	add(filepath.Join(skillDir, "tests", raw))
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return candidates[len(candidates)-1]
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
	RunID        string        `json:"run_id"`
	AgainstRunID string        `json:"against_run_id,omitempty"`
	TracePath    string        `json:"trace_path"`
	ToolCalls    int           `json:"tool_calls"`
	Verified     bool          `json:"verified"`
	Exempt       bool          `json:"exempt,omitempty"`
	ExemptReason string        `json:"exempt_reason,omitempty"`
	Mismatch     string        `json:"mismatch,omitempty"`
	Rows         []ToolHashRow `json:"rows,omitempty"`
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
		if traceRows[i].Seq != d.Seq || traceRows[i].Tool != d.Tool || traceRows[i].InputHash != d.InputHash || traceRows[i].OutputHash != d.OutputHash {
			return ReplayReport{}, fmt.Errorf("replay mismatch trace seq=%d trace=%+v db=%+v", d.Seq, traceRows[i], d)
		}
	}
	return ReplayReport{
		RunID: runID, TracePath: tracePath, ToolCalls: len(dbRows), Verified: true, Rows: dbRows,
	}, nil
}

func CompareReplayHashes(dbPath, runsDir, runID, againstRunID string) (ReplayReport, error) {
	if strings.TrimSpace(againstRunID) == "" {
		return ReplayReport{}, errors.New("against run id required")
	}
	baseDir := filepath.Join(runsDir, runID)
	otherDir := filepath.Join(runsDir, againstRunID)
	baseDet, err := isDeterministicFixture(baseDir)
	if err != nil {
		return ReplayReport{}, err
	}
	otherDet, err := isDeterministicFixture(otherDir)
	if err != nil {
		return ReplayReport{}, err
	}
	if !baseDet || !otherDet {
		return ReplayReport{
			RunID:        runID,
			AgainstRunID: againstRunID,
			TracePath:    filepath.Join(baseDir, "trace.ndjson"),
			Verified:     false,
			Exempt:       true,
			ExemptReason: "NONDET_FIXTURE",
		}, nil
	}
	base, err := VerifyReplayHashes(dbPath, baseDir, runID)
	if err != nil {
		return ReplayReport{}, err
	}
	other, err := VerifyReplayHashes(dbPath, otherDir, againstRunID)
	if err != nil {
		return ReplayReport{}, err
	}
	out := ReplayReport{
		RunID:        runID,
		AgainstRunID: againstRunID,
		TracePath:    base.TracePath,
		ToolCalls:    len(base.Rows),
		Verified:     true,
		Rows:         base.Rows,
	}
	if len(base.Rows) != len(other.Rows) {
		out.Verified = false
		out.Mismatch = fmt.Sprintf("tool call count mismatch base=%d against=%d", len(base.Rows), len(other.Rows))
		return out, nil
	}
	for i := range base.Rows {
		a, b := base.Rows[i], other.Rows[i]
		if a.Seq != b.Seq || a.Tool != b.Tool || a.InputHash != b.InputHash || a.OutputHash != b.OutputHash || a.ErrorCode != b.ErrorCode {
			out.Verified = false
			out.Mismatch = fmt.Sprintf("first divergence seq=%d tool=%s input=%s/%s output=%s/%s", a.Seq, a.Tool, a.InputHash, b.InputHash, a.OutputHash, b.OutputHash)
			return out, nil
		}
	}
	baseArt, err := loadArtifactHashesFromInventory(dbPath, runsDir, runID)
	if err != nil {
		return ReplayReport{}, err
	}
	otherArt, err := loadArtifactHashesFromInventory(dbPath, runsDir, againstRunID)
	if err != nil {
		return ReplayReport{}, err
	}
	if len(baseArt) != len(otherArt) {
		out.Verified = false
		out.Mismatch = fmt.Sprintf("artifact count mismatch base=%d against=%d", len(baseArt), len(otherArt))
		return out, nil
	}
	for i := range baseArt {
		if baseArt[i] != otherArt[i] {
			out.Verified = false
			out.Mismatch = fmt.Sprintf("artifact divergence %s != %s", baseArt[i], otherArt[i])
			return out, nil
		}
	}
	return out, nil
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
			Seq:        toolSeqFromTrace(e),
			Tool:       e.Tool,
			InputHash:  e.ArgsHash,
			OutputHash: firstNonEmpty(strings.TrimSpace(e.OutputHash), payloadString(e.Payload, "output_hash")),
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

func isDeterministicFixture(runDir string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(runDir, "skill-run.json"))
	if err != nil {
		return true, nil
	}
	var meta struct {
		Deterministic *bool `json:"deterministic"`
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return false, fmt.Errorf("parse skill-run.json: %w", err)
	}
	if meta.Deterministic == nil {
		return true, nil
	}
	return *meta.Deterministic, nil
}

func loadArtifactHashes(runDir string) ([]string, error) {
	var out []string
	for _, root := range []string{"artifacts", "toolio"} {
		base := filepath.Join(runDir, root)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.Walk(base, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(runDir, path)
			if err != nil {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel)+":"+trace.SHA256Hex(b))
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk artifact root: %w", err)
		}
	}
	sort.Strings(out)
	return out, nil
}

func loadArtifactHashesFromInventory(dbPath, runsDir, runID string) ([]string, error) {
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	rows, err := st.DB().Query(`SELECT path,sha256 FROM artifacts WHERE run_id=? ORDER BY path,id`, runID)
	if err != nil {
		return nil, fmt.Errorf("query artifacts: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var pathVal string
		var sha string
		if err := rows.Scan(&pathVal, &sha); err != nil {
			return nil, fmt.Errorf("scan artifacts: %w", err)
		}
		absPath, relPath := resolveArtifactPath(runsDir, runID, pathVal)
		if strings.HasPrefix(strings.TrimSpace(sha), "meta:") {
			out = append(out, relPath+":"+strings.TrimSpace(sha))
			continue
		}
		b, err := os.ReadFile(absPath)
		if err != nil {
			return nil, fmt.Errorf("read artifact %s: %w", absPath, err)
		}
		out = append(out, relPath+":"+trace.SHA256Hex(b))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows artifacts: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

func resolveArtifactPath(runsDir, runID, pathVal string) (absPath, relPath string) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(pathVal)))
	runDir := filepath.Join(runsDir, runID)
	if filepath.IsAbs(clean) {
		absPath = clean
	} else {
		candidates := []string{
			filepath.Join(runDir, clean),
			filepath.Join(runsDir, clean),
		}
		for _, c := range candidates {
			if _, err := os.Lstat(c); err == nil {
				absPath = c
				break
			}
		}
		if absPath == "" {
			absPath = filepath.Join(runDir, clean)
		}
	}
	if rel, err := filepath.Rel(runDir, absPath); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return absPath, filepath.ToSlash(rel)
	}
	return absPath, filepath.ToSlash(clean)
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	v, _ := payload[key]
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
