package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestCmdSkillPromoteRefusesStaleVerdict(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "runs", "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEvalRun(context.Background(), store.EvalRun{
		ID:            "ab-stale",
		Skill:         "dd",
		Cohort:        "qa-skill-c3",
		BaseRef:       "base",
		HeadRef:       "head",
		Provider:      "openai:gpt-4.1-mini",
		FixturesHash:  "sha256:fx",
		CfgSHA256:     "sha256:cfg",
		ResultsSHA256: "sha256:res",
		VerdictSHA256: "sha256:verdict",
		VerdictJSON:   `{"pass":true}`,
		Pass:          true,
		CreatedAt:     time.Now().UTC().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	err = cmdSkillPromote([]string{"--db", dbPath, "--repo-dir", tmp, "dd", "ab-stale"})
	if err == nil || !strings.Contains(err.Error(), "STALE_AB_VERDICT") {
		t.Fatalf("expected stale verdict refusal, got %v", err)
	}
}

func TestCmdSkillPromoteWritesTagAndPromotionRow(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	headRef := strings.TrimSpace(string(out))

	dbPath := filepath.Join(tmp, "runs", "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEvalRun(context.Background(), store.EvalRun{
		ID:            "ab-pass",
		Skill:         "dd",
		Cohort:        "qa-skill-c3",
		BaseRef:       headRef,
		HeadRef:       headRef,
		Provider:      "openai:gpt-4.1-mini",
		FixturesHash:  "sha256:fx",
		CfgSHA256:     "sha256:cfg",
		ResultsSHA256: "sha256:res",
		VerdictSHA256: "sha256:verdict",
		VerdictJSON:   `{"pass":true}`,
		Pass:          true,
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	tag := "skill/dd/prod"
	if err := cmdSkillPromote([]string{"--db", dbPath, "--repo-dir", repo, "--tag", tag, "dd", "ab-pass"}); err != nil {
		t.Fatalf("cmdSkillPromote: %v", err)
	}
	gotTag, err := exec.Command("git", "-C", repo, "rev-list", "-n", "1", tag).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(gotTag)) != headRef {
		t.Fatalf("tag ref mismatch got=%s want=%s", strings.TrimSpace(string(gotTag)), headRef)
	}
	ist, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ist.Close()
	var n int
	if err := ist.DB().QueryRow(`SELECT COUNT(*) FROM promotions WHERE skill='dd'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected one promotion row, got %d", n)
	}
}
