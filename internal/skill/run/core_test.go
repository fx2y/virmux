package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haris/virmux/internal/store"
	"github.com/haris/virmux/internal/trace"
)

func TestLoadFixtureShellExecCmdShortcut(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "case.json")
	if err := os.WriteFile(path, []byte(`{"id":"case01","cmd":"echo ok"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFixture(path)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if f.Tool != "shell.exec" {
		t.Fatalf("tool default mismatch: %q", f.Tool)
	}
	if got, _ := f.Args["cmd"].(string); got != "echo ok" {
		t.Fatalf("args.cmd mismatch: %v", f.Args["cmd"])
	}
}

func TestBudgetTrackerToolCallsExceededTyped(t *testing.T) {
	t.Parallel()
	bt := NewBudgetTracker(Budget{ToolCalls: 0}, time.Unix(0, 0))
	err := bt.BeforeToolCall("shell.exec")
	if err == nil {
		t.Fatalf("expected budget error")
	}
	if !strings.Contains(err.Error(), "BUDGET_EXCEEDED") {
		t.Fatalf("expected BUDGET_EXCEEDED, got %v", err)
	}
}

func TestEnsureScorePlaceholderWritesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p, err := EnsureScorePlaceholder(dir, map[string]any{"status": "pending"})
	if err != nil {
		t.Fatalf("ensure score placeholder: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"status": "pending"`) {
		t.Fatalf("score placeholder content mismatch: %s", b)
	}
}

func TestVerifyReplayHashesMatchesTraceToolioAndSQLite(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runsDir := filepath.Join(tmp, "runs")
	runID := "r1"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
		t.Fatal(err)
	}
	reqBytes := mustJSON(t, map[string]any{"req": 1, "tool": "shell.exec", "args": map[string]any{"cmd": "echo ok"}})
	resBytes := mustJSON(t, map[string]any{"req": 1, "ok": true, "rc": 0, "stdout_ref": "artifacts/1.out", "stderr_ref": "artifacts/1.err", "ohash": "sha256:x", "data": map[string]any{"stdout": "ok\n", "stderr": ""}})
	writeNL(t, filepath.Join(runDir, "toolio", "000001.req.json"), reqBytes)
	writeNL(t, filepath.Join(runDir, "toolio", "000001.res.json"), resBytes)
	tw, err := trace.NewWriter(filepath.Join(runDir, "trace.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"tool_seq":    1,
		"req":         1,
		"tool":        "shell.exec",
		"args_hash":   trace.SHA256Hex(reqBytes),
		"input_hash":  trace.SHA256Hex(reqBytes),
		"output_hash": trace.SHA256Hex(resBytes),
		"input_ref":   "toolio/000001.req.json",
		"output_ref":  "toolio/000001.res.json",
		"stdout_ref":  "artifacts/1.out",
		"stderr_ref":  "artifacts/1.err",
		"exit_code":   0,
		"dur_ms":      int64(1),
		"bytes_in":    int64(len(reqBytes)),
		"bytes_out":   int64(len(resBytes)),
		"error_code":  "",
	}
	if err := tw.Emit(runID, "skill:run", "vm.tool.result", payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.StartRun(ctx, store.Run{ID: runID, Task: "skill:run", AgentID: "default", ImageSHA: "img", KernelSHA: "k", RootfsSHA: "r", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertToolCall(ctx, store.ToolCall{
		RunID: runID, Seq: 1, ReqID: 1, Tool: "shell.exec",
		InputHash: trace.SHA256Hex(reqBytes), OutputHash: trace.SHA256Hex(resBytes),
		InputRef: "toolio/000001.req.json", OutputRef: "toolio/000001.res.json",
		StdoutRef: "artifacts/1.out", StderrRef: "artifacts/1.err",
	}); err != nil {
		t.Fatal(err)
	}
	rep, err := VerifyReplayHashes(dbPath, runDir, runID)
	if err != nil {
		t.Fatalf("verify replay hashes: %v", err)
	}
	if !rep.Verified || rep.ToolCalls != 1 {
		t.Fatalf("unexpected replay report: %#v", rep)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeNL(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, append(append([]byte(nil), b...), '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
