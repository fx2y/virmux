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

func TestResolveFixturePathPrefersExistingRepoRelative(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "dd")
	if err := os.MkdirAll(filepath.Join(skillDir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	repoRel := filepath.Join(root, "skills", "dd", "tests", "case01.json")
	if err := os.WriteFile(repoRel, []byte(`{"id":"case01","cmd":"echo ok"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	got := ResolveFixturePath(filepath.Join("skills", "dd"), filepath.ToSlash(filepath.Join("skills", "dd", "tests", "case01.json")))
	if filepath.Clean(got) != filepath.Clean(filepath.Join("skills", "dd", "tests", "case01.json")) {
		t.Fatalf("expected repo-relative fixture path, got %q", got)
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

func TestCompareReplayHashesNondetExemption(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runsDir := filepath.Join(tmp, "runs")
	for _, runID := range []string{"r1", "r2"} {
		runDir := filepath.Join(runsDir, runID)
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(runDir, "skill-run.json"), []byte(`{"deterministic":false}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := CompareReplayHashes(filepath.Join(runsDir, "virmux.sqlite"), runsDir, "r1", "r2")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Exempt || rep.ExemptReason != "NONDET_FIXTURE" {
		t.Fatalf("expected nondet exemption, got %+v", rep)
	}
}

func TestCompareReplayHashesMismatchIncludesFirstDivergence(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runsDir := filepath.Join(tmp, "runs")
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	makeRun := func(runID string, req map[string]any, res map[string]any) {
		runDir := filepath.Join(runsDir, runID)
		if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeNL(t, filepath.Join(runDir, "toolio", "000001.req.json"), mustJSON(t, req))
		writeNL(t, filepath.Join(runDir, "toolio", "000001.res.json"), mustJSON(t, res))
		if err := os.WriteFile(filepath.Join(runDir, "skill-run.json"), []byte(`{"deterministic":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		tw, err := trace.NewWriter(filepath.Join(runDir, "trace.ndjson"))
		if err != nil {
			t.Fatal(err)
		}
		payload := map[string]any{
			"tool_seq":    1,
			"req":         1,
			"tool":        "shell.exec",
			"input_hash":  trace.SHA256Hex(mustJSON(t, req)),
			"output_hash": trace.SHA256Hex(mustJSON(t, res)),
			"stdout_ref":  "artifacts/1.out",
			"stderr_ref":  "artifacts/1.err",
			"exit_code":   0,
			"dur_ms":      int64(1),
			"bytes_in":    int64(1),
			"bytes_out":   int64(1),
		}
		if err := tw.Emit(runID, "skill:run", "vm.tool.result", payload); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := st.StartRun(ctx, store.Run{ID: runID, Task: "skill:run", AgentID: "default", ImageSHA: "img", KernelSHA: "k", RootfsSHA: "r", StartedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertToolCall(ctx, store.ToolCall{
			RunID:      runID,
			Seq:        1,
			ReqID:      1,
			Tool:       "shell.exec",
			InputHash:  trace.SHA256Hex(mustJSON(t, req)),
			OutputHash: trace.SHA256Hex(mustJSON(t, res)),
			StdoutRef:  "artifacts/1.out",
			StderrRef:  "artifacts/1.err",
		}); err != nil {
			t.Fatal(err)
		}
	}
	makeRun("r1", map[string]any{"req": 1, "tool": "shell.exec", "args": map[string]any{"cmd": "echo ok"}}, map[string]any{"req": 1, "ok": true})
	makeRun("r2", map[string]any{"req": 1, "tool": "shell.exec", "args": map[string]any{"cmd": "echo nope"}}, map[string]any{"req": 1, "ok": true})
	rep, err := CompareReplayHashes(dbPath, runsDir, "r1", "r2")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verified {
		t.Fatalf("expected replay mismatch")
	}
	if !strings.Contains(rep.Mismatch, "first divergence") {
		t.Fatalf("expected mismatch detail, got %+v", rep)
	}
}

func TestVerifyReplayHashesDetectsTraceOutputHashMismatch(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runsDir := filepath.Join(tmp, "runs")
	runID := "r1"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
		t.Fatal(err)
	}
	reqBytes := mustJSON(t, map[string]any{"req": 1, "tool": "shell.exec", "args": map[string]any{"cmd": "echo ok"}})
	resBytes := mustJSON(t, map[string]any{"req": 1, "ok": true})
	writeNL(t, filepath.Join(runDir, "toolio", "000001.req.json"), reqBytes)
	writeNL(t, filepath.Join(runDir, "toolio", "000001.res.json"), resBytes)
	tw, err := trace.NewWriter(filepath.Join(runDir, "trace.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.Emit(runID, "skill:run", "vm.tool.result", map[string]any{
		"tool_seq":    1,
		"req":         1,
		"tool":        "shell.exec",
		"input_hash":  trace.SHA256Hex(reqBytes),
		"output_hash": "sha256:not-the-db-hash",
		"stdout_ref":  "artifacts/1.out",
		"stderr_ref":  "artifacts/1.err",
		"exit_code":   0,
		"dur_ms":      int64(1),
		"bytes_in":    int64(len(reqBytes)),
		"bytes_out":   int64(len(resBytes)),
	}); err != nil {
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
		RunID:      runID,
		Seq:        1,
		ReqID:      1,
		Tool:       "shell.exec",
		InputHash:  trace.SHA256Hex(reqBytes),
		OutputHash: trace.SHA256Hex(resBytes),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = VerifyReplayHashes(dbPath, runDir, runID)
	if err == nil || !strings.Contains(err.Error(), "trace") {
		t.Fatalf("expected trace output mismatch, got %v", err)
	}
}

func TestCompareReplayHashesDetectsScoreArtifactDivergence(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runsDir := filepath.Join(tmp, "runs")
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	makeRun := func(runID string, score float64) {
		runDir := filepath.Join(runsDir, runID)
		if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
			t.Fatal(err)
		}
		req := mustJSON(t, map[string]any{"req": 1, "tool": "shell.exec", "args": map[string]any{"cmd": "echo ok"}})
		res := mustJSON(t, map[string]any{"req": 1, "ok": true})
		writeNL(t, filepath.Join(runDir, "toolio", "000001.req.json"), req)
		writeNL(t, filepath.Join(runDir, "toolio", "000001.res.json"), res)
		if err := os.WriteFile(filepath.Join(runDir, "skill-run.json"), []byte(`{"deterministic":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		scoreDoc := map[string]any{"score": score}
		sb, _ := json.Marshal(scoreDoc)
		if err := os.WriteFile(filepath.Join(runDir, "score.json"), append(sb, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		tw, err := trace.NewWriter(filepath.Join(runDir, "trace.ndjson"))
		if err != nil {
			t.Fatal(err)
		}
		if err := tw.Emit(runID, "skill:run", "vm.tool.result", map[string]any{
			"tool_seq":    1,
			"req":         1,
			"tool":        "shell.exec",
			"input_hash":  trace.SHA256Hex(req),
			"output_hash": trace.SHA256Hex(res),
			"stdout_ref":  "artifacts/1.out",
			"stderr_ref":  "artifacts/1.err",
			"exit_code":   0,
			"dur_ms":      int64(1),
			"bytes_in":    int64(len(req)),
			"bytes_out":   int64(len(res)),
		}); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := st.StartRun(ctx, store.Run{ID: runID, Task: "skill:run", AgentID: "default", ImageSHA: "img", KernelSHA: "k", RootfsSHA: "r", StartedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertToolCall(ctx, store.ToolCall{
			RunID:      runID,
			Seq:        1,
			ReqID:      1,
			Tool:       "shell.exec",
			InputHash:  trace.SHA256Hex(req),
			OutputHash: trace.SHA256Hex(res),
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertArtifact(ctx, runID, "score.json", trace.SHA256Hex(append(sb, '\n')), int64(len(sb)+1)); err != nil {
			t.Fatal(err)
		}
	}
	makeRun("r1", 0.8)
	makeRun("r2", 0.1)
	rep, err := CompareReplayHashes(dbPath, runsDir, "r1", "r2")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verified || !strings.Contains(rep.Mismatch, "artifact divergence") {
		t.Fatalf("expected artifact mismatch on score.json, got %+v", rep)
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
