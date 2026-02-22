package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateLine(t *testing.T) {
	t.Parallel()
	good := []byte(`{"ts":"2026-02-22T00:00:00Z","run_id":"r1","seq":1,"type":"event","task":"vm:smoke","event":"run.started","payload":{}}`)
	if err := ValidateLine(good); err != nil {
		t.Fatalf("expected valid line: %v", err)
	}

	bad := []byte(`{"run_id":"r1"}`)
	if err := ValidateLine(bad); err == nil {
		t.Fatalf("expected invalid line")
	}
}

func TestWriterReopenAppends(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "trace.jsonl")

	w1, err := NewWriter(path)
	if err != nil {
		t.Fatalf("new writer #1: %v", err)
	}
	if err := w1.Emit("r1", "vm:smoke", "run.started", map[string]any{"n": 1}); err != nil {
		t.Fatalf("emit #1: %v", err)
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("close #1: %v", err)
	}

	w2, err := NewWriter(path)
	if err != nil {
		t.Fatalf("new writer #2: %v", err)
	}
	if err := w2.Emit("r1", "vm:smoke", "run.finished", map[string]any{"n": 2}); err != nil {
		t.Fatalf("emit #2: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("close #2: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 trace lines after reopen, got %d (%q)", len(lines), string(data))
	}
}

func TestWriterToolReceiptAddsTraceFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "trace.ndjson")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	payload := map[string]any{
		"tool_seq":    1,
		"req":         1,
		"tool":        "shell.exec",
		"input_hash":  "sha256:in",
		"output_hash": "sha256:out",
		"stdout_ref":  "artifacts/1.out",
		"stderr_ref":  "artifacts/1.err",
		"exit_code":   0,
		"dur_ms":      12,
		"bytes_in":    34,
		"bytes_out":   56,
	}
	if err := w.Emit("r1", "vm:run", "vm.tool.result", payload); err != nil {
		t.Fatalf("emit tool receipt: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	line := strings.TrimSpace(string(raw))
	if err := ValidateLine([]byte(line)); err != nil {
		t.Fatalf("validate tool line: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["type"] != "tool" {
		t.Fatalf("expected type=tool, got %#v", got["type"])
	}
	if got["tool"] != "shell.exec" {
		t.Fatalf("expected tool shell.exec, got %#v", got["tool"])
	}
}

func TestExtractToolReceiptParsesPayload(t *testing.T) {
	t.Parallel()
	receipt, ok := ExtractToolReceipt("vm.tool.result", map[string]any{
		"tool_seq":    7,
		"req":         3,
		"tool":        "fs.read",
		"args_hash":   "sha256:a",
		"output_hash": "sha256:b",
		"input_ref":   "toolio/000003.req.json",
		"output_ref":  "toolio/000003.res.json",
		"stdout_ref":  "artifacts/3.out",
		"stderr_ref":  "artifacts/3.err",
		"exit_code":   0,
		"dur_ms":      9,
		"bytes_in":    10,
		"bytes_out":   11,
		"error_code":  "",
	})
	if !ok {
		t.Fatalf("expected tool receipt")
	}
	if receipt.Tool != "fs.read" || receipt.InputHash != "sha256:a" || receipt.OutputHash != "sha256:b" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestWriterEscapesPayloadNewlines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "trace.ndjson")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Emit("r1", "vm:run", "x", map[string]any{"s": "a\nb"}); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one NDJSON line, got %d", len(lines))
	}
}
