package main

import (
	"context"
	"database/sql"
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

func TestDBCheckFailsMissingSchemaWithoutCreatingTables(t *testing.T) {
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
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA foreign_keys=ON;`,
		`CREATE TABLE runs(id TEXT PRIMARY KEY, task TEXT NOT NULL, label TEXT NOT NULL DEFAULT '', agent_id TEXT NOT NULL DEFAULT 'default', image_sha TEXT NOT NULL, kernel_sha TEXT NOT NULL DEFAULT '', rootfs_sha TEXT NOT NULL DEFAULT '', snapshot_id TEXT NOT NULL DEFAULT '', cost_est REAL NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'running', started_at TEXT NOT NULL, ended_at TEXT, boot_ms INTEGER NOT NULL DEFAULT 0, resume_ms INTEGER NOT NULL DEFAULT 0, trace_path TEXT NOT NULL DEFAULT '', source_bundle TEXT NOT NULL DEFAULT '');`,
		`CREATE TABLE events(id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, ts TEXT NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL DEFAULT '{}', FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE);`,
		`CREATE TABLE artifacts(id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, path TEXT NOT NULL, sha256 TEXT NOT NULL, bytes INTEGER NOT NULL DEFAULT 0, FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE);`,
		`CREATE INDEX idx_events_run_id ON events(run_id);`,
		`CREATE INDEX idx_runs_started_at ON runs(started_at);`,
		`CREATE INDEX idx_artifacts_run_id ON artifacts(run_id);`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed db stmt failed: %v\n%s", err, stmt)
		}
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "scripts", "db_check.sh")
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected db_check failure for missing schema, output:\n%s", string(out))
	}
	if !strings.Contains(string(out), "missing") {
		t.Fatalf("expected missing schema diagnostics, got:\n%s", string(out))
	}

	var tableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='tool_calls'`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 {
		t.Fatalf("db_check unexpectedly created tool_calls table")
	}
}
