package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/haris/virmux/internal/store"
)

func TestDBCheckFailsHashMismatchWithoutMutation(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "init")

	runsDir := filepath.Join(repo, "runs")
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runID := "rid-db-check"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.req.json"), []byte(`{"req":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.res.json"), []byte(`{"req":1,"ok":true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.StartRun(ctx, store.Run{
		ID:        runID,
		Task:      "skill:run",
		Label:     "db-check",
		AgentID:   "default",
		ImageSHA:  "img",
		KernelSHA: "k",
		RootfsSHA: "r",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertToolCall(ctx, store.ToolCall{
		RunID:      runID,
		Seq:        1,
		ReqID:      1,
		Tool:       "shell.exec",
		InputHash:  "sha256:bad-input",
		OutputHash: "sha256:bad-output",
		InputRef:   "toolio/000001.req.json",
		OutputRef:  "toolio/000001.res.json",
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "scripts", "db_check.sh")
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected db_check failure for hash mismatch, output:\n%s", string(out))
	}
	if !strings.Contains(string(out), "hash mismatch") {
		t.Fatalf("expected mismatch diagnostics, got:\n%s", string(out))
	}
	ist, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ist.Close()
	var inHash, outHash string
	if err := ist.DB().QueryRow(`SELECT input_hash,output_hash FROM tool_calls WHERE run_id=?`, runID).Scan(&inHash, &outHash); err != nil {
		t.Fatal(err)
	}
	if inHash != "sha256:bad-input" || outHash != "sha256:bad-output" {
		t.Fatalf("db_check mutated hashes unexpectedly input=%s output=%s", inHash, outHash)
	}
}
