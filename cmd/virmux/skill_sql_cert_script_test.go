package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillSQLCertRequiresCohortLinkedPairwiseRows(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	runsDir := filepath.Join(repo, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	setup := exec.Command("sqlite3", dbPath)
	setup.Stdin = strings.NewReader(`
CREATE TABLE eval_runs (id TEXT PRIMARY KEY, skill TEXT, cohort TEXT, pass INTEGER, created_at TEXT);
CREATE TABLE experiments (id TEXT PRIMARY KEY, eval_run_id TEXT, skill TEXT, created_at TEXT);
CREATE TABLE comparisons (id TEXT PRIMARY KEY, experiment_id TEXT);
CREATE TABLE promotions (id TEXT PRIMARY KEY, op TEXT, eval_run_id TEXT, created_at TEXT);
CREATE TABLE canary_runs (id TEXT PRIMARY KEY, eval_run_id TEXT, action TEXT, caught_by_canary INTEGER, created_at TEXT);
INSERT INTO eval_runs(id,skill,cohort,pass,created_at) VALUES
  ('c3-pass','dd','qa-skill-c3-20260224',1,'2026-02-24T10:00:00Z'),
  ('c3-fail','dd','qa-skill-c3-20260224',0,'2026-02-24T10:01:00Z');
INSERT INTO promotions(id,op,eval_run_id,created_at) VALUES ('promo-1','promote','c3-pass','2026-02-24T10:02:00Z');
INSERT INTO experiments(id,eval_run_id,skill,created_at) VALUES ('exp-stale','other-eval','dd','2026-02-24T10:03:00Z');
INSERT INTO comparisons(id,experiment_id) VALUES ('cmp-stale','exp-stale');
`)
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup failed: %v\n%s", err, string(out))
	}
	out, err := runScriptFromRepo(t, repo, "skill_sql_cert.sh", "--db", dbPath)
	if err == nil {
		t.Fatalf("expected cert failure when pairwise rows are not cohort-linked, output:\n%s", out)
	}
}

func TestSkillSQLCertPassesWithCohortLinkedPairwiseRows(t *testing.T) {
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
CREATE TABLE experiments (id TEXT PRIMARY KEY, eval_run_id TEXT, skill TEXT, created_at TEXT);
CREATE TABLE comparisons (id TEXT PRIMARY KEY, experiment_id TEXT);
CREATE TABLE promotions (id TEXT PRIMARY KEY, op TEXT, eval_run_id TEXT, created_at TEXT);
CREATE TABLE canary_runs (id TEXT PRIMARY KEY, eval_run_id TEXT, action TEXT, caught_by_canary INTEGER, created_at TEXT);
INSERT INTO eval_runs(id,skill,cohort,pass,created_at) VALUES
  ('c3-pass','dd','qa-skill-c3-20260224',1,'2026-02-24T10:00:00Z'),
  ('c3-fail','dd','qa-skill-c3-20260224',0,'2026-02-24T10:01:00Z');
INSERT INTO promotions(id,op,eval_run_id,created_at) VALUES ('promo-1','promote','c3-pass','2026-02-24T10:02:00Z');
INSERT INTO experiments(id,eval_run_id,skill,created_at) VALUES ('exp-1','c3-pass','dd','2026-02-24T10:03:00Z');
INSERT INTO comparisons(id,experiment_id) VALUES ('cmp-1','exp-1');
`)
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup failed: %v\n%s", err, string(out))
	}
	out, err := runScriptFromRepo(t, repo, "skill_sql_cert.sh", "--db", dbPath)
	if err != nil {
		t.Fatalf("expected cert success, err=%v output:\n%s", err, out)
	}
}
