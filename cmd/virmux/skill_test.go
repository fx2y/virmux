package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haris/virmux/internal/store"
)

func TestCmdSkillJudgeWritesScoreRowsAndTraceEvent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runsDir := filepath.Join(tmp, "runs")
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	runID := "rid-skill-judge"
	runDir := filepath.Join(runsDir, runID)
	skillsDir := filepath.Join(tmp, "skills")
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "dd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "dd", "rubric.yaml"), []byte("criteria:\n- {id: format, w: 0.4, must: true}\n- {id: completeness, w: 0.4}\n- {id: actionability, w: 0.2}\npass: 0.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "trace.ndjson"), []byte(`{"ts":"2026-02-22T00:00:00Z","run_id":"rid-skill-judge","seq":1,"type":"event","task":"skill:run","event":"run.started","payload":{"label":"x"}}`+"\n"+`{"ts":"2026-02-22T00:00:01Z","run_id":"rid-skill-judge","seq":2,"type":"tool","task":"skill:run","event":"vm.tool.result","tool":"shell.exec","args_hash":"sha256:a","stdout_ref":"artifacts/1.out","stderr_ref":"artifacts/1.err","exit_code":0,"dur_ms":1,"bytes_in":1,"bytes_out":1,"payload":{"tool":"shell.exec","input_hash":"sha256:a","output_hash":"sha256:b","stdout_ref":"artifacts/1.out","stderr_ref":"artifacts/1.err","exit_code":0,"dur_ms":1,"bytes_in":1,"bytes_out":1}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.req.json"), []byte(`{"req":1,"tool":"shell.exec","args":{"cmd":"echo ok"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.res.json"), []byte(`{"req":1,"ok":true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "artifacts", "1.out"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "artifacts", "1.err"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "skill-run.json"), []byte(`{"skill":"dd","tool":"shell.exec","deterministic":true,"expect_files":[]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.StartRun(ctx, store.Run{
		ID:        runID,
		Task:      "skill:run",
		Label:     "c2",
		AgentID:   "default",
		ImageSHA:  "img",
		KernelSHA: "k",
		RootfsSHA: "r",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishRun(ctx, runID, "ok", 1, 0, filepath.Join(runDir, "trace.ndjson"), "", 0, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	if err := cmdSkillJudge([]string{"--runs-dir", runsDir, "--db", dbPath, "--skills-dir", skillsDir, runID}); err != nil {
		t.Fatalf("cmdSkillJudge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "score.json")); err != nil {
		t.Fatalf("score.json missing: %v", err)
	}
	ist, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ist.Close()
	var scores int
	if err := ist.DB().QueryRow(`SELECT COUNT(*) FROM scores WHERE run_id=?`, runID).Scan(&scores); err != nil {
		t.Fatal(err)
	}
	if scores != 1 {
		t.Fatalf("expected one score row, got %d", scores)
	}
	var judgeRuns int
	if err := ist.DB().QueryRow(`SELECT COUNT(*) FROM judge_runs WHERE run_id=?`, runID).Scan(&judgeRuns); err != nil {
		t.Fatal(err)
	}
	if judgeRuns != 1 {
		t.Fatalf("expected one judge_run row, got %d", judgeRuns)
	}
	var judgeEvents int
	if err := ist.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=? AND kind='skill.judge.scored'`, runID).Scan(&judgeEvents); err != nil {
		t.Fatal(err)
	}
	if judgeEvents != 1 {
		t.Fatalf("expected one skill.judge.scored event, got %d", judgeEvents)
	}
}
