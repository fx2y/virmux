package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haris/virmux/internal/skill/research"
	yaml "gopkg.in/yaml.v2"
)

func TestResearchPlanWritesPlanYAML(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	
	// Create required directories and files
	sha := "test-sha"
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "vm", "images.lock"), sha)
	cacheDir := filepath.Join(repo, ".cache", "ghostfleet", "images", sha)
	os.MkdirAll(cacheDir, 0755)
	mustWriteScriptFixtureFile(t, filepath.Join(cacheDir, "firecracker"), "mock fc")
	mustWriteScriptFixtureFile(t, filepath.Join(cacheDir, "vmlinux"), "mock kernel")
	mustWriteScriptFixtureFile(t, filepath.Join(cacheDir, "rootfs.ext4"), "mock rootfs")

	os.MkdirAll(filepath.Join(repo, "runs"), 0755)
	os.MkdirAll(filepath.Join(repo, "agents"), 0755)
	os.MkdirAll(filepath.Join(repo, "volumes"), 0755)

	args := []string{
		"research", "plan",
		"--query", "test research query",
		"--images-lock", filepath.Join(repo, "vm", "images.lock"),
		"--runs-dir", filepath.Join(repo, "runs"),
		"--db", filepath.Join(repo, "runs", "virmux.sqlite"),
		"--agent", "test-agent",
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	if err := run(args); err != nil {
		os.Stdout = oldStdout
		t.Fatalf("research plan failed: %v", err)
	}

	w.Close()
	os.Stdout = oldStdout
	
	outBytes, _ := ioutil.ReadAll(r)
	out := string(outBytes)
	lines := strings.Split(out, "\n")
	var summary map[string]any
	var parseErr error
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if err := json.Unmarshal([]byte(line), &summary); err == nil {
			parseErr = nil
			break
		} else {
			parseErr = err
		}
	}
	if summary == nil {
		t.Fatalf("failed to find summary json: %v\noutput: %s", parseErr, out)
	}

	runDirVal, ok := summary["run_dir"].(string)
	if !ok {
		t.Fatalf("summary missing run_dir: %v", summary)
	}
	runDir := runDirVal
	if !filepath.IsAbs(runDir) && !strings.HasPrefix(runDir, repo) {
		runDir = filepath.Join(repo, runDir)
	}

	planPath := filepath.Join(runDir, "plan.yaml")
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("plan.yaml not found: %v", err)
	}

	planData, err := ioutil.ReadFile(planPath)
	if err != nil {
		t.Fatalf("failed to read plan.yaml: %v", err)
	}

	var plan research.Plan
	if err := yaml.Unmarshal(planData, &plan); err != nil {
		t.Fatalf("failed to unmarshal plan.yaml: %v", err)
	}

	if plan.Goal != "test research query" {
		t.Errorf("expected goal 'test research query', got '%s'", plan.Goal)
	}

	if plan.PlanID == "" {
		t.Errorf("expected plan_id to be set")
	}

	// Verify plan_id is hash of content
	expectedHash := plan.Hash()
	if plan.PlanID != expectedHash {
		t.Errorf("expected plan_id %s, got %s", expectedHash, plan.PlanID)
	}

	// Verify trace contains research.plan.created
	tracePath := filepath.Join(runDir, "trace.ndjson")
	traceData, err := ioutil.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("failed to read trace: %v", err)
	}
	if !strings.Contains(string(traceData), "research.plan.created") {
		t.Errorf("trace missing research.plan.created event")
	}
}
