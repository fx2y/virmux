package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestStoreSchemaAndFK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "virmux.sqlite")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	run := Run{
		ID:        "run-1",
		Task:      "vm:smoke",
		Label:     "test",
		AgentID:   "A",
		ImageSHA:  "abc",
		KernelSHA: "k1",
		RootfsSHA: "r1",
		StartedAt: time.Now(),
	}
	if err := s.StartRun(ctx, run); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := s.InsertEvent(ctx, run.ID, "run.started", `{}`, time.Now()); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if err := s.InsertEvent(ctx, "missing", "bad", `{}`, time.Now()); err == nil {
		t.Fatalf("expected fk error for unknown run_id")
	}
	if err := s.FinishRun(ctx, run.ID, "ok", 10, 0, "runs/run-1/trace.jsonl", "snap-1", 0.25, time.Now()); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	var got string
	if err := s.db.QueryRow(`SELECT agent_id FROM runs WHERE id=?`, run.ID).Scan(&got); err != nil {
		t.Fatalf("query agent_id: %v", err)
	}
	if got != "A" {
		t.Fatalf("expected agent_id A, got %q", got)
	}
	if err := s.InsertArtifact(ctx, run.ID, "runs/run-1/serial.log", "deadbeef", 42); err != nil {
		t.Fatalf("insert artifact: %v", err)
	}
	if err := s.InsertArtifact(ctx, "missing", "x", "y", 1); err == nil {
		t.Fatalf("expected fk error for missing run in artifacts")
	}
	if err := s.InsertToolCall(ctx, ToolCall{
		RunID:      run.ID,
		Seq:        1,
		ReqID:      1,
		Tool:       "shell.exec",
		InputHash:  "sha256:in",
		OutputHash: "sha256:out",
		StdoutRef:  "artifacts/1.out",
		StderrRef:  "artifacts/1.err",
	}); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	if err := s.InsertToolCall(ctx, ToolCall{RunID: "missing", Seq: 1, Tool: "shell.exec", InputHash: "x", OutputHash: "y"}); err == nil {
		t.Fatalf("expected fk error for missing run in tool_calls")
	}
	if err := s.InsertScore(ctx, Score{
		RunID:        run.ID,
		Skill:        "dd",
		Score:        0.9,
		Pass:         true,
		Critique:     `["ok"]`,
		JudgeCfgHash: "sha256:cfg",
		ArtifactHash: "sha256:art",
	}); err != nil {
		t.Fatalf("insert score: %v", err)
	}
	if err := s.InsertScore(ctx, Score{
		RunID:        "missing",
		Skill:        "dd",
		Score:        0.1,
		Pass:         false,
		Critique:     `["bad"]`,
		JudgeCfgHash: "sha256:cfg",
		ArtifactHash: "sha256:art",
	}); err == nil {
		t.Fatalf("expected fk error for missing run in scores")
	}
	if err := s.InsertJudgeRun(ctx, JudgeRun{
		RunID:        run.ID,
		Skill:        "dd",
		RubricHash:   "sha256:rubric",
		JudgeCfgHash: "sha256:cfg",
		ArtifactHash: "sha256:art",
		MetricsJSON:  `{"format":1}`,
		Critique:     `["ok"]`,
		Score:        0.9,
		Pass:         true,
	}); err != nil {
		t.Fatalf("insert judge_run: %v", err)
	}
	if err := s.InsertEvalRun(ctx, EvalRun{
		ID:            "ab-1",
		Skill:         "dd",
		Cohort:        "qa-skill-c3",
		BaseRef:       "base",
		HeadRef:       "head",
		Provider:      "openai:gpt-4.1-mini",
		FixturesHash:  "sha256:fixtures",
		CfgSHA256:     "sha256:cfg",
		ResultsSHA256: "sha256:res",
		VerdictSHA256: "sha256:verdict",
		VerdictJSON:   `{"pass":true}`,
		Pass:          true,
	}); err != nil {
		t.Fatalf("insert eval_run: %v", err)
	}
	if err := s.InsertEvalCase(ctx, EvalCase{
		EvalRunID: "ab-1",
		FixtureID: "case01",
		BaseScore: 0.8,
		HeadScore: 0.9,
		BasePass:  true,
		HeadPass:  true,
	}); err != nil {
		t.Fatalf("insert eval_case: %v", err)
	}
	if _, err := s.GetEvalRun(ctx, "ab-1"); err != nil {
		t.Fatalf("get eval_run: %v", err)
	}
	if err := s.InsertPromotion(ctx, Promotion{
		ID:            "promo-1",
		Skill:         "dd",
		Tag:           "skill/dd/prod",
		BaseRef:       "base",
		HeadRef:       "head",
		EvalRunID:     "ab-1",
		VerdictSHA256: "sha256:verdict",
		Actor:         "tester",
	}); err != nil {
		t.Fatalf("insert promotion: %v", err)
	}
	if _, err := s.LatestEvalRunBySkill(ctx, "dd"); err != nil {
		t.Fatalf("latest eval by skill: %v", err)
	}
	if err := s.InsertEvalRun(ctx, EvalRun{
		ID:            "ab-2",
		Skill:         "dd",
		Cohort:        "qa",
		BaseRef:       "b",
		HeadRef:       "h",
		Provider:      "fake",
		FixturesHash:  "sha256:fx2",
		CfgSHA256:     "sha256:cfg2",
		ResultsSHA256: "sha256:res2",
		VerdictSHA256: "sha256:ver2",
		VerdictJSON:   `{"pass":false}`,
		Pass:          false,
		CreatedAt:     time.Now().UTC().Add(1 * time.Minute),
	}); err != nil {
		t.Fatalf("insert eval_run fail row: %v", err)
	}
	latestPass, err := s.LatestPassingEvalRunBySkill(ctx, "dd")
	if err != nil {
		t.Fatalf("latest passing eval by skill: %v", err)
	}
	if latestPass.ID != "ab-1" || !latestPass.Pass {
		t.Fatalf("expected latest passing row ab-1, got %+v", latestPass)
	}
	if err := s.InsertRefineRun(ctx, RefineRun{
		ID:         "ref-1",
		RunID:      run.ID,
		Skill:      "dd",
		EvalRunID:  "ab-1",
		Branch:     "refine/dd/run-1",
		CommitSHA:  "abc123",
		PatchHash:  "sha256:patch",
		PatchPath:  "runs/run-1/refine.patch",
		PRBodyPath: "runs/run-1/refine-pr.md",
		HunkCount:  1,
		ToolsEdit:  false,
	}); err != nil {
		t.Fatalf("insert refine_run: %v", err)
	}
	if err := s.InsertSuggestRun(ctx, SuggestRun{
		ID:         "sug-1",
		Skill:      "suggest-dd-a1b2",
		MotifKey:   "motif",
		Branch:     "suggest/dd-a1b2",
		CommitSHA:  "def456",
		PRBodyHash: "sha256:pr",
		PRBodyPath: "runs/sug-1/suggest-pr.md",
		RunIDsJSON: `["run-1"]`,
	}); err != nil {
		t.Fatalf("insert suggest_run: %v", err)
	}
	if err := s.InsertCanaryRun(ctx, CanaryRun{
		ID:               "canary-1",
		Skill:            "dd",
		EvalRunID:        "ab-1",
		CuratedEvalRunID: "ab-2",
		DsetPath:         "dsets/prod_20260224.jsonl",
		DsetSHA256:       "sha256:dset",
		DsetCount:        3,
		CandidateRef:     "head",
		BaselineRef:      "base",
		GateVerdictJSON:  `{"pass":false}`,
		Action:           "rollback",
		ActionRef:        "base",
		CaughtByCanary:   true,
		BacklogPath:      "runs/ab-1/canary-backlog.md",
		SummaryPath:      "runs/ab-1/canary-summary.json",
	}); err != nil {
		t.Fatalf("insert canary_run: %v", err)
	}
	var idxCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_artifacts_run_id'`).Scan(&idxCount); err != nil {
		t.Fatalf("query artifacts index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_artifacts_run_id, got %d", idxCount)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_tool_calls_run_seq'`).Scan(&idxCount); err != nil {
		t.Fatalf("query tool_calls index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_tool_calls_run_seq, got %d", idxCount)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_scores_run_created'`).Scan(&idxCount); err != nil {
		t.Fatalf("query scores index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_scores_run_created, got %d", idxCount)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_judge_runs_run_created'`).Scan(&idxCount); err != nil {
		t.Fatalf("query judge_runs index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_judge_runs_run_created, got %d", idxCount)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_eval_runs_skill_created'`).Scan(&idxCount); err != nil {
		t.Fatalf("query eval_runs index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_eval_runs_skill_created, got %d", idxCount)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_eval_cases_run_fixture'`).Scan(&idxCount); err != nil {
		t.Fatalf("query eval_cases index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_eval_cases_run_fixture, got %d", idxCount)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_promotions_skill_created'`).Scan(&idxCount); err != nil {
		t.Fatalf("query promotions index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_promotions_skill_created, got %d", idxCount)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_refine_runs_run_created'`).Scan(&idxCount); err != nil {
		t.Fatalf("query refine_runs index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_refine_runs_run_created, got %d", idxCount)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_suggest_runs_skill_created'`).Scan(&idxCount); err != nil {
		t.Fatalf("query suggest_runs index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_suggest_runs_skill_created, got %d", idxCount)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_canary_runs_skill_created'`).Scan(&idxCount); err != nil {
		t.Fatalf("query canary_runs skill index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_canary_runs_skill_created, got %d", idxCount)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_canary_runs_eval_run'`).Scan(&idxCount); err != nil {
		t.Fatalf("query canary_runs eval index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected idx_canary_runs_eval_run, got %d", idxCount)
	}
}

func TestOpenMigratesLegacyRunsTable(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  task TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  image_sha TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'running',
  started_at TEXT NOT NULL,
  ended_at TEXT,
  boot_ms INTEGER NOT NULL DEFAULT 0,
  resume_ms INTEGER NOT NULL DEFAULT 0,
  trace_path TEXT NOT NULL DEFAULT ''
);`); err != nil {
		t.Fatalf("create legacy runs: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer s.Close()

	for _, col := range []string{"agent_id", "kernel_sha", "rootfs_sha", "snapshot_id", "cost_est", "source_bundle"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('runs') WHERE name=?`, col).Scan(&n); err != nil {
			t.Fatalf("query column %s: %v", col, err)
		}
		if n != 1 {
			t.Fatalf("missing migrated column %s", col)
		}
	}
	for _, tbl := range []string{"tool_calls", "scores", "judge_runs", "eval_runs", "eval_cases", "promotions", "refine_runs", "suggest_runs"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n); err != nil {
			t.Fatalf("query table %s: %v", tbl, err)
		}
		if n != 1 {
			t.Fatalf("missing migrated table %s", tbl)
		}
	}
}
