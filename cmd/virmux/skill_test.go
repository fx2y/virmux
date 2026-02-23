package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	skillrun "github.com/haris/virmux/internal/skill/run"
	skillspec "github.com/haris/virmux/internal/skill/spec"
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

func TestCmdSkillRefineCreatesBranchCommitArtifactsAndAuditRow(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "tester")

	skillDir := filepath.Join(repo, "skills", "dd")
	if err := os.MkdirAll(filepath.Join(skillDir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: dd\ndescription: x\nrequires: {bins: [], env: [], config: []}\nos: [linux]\n---\n# Steps\nDo one thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "tools.yaml"), []byte("allowed_tools: [shell.exec]\nbudget: {tool_calls: 1, seconds: 20, tokens: 0}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "rubric.yaml"), []byte("criteria:\n- {id: format, w: 0.4, must: true}\n- {id: completeness, w: 0.4}\n- {id: actionability, w: 0.2}\npass: 0.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "tests", "case01.json"), []byte(`{"id":"case01","tool":"shell.exec","args":{"cmd":"echo ok"},"deterministic":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "init")
	headRef := runGit("rev-parse", "HEAD")

	runsDir := filepath.Join(repo, "runs")
	runID := "rid-refine"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "trace.ndjson"), []byte(`{"ts":"2026-02-23T00:00:00Z","run_id":"rid-refine","seq":1,"type":"event","task":"skill:run","event":"run.finished","payload":{"status":"ok"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "skill-run.json"), []byte(`{"skill":"dd","tool":"shell.exec","deterministic":true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "score.json"), []byte(`{"run_id":"rid-refine","skill":"dd","score":0.4,"pass":false,"criterion":[{"id":"format","value":0.1,"weight":0.4}],"rubric_hash":"sha256:r","judge_cfg_hash":"sha256:j"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.StartRun(ctx, store.Run{
		ID:        runID,
		Task:      "skill:run",
		Label:     "c4",
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
	if err := st.InsertEvalRun(ctx, store.EvalRun{
		ID:            "ab-pass",
		Skill:         "dd",
		Cohort:        "qa-skill-c4",
		BaseRef:       headRef,
		HeadRef:       headRef,
		Provider:      "openai:gpt-4.1-mini",
		FixturesHash:  "sha256:fx",
		CfgSHA256:     "sha256:cfg",
		ResultsSHA256: "sha256:res",
		VerdictSHA256: "sha256:verdict",
		VerdictJSON:   `{"pass":true}`,
		ScoreP50Delta: 0.10,
		FailRateDelta: -0.20,
		CostDelta:     0,
		Pass:          true,
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	if err := cmdSkillRefine([]string{"--db", dbPath, "--runs-dir", runsDir, "--skills-dir", filepath.Join(repo, "skills"), "--repo-dir", repo, runID}); err != nil {
		t.Fatalf("cmdSkillRefine: %v", err)
	}

	if _, err := os.Stat(filepath.Join(runDir, "refine.patch")); err != nil {
		t.Fatalf("refine.patch missing: %v", err)
	}
	prBodyPath := filepath.Join(runDir, "refine-pr.md")
	prBody, err := os.ReadFile(prBodyPath)
	if err != nil {
		t.Fatalf("read refine-pr.md: %v", err)
	}
	if !strings.Contains(string(prBody), "Trace: runs/rid-refine/trace.ndjson") || !strings.Contains(string(prBody), "AB: eval=ab-pass") {
		t.Fatalf("pr body missing required evidence links/deltas:\n%s", string(prBody))
	}
	toolsRaw, err := os.ReadFile(filepath.Join(skillDir, "tools.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(toolsRaw), "http.fetch") {
		t.Fatalf("unexpected tools.yaml mutation")
	}
	branch := runGit("branch", "--show-current")
	if branch != "refine/dd/rid-refine" {
		t.Fatalf("branch mismatch: %s", branch)
	}
	head := runGit("rev-parse", "HEAD")
	if strings.TrimSpace(head) == "" {
		t.Fatalf("missing HEAD after refine commit")
	}

	ist, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ist.Close()
	var n int
	if err := ist.DB().QueryRow(`SELECT COUNT(*) FROM refine_runs WHERE run_id=?`, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected one refine_run row, got %d", n)
	}
}

func TestCmdSkillRefineRejectsLargePatch(t *testing.T) {
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
	skillDir := filepath.Join(repo, "skills", "dd")
	if err := os.MkdirAll(filepath.Join(skillDir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: dd\ndescription: x\nrequires: {bins: [], env: [], config: []}\nos: [linux]\n---\n# Steps\nDo one thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "tools.yaml"), []byte("allowed_tools: [shell.exec]\nbudget: {tool_calls: 1, seconds: 20, tokens: 0}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "rubric.yaml"), []byte("criteria:\n- {id: format, w: 0.4, must: true}\n- {id: completeness, w: 0.4}\n- {id: actionability, w: 0.2}\npass: 0.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "tests", "case01.json"), []byte(`{"id":"case01","tool":"shell.exec","args":{"cmd":"echo ok"},"deterministic":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "init")
	headRefCmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	headRaw, err := headRefCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	headRef := strings.TrimSpace(string(headRaw))

	runsDir := filepath.Join(repo, "runs")
	runID := "rid-refine-big"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "trace.ndjson"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scoreDoc := map[string]any{"run_id": runID, "skill": "dd", "score": 0.1, "pass": false, "criterion": []map[string]any{{"id": "format", "value": 0.0}}, "rubric_hash": "sha256:r", "judge_cfg_hash": "sha256:j"}
	sb, _ := json.Marshal(scoreDoc)
	if err := os.WriteFile(filepath.Join(runDir, "score.json"), append(sb, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "skill-run.json"), []byte(`{"skill":"dd","tool":"shell.exec","deterministic":true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.StartRun(ctx, store.Run{ID: runID, Task: "skill:run", Label: "c4", AgentID: "d", ImageSHA: "img", KernelSHA: "k", RootfsSHA: "r", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishRun(ctx, runID, "ok", 0, 0, filepath.Join(runDir, "trace.ndjson"), "", 0, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEvalRun(ctx, store.EvalRun{
		ID:            "ab-pass2",
		Skill:         "dd",
		Cohort:        "qa-skill-c4",
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

	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	err = cmdSkillRefine([]string{"--db", dbPath, "--runs-dir", runsDir, "--skills-dir", filepath.Join(repo, "skills"), "--repo-dir", repo, "--max-hunks", "1", runID})
	if err == nil || !strings.Contains(err.Error(), "REFINE_PATCH_TOO_LARGE") {
		t.Fatalf("expected large patch refusal, got %v", err)
	}
}

func TestCmdSkillSuggestTriggersScaffoldAndPRBodyEvidence(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "tester")
	if err := os.MkdirAll(filepath.Join(repo, "skills", "dd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "dd", "SKILL.md"), []byte("---\nname: dd\ndescription: x\nrequires: {bins: [], env: [], config: []}\nos: [linux]\n---\n# Steps\nRun deterministic command.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "dd", "tools.yaml"), []byte("allowed_tools: [shell.exec]\nbudget: {tool_calls: 1, seconds: 20, tokens: 0}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "dd", "rubric.yaml"), []byte("criteria:\n- {id: format, w: 0.4, must: true}\n- {id: completeness, w: 0.4}\n- {id: actionability, w: 0.2}\npass: 0.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "init")

	runsDir := filepath.Join(repo, "runs")
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		runID := "rid-suggest-" + string(rune('1'+i))
		runDir := filepath.Join(runsDir, runID)
		if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.req.json"), []byte(`{"req":1,"tool":"shell.exec","args":{"cmd":"echo ok"}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := st.StartRun(ctx, store.Run{ID: runID, Task: "skill:run", Label: "c5", AgentID: "default", ImageSHA: "img", KernelSHA: "k", RootfsSHA: "r", StartedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishRun(ctx, runID, "ok", 0, 0, filepath.Join(runDir, "trace.ndjson"), "", 0.1, time.Now().UTC()); err != nil {
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
			CreatedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertToolCall(ctx, store.ToolCall{RunID: runID, Seq: 1, ReqID: int64(i + 1), Tool: "shell.exec", InputHash: "sha256:in", OutputHash: "sha256:out"}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertArtifact(ctx, runID, "artifacts/out.txt", "sha256:file", 2); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	if err := cmdSkillSuggest([]string{"--db", dbPath, "--runs-dir", runsDir, "--skills-dir", filepath.Join(repo, "skills"), "--repo-dir", repo}); err != nil {
		t.Fatalf("cmdSkillSuggest: %v", err)
	}
	branch := runGit("branch", "--show-current")
	if !strings.HasPrefix(branch, "suggest/") {
		t.Fatalf("expected suggest branch, got %s", branch)
	}
	dirs, err := filepath.Glob(filepath.Join(repo, "skills", "suggest-dd-*"))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("expected one suggested skill dir, got %v err=%v", dirs, err)
	}
	if _, err := skillspec.LintDirs([]string{dirs[0]}, skillspec.DefaultEligibilityEnv()); err != nil {
		t.Fatalf("generated skill lint: %v", err)
	}
	if _, err := skillrun.LoadFixture(filepath.Join(dirs[0], "tests", "case01.json")); err != nil {
		t.Fatalf("generated fixture parse: %v", err)
	}
	prs, err := filepath.Glob(filepath.Join(runsDir, "*-suggest", "suggest-pr.md"))
	if err != nil || len(prs) == 0 {
		t.Fatalf("expected suggest-pr artifact, got %v err=%v", prs, err)
	}
	body, err := os.ReadFile(prs[0])
	if err != nil {
		t.Fatal(err)
	}
	txt := string(body)
	if !strings.Contains(txt, "Motif key: ") || !strings.Contains(txt, "Evidence rows (runs):") || !strings.Contains(txt, "rid-suggest-1") {
		t.Fatalf("pr body missing motif/evidence rows:\n%s", txt)
	}
}

func TestCmdSkillSuggestBelowPassRateDoesNotTrigger(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "skills", "dd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "dd", "SKILL.md"), []byte("---\nname: dd\ndescription: x\nrequires: {bins: [], env: [], config: []}\nos: [linux]\n---\n# Steps\nRun deterministic command.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "dd", "tools.yaml"), []byte("allowed_tools: [shell.exec]\nbudget: {tool_calls: 1, seconds: 20, tokens: 0}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "dd", "rubric.yaml"), []byte("criteria:\n- {id: format, w: 0.4, must: true}\n- {id: completeness, w: 0.4}\n- {id: actionability, w: 0.2}\npass: 0.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "init")

	runsDir := filepath.Join(repo, "runs")
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		runID := "rid-suggest-low-" + string(rune('1'+i))
		runDir := filepath.Join(runsDir, runID)
		if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.req.json"), []byte(`{"req":1,"tool":"shell.exec","args":{"cmd":"echo ok"}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := st.StartRun(ctx, store.Run{ID: runID, Task: "skill:run", Label: "c5", AgentID: "default", ImageSHA: "img", KernelSHA: "k", RootfsSHA: "r", StartedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishRun(ctx, runID, "ok", 0, 0, filepath.Join(runDir, "trace.ndjson"), "", 0.1, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertScore(ctx, store.Score{
			RunID:        runID,
			Skill:        "dd",
			Score:        0.9,
			Pass:         i == 0,
			Critique:     `["ok"]`,
			JudgeCfgHash: "sha256:cfg",
			ArtifactHash: "sha256:art",
			CreatedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertToolCall(ctx, store.ToolCall{RunID: runID, Seq: 1, ReqID: int64(i + 1), Tool: "shell.exec", InputHash: "sha256:in", OutputHash: "sha256:out"}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertArtifact(ctx, runID, "artifacts/out.txt", "sha256:file", 2); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()
	err = cmdSkillSuggest([]string{"--db", dbPath, "--runs-dir", runsDir, "--skills-dir", filepath.Join(repo, "skills"), "--repo-dir", repo, "--min-pass-rate", "0.8"})
	if err == nil || !strings.Contains(err.Error(), "SUGGEST_NOT_TRIGGERED") {
		t.Fatalf("expected non-trigger error, got %v", err)
	}
}
