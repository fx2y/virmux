package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSkillDocsDriftScriptPassesWithSpec05Canon(t *testing.T) {
	t.Parallel()
	repo := setupDocsDriftRepoFixture(t, false)
	out, err := runSkillDocsDriftScript(t, repo)
	if err != nil {
		t.Fatalf("expected success, err=%v output=\n%s", err, out)
	}
	okPath := filepath.Join(repo, "tmp", "skill-docs-drift.ok")
	if _, statErr := os.Stat(okPath); statErr != nil {
		t.Fatalf("expected marker at %s: %v", okPath, statErr)
	}
}

func TestSkillDocsDriftScriptFailsOnStaleGhostfleetCommand(t *testing.T) {
	t.Parallel()
	repo := setupDocsDriftRepoFixture(t, true)
	out, err := runSkillDocsDriftScript(t, repo)
	if err == nil {
		t.Fatalf("expected failure, output=\n%s", out)
	}
	if !strings.Contains(out, "stale command examples") {
		t.Fatalf("expected stale command diagnostics, output=\n%s", out)
	}
}

func setupDocsDriftRepoFixture(t *testing.T, includeStale bool) string {
	t.Helper()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	mustMkdirAll(t, filepath.Join(repo, "docs", "rfcs", "000-ghostfleet-compounding-os"))
	mustMkdirAll(t, filepath.Join(repo, "spec-0", "05"))

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

	baseDoc := "skills/<name>/{SKILL.md,tools.yaml,rubric.yaml,tests/*}\n"
	if includeStale {
		baseDoc += "ghostfleet ab run exp.yaml\n"
	}
	mustWriteFile(t, filepath.Join(repo, "docs", "rfcs", "000-ghostfleet-compounding-os.md"), baseDoc)
	mustWriteFile(t, filepath.Join(repo, "docs", "rfcs", "000-ghostfleet-compounding-os", "01-walkthroughs.md"), "walkthrough\n")
	mustWriteFile(t, filepath.Join(repo, "docs", "rfcs", "000-ghostfleet-compounding-os", "02-snippets.md"), "snips\n")
	mustWriteFile(t, filepath.Join(repo, "AGENTS.md"), "trace.ndjson\n")
	mustWriteFile(t, filepath.Join(repo, "spec-0", "04-htn.jsonl"), `{"id":"canon","cmd":"virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>"}`+"\n")
	mustWriteFile(t, filepath.Join(repo, "spec-0", "05", "cli-map.jsonl"), `{"id":"map.cli.ghostfleet->virmux","cmd_canon":"virmux skill <lint|run|judge|ab|refine|suggest|promote|replay>"}`+"\n")
	mustWriteFile(t, filepath.Join(repo, "README.md"), "x\n")

	runGit("add", ".")
	runGit("commit", "-m", "init")
	return repo
}

func runSkillDocsDriftScript(t *testing.T, repo string) (string, error) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "scripts", "skill_docs_drift.sh")
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
