package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpec05DoDMatrixScriptPassesWithFreshEvidence(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	runsDir := filepath.Join(repo, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	setup := exec.Command("sqlite3", dbPath)
	setup.Stdin = strings.NewReader(`
CREATE TABLE eval_runs (id TEXT PRIMARY KEY, skill TEXT, cohort TEXT, pass INTEGER, created_at TEXT);
CREATE TABLE experiments (id TEXT PRIMARY KEY, skill TEXT, base_ref TEXT, head_ref TEXT, created_at TEXT);
CREATE TABLE comparisons (id TEXT PRIMARY KEY, experiment_id TEXT);
CREATE TABLE promotions (id TEXT PRIMARY KEY, skill TEXT, op TEXT, eval_run_id TEXT, created_at TEXT);
CREATE TABLE canary_runs (id TEXT PRIMARY KEY, eval_run_id TEXT, action TEXT, caught_by_canary INTEGER, created_at TEXT);
INSERT INTO eval_runs(id,skill,cohort,pass,created_at) VALUES
  ('c3-pass','dd','qa-skill-c3-20260224',1,'2026-02-24T10:00:00Z'),
  ('c3-fail','dd','qa-skill-c3-20260224',0,'2026-02-24T10:01:00Z'),
  ('c5-pass','dd','qa-skill-c5-20260224',1,'2026-02-24T10:02:00Z'),
  ('c5-fail','dd','qa-skill-c5-20260224',0,'2026-02-24T10:03:00Z');
INSERT INTO experiments(id,skill,base_ref,head_ref,created_at) VALUES ('exp-1','dd','base','head','2026-02-24T10:04:30Z');
INSERT INTO comparisons(id,experiment_id) VALUES ('cmp-1','exp-1');
INSERT INTO promotions(id,skill,op,eval_run_id,created_at) VALUES
  ('promo-1','dd','promote','c3-pass','2026-02-24T10:04:00Z'),
  ('promo-2','dd','rollback','c5-fail','2026-02-24T10:05:00Z');
INSERT INTO canary_runs(id,eval_run_id,action,caught_by_canary,created_at) VALUES
  ('canary-1','c5-pass','promote',0,'2026-02-24T10:06:00Z'),
  ('canary-2','c5-fail','rollback',1,'2026-02-24T10:07:00Z');
`)
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup failed: %v\n%s", err, string(out))
	}
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "tmp", "skill-sql-cert-summary.json"), "{}\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "tmp", "skill-test-c2.ok"), "2026-02-24T10:00:00Z\n")

	out, err := runScriptFromRepo(t, repo, "spec05_dod_matrix.sh", "--db", dbPath, "--cert-ts", "2026-02-24T00:00:00Z")
	if err != nil {
		t.Fatalf("dod matrix script failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "spec05:dod: OK") {
		t.Fatalf("expected success output, got:\n%s", out)
	}
	b, err := os.ReadFile(filepath.Join(repo, "tmp", "spec05-dod-matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"pass": false`) {
		t.Fatalf("expected all matrix entries to pass, got:\n%s", string(b))
	}
}

func TestSpec05DoDMatrixScriptFailsWhenRollbackEvidenceMissing(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	runsDir := filepath.Join(repo, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	setup := exec.Command("sqlite3", dbPath)
	setup.Stdin = strings.NewReader(`
CREATE TABLE eval_runs (id TEXT PRIMARY KEY, skill TEXT, cohort TEXT, pass INTEGER, created_at TEXT);
CREATE TABLE experiments (id TEXT PRIMARY KEY, skill TEXT, base_ref TEXT, head_ref TEXT, created_at TEXT);
CREATE TABLE comparisons (id TEXT PRIMARY KEY, experiment_id TEXT);
CREATE TABLE promotions (id TEXT PRIMARY KEY, skill TEXT, op TEXT, eval_run_id TEXT, created_at TEXT);
CREATE TABLE canary_runs (id TEXT PRIMARY KEY, eval_run_id TEXT, action TEXT, caught_by_canary INTEGER, created_at TEXT);
INSERT INTO eval_runs(id,skill,cohort,pass,created_at) VALUES
  ('c3-pass','dd','qa-skill-c3-20260224',1,'2026-02-24T10:00:00Z'),
  ('c3-fail','dd','qa-skill-c3-20260224',0,'2026-02-24T10:01:00Z'),
  ('c5-pass','dd','qa-skill-c5-20260224',1,'2026-02-24T10:02:00Z');
INSERT INTO experiments(id,skill,base_ref,head_ref,created_at) VALUES ('exp-1','dd','base','head','2026-02-24T10:04:30Z');
INSERT INTO comparisons(id,experiment_id) VALUES ('cmp-1','exp-1');
INSERT INTO promotions(id,skill,op,eval_run_id,created_at) VALUES
  ('promo-1','dd','promote','c3-pass','2026-02-24T10:04:00Z');
INSERT INTO canary_runs(id,eval_run_id,action,caught_by_canary,created_at) VALUES
  ('canary-1','c5-pass','promote',0,'2026-02-24T10:06:00Z');
`)
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup failed: %v\n%s", err, string(out))
	}
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "tmp", "skill-sql-cert-summary.json"), "{}\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "tmp", "skill-test-c2.ok"), "2026-02-24T10:00:00Z\n")

	out, err := runScriptFromRepo(t, repo, "spec05_dod_matrix.sh", "--db", dbPath, "--cert-ts", "2026-02-24T00:00:00Z")
	if err == nil {
		t.Fatalf("expected dod matrix script failure, output:\n%s", out)
	}
	risk, rerr := os.ReadFile(filepath.Join(repo, "tmp", "spec05-residual-risk.md"))
	if rerr != nil {
		t.Fatalf("expected residual risk artifact: %v", rerr)
	}
	if !strings.Contains(string(risk), "RISK-C7-001") {
		t.Fatalf("expected C7 risk marker, got:\n%s", string(risk))
	}
}
