package trace

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	TS        string         `json:"ts"`
	RunID     string         `json:"run_id"`
	Seq       int64          `json:"seq"`
	Type      string         `json:"type,omitempty"`
	Task      string         `json:"task"`
	Event     string         `json:"event"`
	Tool      string         `json:"tool,omitempty"`
	ArgsHash  string         `json:"args_hash,omitempty"`
	StdoutRef string         `json:"stdout_ref,omitempty"`
	StderrRef string         `json:"stderr_ref,omitempty"`
	ExitCode  *int           `json:"exit_code,omitempty"`
	DurMS     *int64         `json:"dur_ms,omitempty"`
	BytesIn   *int64         `json:"bytes_in,omitempty"`
	BytesOut  *int64         `json:"bytes_out,omitempty"`
	Payload   map[string]any `json:"payload"`
}

type Writer struct {
	file *os.File
	buf  *bufio.Writer
	mu   sync.Mutex
	seq  int64
}

func NewWriter(path string) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create trace dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open trace file: %w", err)
	}
	return &Writer{file: f, buf: bufio.NewWriter(f)}, nil
}

func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	if w.buf != nil {
		if err := w.buf.Flush(); err != nil {
			_ = w.file.Close()
			return err
		}
	}
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

func (w *Writer) Emit(runID, task, event string, payload map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if payload == nil {
		payload = map[string]any{}
	}
	w.seq++
	entry := Entry{
		TS:      time.Now().UTC().Format(time.RFC3339Nano),
		RunID:   runID,
		Seq:     w.seq,
		Type:    "event",
		Task:    task,
		Event:   event,
		Payload: payload,
	}
	if tr, ok := ExtractToolReceipt(event, payload); ok {
		entry.Type = "tool"
		entry.Tool = tr.Tool
		entry.ArgsHash = tr.InputHash
		entry.StdoutRef = tr.StdoutRef
		entry.StderrRef = tr.StderrRef
		entry.ExitCode = intPtr(tr.RC)
		entry.DurMS = int64Ptr(tr.DurMS)
		entry.BytesIn = int64Ptr(tr.BytesIn)
		entry.BytesOut = int64Ptr(tr.BytesOut)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal trace event: %w", err)
	}
	if _, err := w.buf.Write(data); err != nil {
		return fmt.Errorf("write trace event: %w", err)
	}
	if err := w.buf.WriteByte('\n'); err != nil {
		return fmt.Errorf("write trace newline: %w", err)
	}
	return w.buf.Flush()
}

func ValidateLine(data []byte) error {
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	if e.TS == "" || e.RunID == "" || e.Seq <= 0 || e.Task == "" || e.Event == "" || e.Payload == nil {
		return fmt.Errorf("missing required field")
	}
	if e.Type == "" {
		return fmt.Errorf("missing type")
	}
	if e.Type == "tool" {
		if e.Tool == "" || e.ArgsHash == "" || e.ExitCode == nil || e.DurMS == nil || e.BytesIn == nil || e.BytesOut == nil {
			return fmt.Errorf("missing tool receipt field")
		}
		if e.StdoutRef == "" || e.StderrRef == "" {
			return fmt.Errorf("missing tool io refs")
		}
	}
	return nil
}

type ToolReceipt struct {
	ReqID      int64
	Seq        int64
	Tool       string
	InputHash  string
	OutputHash string
	InputRef   string
	OutputRef  string
	StdoutRef  string
	StderrRef  string
	RC         int
	DurMS      int64
	BytesIn    int64
	BytesOut   int64
	ErrorCode  string
}

func ExtractToolReceipt(event string, payload map[string]any) (ToolReceipt, bool) {
	if event != "vm.tool.result" || payload == nil {
		return ToolReceipt{}, false
	}
	tool := stringVal(payload["tool"])
	inHash := firstNonEmpty(stringVal(payload["input_hash"]), stringVal(payload["args_hash"]))
	outHash := stringVal(payload["output_hash"])
	if tool == "" || inHash == "" || outHash == "" {
		return ToolReceipt{}, false
	}
	reqID := int64Val(payload["req"])
	rc := int(int64Val(payload["exit_code"]))
	durMS := int64Val(payload["dur_ms"])
	bytesIn := int64Val(payload["bytes_in"])
	bytesOut := int64Val(payload["bytes_out"])
	seq := int64Val(payload["tool_seq"])
	return ToolReceipt{
		ReqID:      reqID,
		Seq:        seq,
		Tool:       tool,
		InputHash:  inHash,
		OutputHash: outHash,
		InputRef:   stringVal(payload["input_ref"]),
		OutputRef:  stringVal(payload["output_ref"]),
		StdoutRef:  stringVal(payload["stdout_ref"]),
		StderrRef:  stringVal(payload["stderr_ref"]),
		RC:         rc,
		DurMS:      durMS,
		BytesIn:    bytesIn,
		BytesOut:   bytesOut,
		ErrorCode:  stringVal(payload["error_code"]),
	}, true
}

func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func intPtr(v int) *int       { return &v }
func int64Ptr(v int64) *int64 { return &v }

func stringVal(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func int64Val(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	default:
		return 0
	}
}
