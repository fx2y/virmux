package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/haris/virmux/internal/store"
)

func TestExportImportDeterministicRoundTrip(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not installed")
	}
	tmp := t.TempDir()
	runsDir := filepath.Join(tmp, "runs")
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	runID := "rid-exp"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "trace.ndjson"), []byte(`{"ts":"2026-02-22T00:00:00Z","run_id":"rid-exp","seq":1,"type":"event","task":"vm:run","event":"run.started","payload":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("trace.ndjson", filepath.Join(runDir, "trace.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "meta.json"), []byte(`{"run_id":"rid-exp"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.req.json"), []byte(`{"req":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.res.json"), []byte(`{"ok":true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.StartRun(ctx, store.Run{
		ID:        runID,
		Task:      "vm:run",
		Label:     "c4",
		AgentID:   "A",
		ImageSHA:  "img",
		KernelSHA: "k",
		RootfsSHA: "r",
		StartedAt: time.Date(2026, 2, 22, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEvent(ctx, runID, "run.started", `{}`, time.Date(2026, 2, 22, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertArtifact(ctx, runID, filepath.Join(runDir, "trace.ndjson"), "abc", 3); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertToolCall(ctx, store.ToolCall{
		RunID:      runID,
		Seq:        1,
		ReqID:      1,
		Tool:       "shell.exec",
		InputHash:  "sha256:in",
		OutputHash: "sha256:out",
		InputRef:   "toolio/000001.req.json",
		OutputRef:  "toolio/000001.res.json",
		StdoutRef:  "artifacts/1.out",
		StderrRef:  "artifacts/1.err",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertScore(ctx, store.Score{
		RunID:        runID,
		Skill:        "dd",
		Score:        0.9,
		Pass:         true,
		Critique:     `["ok"]`,
		JudgeCfgHash: "sha256:cfg",
		ArtifactHash: "sha256:art",
		CreatedAt:    time.Date(2026, 2, 22, 0, 0, 1, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertJudgeRun(ctx, store.JudgeRun{
		RunID:        runID,
		Skill:        "dd",
		RubricHash:   "sha256:rubric",
		JudgeCfgHash: "sha256:cfg",
		ArtifactHash: "sha256:art",
		MetricsJSON:  `{"format":1,"completeness":1,"actionability":1}`,
		Critique:     `["ok"]`,
		Score:        0.9,
		Pass:         true,
		CreatedAt:    time.Date(2026, 2, 22, 0, 0, 1, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEvalRun(ctx, store.EvalRun{
		ID:            "ab-exp",
		Skill:         "dd",
		Cohort:        "qa-skill-c4",
		BaseRef:       "base",
		HeadRef:       "head",
		Provider:      "openai:gpt-4.1-mini",
		FixturesHash:  "sha256:fixtures",
		CfgSHA256:     "sha256:cfg",
		ResultsSHA256: "sha256:res",
		VerdictSHA256: "sha256:verdict",
		VerdictJSON:   `{"pass":true}`,
		Pass:          true,
		CreatedAt:     time.Date(2026, 2, 22, 0, 0, 1, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertRefineRun(ctx, store.RefineRun{
		ID:         "ref-exp",
		RunID:      runID,
		Skill:      "dd",
		EvalRunID:  "ab-exp",
		Branch:     "refine/dd/rid-exp",
		CommitSHA:  "abc123",
		PatchHash:  "sha256:patch",
		PatchPath:  "rid-exp/refine.patch",
		PRBodyPath: "rid-exp/refine-pr.md",
		HunkCount:  1,
		ToolsEdit:  false,
		CreatedAt:  time.Date(2026, 2, 22, 0, 0, 1, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishRun(ctx, runID, "ok", 1, 0, filepath.Join(runDir, "trace.ndjson"), "", 0, time.Date(2026, 2, 22, 0, 0, 1, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	b1 := filepath.Join(tmp, "b1.tar.zst")
	b2 := filepath.Join(tmp, "b2.tar.zst")
	if err := exportRunBundle(context.Background(), dbPath, runsDir, runID, b1, exportOptions{}); err != nil {
		t.Fatalf("export #1: %v", err)
	}
	if err := exportRunBundle(context.Background(), dbPath, runsDir, runID, b2, exportOptions{}); err != nil {
		t.Fatalf("export #2: %v", err)
	}
	raw1, err := os.ReadFile(b1)
	if err != nil {
		t.Fatal(err)
	}
	raw2, err := os.ReadFile(b2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw1, raw2) {
		t.Fatalf("deterministic export mismatch")
	}

	importRuns := filepath.Join(tmp, "runs-import")
	importDB := filepath.Join(importRuns, "virmux.sqlite")
	if err := importRunBundle(context.Background(), b1, importDB, importRuns); err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := os.Stat(filepath.Join(importRuns, runID, "trace.ndjson")); err != nil {
		t.Fatalf("imported run dir missing trace: %v", err)
	}
	ist, err := store.Open(importDB)
	if err != nil {
		t.Fatal(err)
	}
	defer ist.Close()
	var sourceBundle string
	if err := ist.DB().QueryRow(`SELECT source_bundle FROM runs WHERE id=?`, runID).Scan(&sourceBundle); err != nil {
		t.Fatal(err)
	}
	if sourceBundle == "" {
		t.Fatalf("expected source_bundle on imported run")
	}
	var tcCount int
	if err := ist.DB().QueryRow(`SELECT COUNT(*) FROM tool_calls WHERE run_id=?`, runID).Scan(&tcCount); err != nil {
		t.Fatal(err)
	}
	if tcCount != 1 {
		t.Fatalf("expected 1 tool_call, got %d", tcCount)
	}
	var scoreCount int
	if err := ist.DB().QueryRow(`SELECT COUNT(*) FROM scores WHERE run_id=?`, runID).Scan(&scoreCount); err != nil {
		t.Fatal(err)
	}
	if scoreCount != 1 {
		t.Fatalf("expected 1 score row, got %d", scoreCount)
	}
	var judgeCount int
	if err := ist.DB().QueryRow(`SELECT COUNT(*) FROM judge_runs WHERE run_id=?`, runID).Scan(&judgeCount); err != nil {
		t.Fatal(err)
	}
	if judgeCount != 1 {
		t.Fatalf("expected 1 judge_run row, got %d", judgeCount)
	}
	var refineCount int
	if err := ist.DB().QueryRow(`SELECT COUNT(*) FROM refine_runs WHERE run_id=?`, runID).Scan(&refineCount); err != nil {
		t.Fatal(err)
	}
	if refineCount != 1 {
		t.Fatalf("expected 1 refine_run row, got %d", refineCount)
	}
}

func TestExportRunBundleMarksPartialMeta(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not installed")
	}
	tmp := t.TempDir()
	runsDir := filepath.Join(tmp, "runs")
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	runID := "rid-partial"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "trace.ndjson"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.StartRun(ctx, store.Run{
		ID:        runID,
		Task:      "vm:run",
		Label:     "partial",
		AgentID:   "A",
		ImageSHA:  "img",
		KernelSHA: "k",
		RootfsSHA: "r",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishRun(ctx, runID, "failed", 0, 0, filepath.Join(runDir, "trace.ndjson"), "", 0, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	bundle := filepath.Join(tmp, "partial.tar.zst")
	if err := exportRunBundle(context.Background(), dbPath, runsDir, runID, bundle, exportOptions{Partial: true}); err != nil {
		t.Fatalf("partial export: %v", err)
	}

	stage := filepath.Join(tmp, "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractZstdTar(bundle, stage); err != nil {
		t.Fatal(err)
	}
	var meta exportBundleMeta
	if err := readJSONFile(filepath.Join(stage, "meta.json"), &meta); err != nil {
		t.Fatal(err)
	}
	if !meta.Partial {
		t.Fatalf("expected partial=true in export meta")
	}
	var rawMeta map[string]any
	b, err := os.ReadFile(filepath.Join(stage, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &rawMeta); err != nil {
		t.Fatal(err)
	}
	if rawMeta["partial"] != true {
		t.Fatalf("expected raw meta partial=true, got %#v", rawMeta["partial"])
	}
}

func TestValidateSymlinkTargetRejectsEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	linkPath := filepath.Join(root, "runs", "rid", "trace.jsonl")
	if _, err := validateSymlinkTarget(root, linkPath, "/etc/passwd"); err == nil {
		t.Fatalf("expected absolute symlink target rejection")
	}
	if _, err := validateSymlinkTarget(root, linkPath, "../../../../../etc/passwd"); err == nil {
		t.Fatalf("expected parent escape rejection")
	}
}

func TestValidateSymlinkTargetAllowsInTreeRelative(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	linkPath := filepath.Join(root, "runs", "rid", "trace.jsonl")
	got, err := validateSymlinkTarget(root, linkPath, "../rid/trace.ndjson")
	if err != nil {
		t.Fatalf("expected in-tree symlink target allowed, got %v", err)
	}
	if got != filepath.Clean("../rid/trace.ndjson") {
		t.Fatalf("cleaned symlink target mismatch: %q", got)
	}
	_, err = validateSymlinkTarget(root, linkPath, ".")
	if err == nil {
		t.Fatalf("expected '.' symlink target rejection")
	}
}
