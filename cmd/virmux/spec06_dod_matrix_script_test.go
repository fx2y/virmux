package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpec06DoDMatrixFailsWithoutParallelProof(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)

	dbPath := filepath.Join(repo, "runs", "virmux.sqlite")
	mustWriteScriptFixtureFile(t, dbPath, "")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "tmp", "research-sql-cert-summary.json"), `{
  "research_run_count": 1,
  "research_reduce_count": 1,
  "research_replay_count": 1,
  "evidence_count": 1,
  "reports_count": 1
}`+"\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "tmp", "research-cert.ok"), "ok\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "tmp", "research-portability.ok"), "ok\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "tmp", "research-docs-drift.ok"), "ok\n")

	out, err := runScriptFromRepo(t, repo, "spec06_dod_matrix.sh", "--db", dbPath, "--cert-ts", "2026-02-24T00:00:00Z")
	if err == nil {
		t.Fatalf("expected failure without research-parallel.ok, output=\n%s", out)
	}
	if !strings.Contains(out, "spec06:dod: failed") {
		t.Fatalf("expected failure banner, output=\n%s", out)
	}

	matrixPath := filepath.Join(repo, "tmp", "spec06-dod-matrix.json")
	data, readErr := os.ReadFile(matrixPath)
	if readErr != nil {
		t.Fatalf("expected matrix output: %v", readErr)
	}
	if !strings.Contains(string(data), `"DOD-S06-2"`) || !strings.Contains(string(data), `"pass": false`) {
		t.Fatalf("expected DOD-S06-2 fail in matrix, got:\n%s", string(data))
	}
}
