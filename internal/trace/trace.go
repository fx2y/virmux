package trace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Entry struct {
	TS      string         `json:"ts"`
	RunID   string         `json:"run_id"`
	Task    string         `json:"task"`
	Event   string         `json:"event"`
	Payload map[string]any `json:"payload"`
}

type Writer struct {
	file *os.File
	buf  *bufio.Writer
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
	if payload == nil {
		payload = map[string]any{}
	}
	entry := Entry{
		TS:      time.Now().UTC().Format(time.RFC3339Nano),
		RunID:   runID,
		Task:    task,
		Event:   event,
		Payload: payload,
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
	if e.TS == "" || e.RunID == "" || e.Task == "" || e.Event == "" || e.Payload == nil {
		return fmt.Errorf("missing required field")
	}
	return nil
}
