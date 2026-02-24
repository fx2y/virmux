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

func TestCanarySnapshotScriptWritesDeterministicDsetAndManifest(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	runsDir := filepath.Join(repo, "runs")
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		runID := "rid-canary-snap-" + string(rune('1'+i))
		if err := st.StartRun(ctx, store.Run{ID: runID, Task: "skill:run", Label: "c5", AgentID: "default", ImageSHA: "img", KernelSHA: "k", RootfsSHA: "r", StartedAt: now.Add(-1 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishRun(ctx, runID, "ok", 0, 0, filepath.Join("runs", runID, "trace.ndjson"), "", 0.2, now); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertToolCall(ctx, store.ToolCall{RunID: runID, Seq: 1, ReqID: int64(i + 1), Tool: "shell.exec", InputHash: "sha256:in", OutputHash: "sha256:out"}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertArtifact(ctx, runID, filepath.Join("runs", runID, "artifact.bin"), "sha256:file", 5000); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	dsetsDir := filepath.Join(repo, "dsets")
	if err := os.MkdirAll(filepath.Join(dsetsDir, "smoke"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dsetsDir, "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dsetsDir, "torture"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteScriptFixtureFile(t, filepath.Join(dsetsDir, "smoke", "smoke-v1.jsonl"), `{"id":"smoke-1","input":{},"context_refs":[],"expected_properties":{},"tags":["smoke"]}`+"\n")
	mustWriteScriptFixtureFile(t, filepath.Join(dsetsDir, "core", "core-v1.jsonl"), `{"id":"core-1","input":{},"context_refs":[],"expected_properties":{},"tags":["core"]}`+"\n")
	mustWriteScriptFixtureFile(t, filepath.Join(dsetsDir, "torture", "torture-v1.jsonl"), `{"id":"torture-1","input":{},"context_refs":[],"expected_properties":{},"tags":["torture"]}`+"\n")

	out, err := runScriptFromRepo(t, repo, "canary_snapshot.sh", "--db", dbPath, "--out-dir", dsetsDir, "--date", "20260224", "--lookback-hours", "48", "--limit", "10")
	if err != nil {
		t.Fatalf("snapshot failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "canary:snapshot: OK") {
		t.Fatalf("expected OK output, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dsetsDir, "prod_20260224.jsonl")); err != nil {
		t.Fatalf("missing prod dset: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dsetsDir, "prod_20260224.manifest.json")); err != nil {
		t.Fatalf("missing prod manifest: %v", err)
	}

	out2, err := runScriptFromRepo(t, repo, "canary_snapshot.sh", "--db", dbPath, "--out-dir", dsetsDir, "--date", "20260224")
	if err == nil {
		t.Fatalf("expected second snapshot run to fail, output:\n%s", out2)
	}
}

func TestCanaryRunScriptWritesCanaryRowAndCaughtByCanary(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	dbDir := filepath.Join(repo, "runs")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "virmux.sqlite")
	cmd := exec.Command("sqlite3", dbPath)
	cmd.Stdin = strings.NewReader(`
CREATE TABLE eval_runs (
  id TEXT PRIMARY KEY,
  skill TEXT NOT NULL,
  cohort TEXT NOT NULL DEFAULT '',
  pass INTEGER NOT NULL,
  score_p50_delta REAL NOT NULL DEFAULT 0,
  fail_rate_delta REAL NOT NULL DEFAULT 0,
  cost_delta REAL NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE eval_cases (
  eval_run_id TEXT NOT NULL,
  fixture_id TEXT NOT NULL,
  base_score REAL NOT NULL,
  head_score REAL NOT NULL,
  base_pass INTEGER NOT NULL,
  head_pass INTEGER NOT NULL
);
CREATE TABLE canary_runs (
  id TEXT PRIMARY KEY,
  skill TEXT NOT NULL,
  eval_run_id TEXT NOT NULL,
  curated_eval_run_id TEXT,
  dset_path TEXT NOT NULL,
  dset_sha256 TEXT NOT NULL,
  dset_count INTEGER NOT NULL,
  candidate_ref TEXT NOT NULL,
  baseline_ref TEXT NOT NULL,
  gate_verdict_json TEXT NOT NULL,
  action TEXT NOT NULL,
  action_ref TEXT NOT NULL,
  caught_by_canary INTEGER NOT NULL,
  backlog_path TEXT NOT NULL,
  summary_path TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE promotions (
  id TEXT PRIMARY KEY,
  skill TEXT NOT NULL,
  op TEXT NOT NULL,
  eval_run_id TEXT,
  created_at TEXT NOT NULL
);
INSERT INTO eval_runs(id,skill,cohort,pass,score_p50_delta,fail_rate_delta,cost_delta,created_at)
VALUES
  ('ab-canary-1','dd','qa-skill-c5-20260224',0,-0.7,0.4,0.2,'2026-02-24T00:00:00Z'),
  ('ab-curated-pass','dd','qa-skill-c3-20260223',1,0.1,-0.1,0.0,'2026-02-23T00:00:00Z');
INSERT INTO eval_cases(eval_run_id,fixture_id,base_score,head_score,base_pass,head_pass)
VALUES ('ab-canary-1','case01',0.9,0.2,1,0);
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup failed: %v\n%s", err, string(out))
	}

	if err := os.MkdirAll(filepath.Join(repo, "dsets"), 0o755); err != nil {
		t.Fatal(err)
	}
	dsetPath := filepath.Join(repo, "dsets", "prod_20260224.jsonl")
	mustWriteScriptFixtureFile(t, dsetPath, `{"id":"prod-1","input":{},"context_refs":[],"expected_properties":{},"tags":["core"]}`+"\n")

	binDir := filepath.Join(repo, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteScriptFixtureFile(t, filepath.Join(binDir, "go"), `#!/usr/bin/env bash
set -euo pipefail
db=""
for ((i=1;i<=$#;i++)); do
  if [[ "${!i}" == "--db" ]]; then
    j=$((i+1))
    db="${!j}"
  fi
done
if [[ "$*" == *" skill ab "* ]]; then
  echo '{"id":"ab-canary-1","pass":false,"reason":"seeded"}'
  exit 1
fi
if [[ "$*" == *" skill promote "* ]]; then
  sqlite3 "$db" "INSERT INTO promotions(id,skill,op,eval_run_id,created_at) VALUES('promo-rollback-1','dd','rollback','ab-canary-1','2026-02-24T00:00:00Z');"
  echo '{"id":"promo-rollback-1","op":"rollback"}'
  exit 0
fi
echo "unsupported mocked go invocation: $*" >&2
exit 2
`)
	if err := os.Chmod(filepath.Join(binDir, "go"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runScriptFromRepoWithEnv(t, repo, []string{"PATH=" + binDir + ":" + os.Getenv("PATH")}, "canary_run.sh",
		"--skill", "dd",
		"--candidate-ref", "head-sha",
		"--baseline-ref", "base-sha",
		"--dset", dsetPath,
		"--db", dbPath,
		"--runs-dir", filepath.Join(repo, "runs"),
		"--repo-dir", repo,
		"--skills-dir", "skills",
	)
	if err == nil {
		t.Fatalf("expected canary regression exit, output:\n%s", out)
	}
	if !strings.Contains(out, "\"caught_by_canary\": true") {
		t.Fatalf("expected caught_by_canary in summary output, got:\n%s", out)
	}

	q := exec.Command("sqlite3", dbPath, `SELECT action,caught_by_canary,summary_path,backlog_path FROM canary_runs WHERE eval_run_id='ab-canary-1'`)
	qb, qerr := q.CombinedOutput()
	if qerr != nil {
		t.Fatalf("query canary_runs failed: %v\n%s", qerr, string(qb))
	}
	parts := strings.Split(strings.TrimSpace(string(qb)), "|")
	if len(parts) != 4 {
		t.Fatalf("unexpected canary row: %q", string(qb))
	}
	if parts[0] != "rollback" || parts[1] != "1" {
		t.Fatalf("unexpected action/caught values: %q", string(qb))
	}
	if parts[2] == "" || parts[3] == "" {
		t.Fatalf("expected summary/backlog paths, got: %q", string(qb))
	}
	if _, err := os.Stat(filepath.Join(repo, parts[3])); err != nil {
		t.Fatalf("backlog file missing: %v", err)
	}
	promoQ := exec.Command("sqlite3", dbPath, `SELECT COUNT(*) FROM promotions WHERE op='rollback'`)
	pb, perr := promoQ.CombinedOutput()
	if perr != nil {
		t.Fatalf("query promotions failed: %v\n%s", perr, string(pb))
	}
	if strings.TrimSpace(string(pb)) != "1" {
		t.Fatalf("expected one rollback promotion row, got %q", string(pb))
	}
}

func newScriptTestRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
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
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "README.md"), "x\n")
	runGit("add", "README.md")
	runGit("commit", "-m", "init")
	return repo
}

func runScriptFromRepo(t *testing.T, repo, name string, args ...string) (string, error) {
	t.Helper()
	return runScriptFromRepoWithEnv(t, repo, nil, name, args...)
}

func runScriptFromRepoWithEnv(t *testing.T, repo string, extraEnv []string, name string, args ...string) (string, error) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "scripts", name)
	cmdArgs := append([]string{scriptPath}, args...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustWriteScriptFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
