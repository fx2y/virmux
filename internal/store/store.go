package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Run struct {
	ID         string
	Task       string
	Label      string
	AgentID    string
	ImageSHA   string
	KernelSHA  string
	RootfsSHA  string
	SnapshotID string
	CostEst    float64
	StartedAt  time.Time
}

type Store struct {
	db *sql.DB
}

type ToolCall struct {
	RunID      string
	Seq        int64
	ReqID      int64
	Tool       string
	InputHash  string
	OutputHash string
	InputRef   string
	OutputRef  string
	StdoutRef  string
	StderrRef  string
	RC         int
	DurMS      int64
	BytesIn    int64
	BytesOut   int64
	ErrorCode  string
}

type Score struct {
	RunID        string
	Skill        string
	Score        float64
	Pass         bool
	Critique     string
	JudgeCfgHash string
	ArtifactHash string
	CreatedAt    time.Time
}

type JudgeRun struct {
	RunID             string
	Skill             string
	RubricHash        string
	JudgeCfgHash      string
	ArtifactHash      string
	MetricsJSON       string
	Critique          string
	Score             float64
	Pass              bool
	CreatedAt         time.Time
	ModelID           string
	PromptHash        string
	SchemaVer         string
	Mode              string
	JudgeInvalidCount int
}

type EvalRun struct {
	ID            string
	Skill         string
	Cohort        string
	BaseRef       string
	HeadRef       string
	Provider      string
	FixturesHash  string
	CfgSHA256     string
	CfgPath       string
	ResultsSHA256 string
	ResultsPath   string
	VerdictSHA256 string
	VerdictPath   string
	ScoreP50Base  float64
	ScoreP50Head  float64
	FailRateBase  float64
	FailRateHead  float64
	CostTotalBase float64
	CostTotalHead float64
	ScoreP50Delta float64
	FailRateDelta float64
	CostDelta     float64
	Pass          bool
	VerdictJSON   string
	CreatedAt     time.Time
}

type EvalCase struct {
	EvalRunID string
	FixtureID string
	BaseScore float64
	HeadScore float64
	BasePass  bool
	HeadPass  bool
	BaseCost  float64
	HeadCost  float64
	CreatedAt time.Time
}

type Experiment struct {
	ID        string
	Skill     string
	BaseRef   string
	HeadRef   string
	CreatedAt time.Time
}

type Comparison struct {
	ExperimentID string
	FixtureID    string
	Winner       string // "A", "B", "tie"
	Rationale    string
	CreatedAt    time.Time
}

type ExperimentReport struct {
	ExperimentID string  `json:"experiment_id"`
	Skill        string  `json:"skill"`
	WinsA        int     `json:"wins_a"`
	WinsB        int     `json:"wins_b"`
	Ties         int     `json:"ties"`
	WinRate      float64 `json:"win_rate"`
	Total        int     `json:"total"`
}

type Promotion struct {
	ID            string
	Skill         string
	Tag           string
	BaseRef       string
	HeadRef       string
	FromRef       string
	ToRef         string
	Reason        string
	MetricsJSON   string
	CommitSHA     string
	Op            string // "promote" or "rollback"
	EvalRunID     string // Can be empty for rollbacks
	VerdictSHA256 string
	Actor         string
	CreatedAt     time.Time
}

type RefineRun struct {
	ID         string
	RunID      string
	Skill      string
	EvalRunID  string
	Branch     string
	CommitSHA  string
	PatchHash  string
	PatchPath  string
	PRBodyPath string
	HunkCount  int
	ToolsEdit  bool
	CreatedAt  time.Time
}

type SuggestRun struct {
	ID         string
	Skill      string
	MotifKey   string
	Branch     string
	CommitSHA  string
	PRBodyHash string
	PRBodyPath string
	RunIDsJSON string
	CreatedAt  time.Time
}

type CanaryRun struct {
	ID               string
	Skill            string
	EvalRunID        string
	CuratedEvalRunID string
	DsetPath         string
	DsetSHA256       string
	DsetCount        int
	CandidateRef     string
	BaselineRef      string
	GateVerdictJSON  string
	Action           string
	ActionRef        string
	CaughtByCanary   bool
	BacklogPath      string
	SummaryPath      string
	CreatedAt        time.Time
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}
	if err := ensureSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func ensureSchema(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS runs (
  id TEXT PRIMARY KEY,
  task TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  agent_id TEXT NOT NULL DEFAULT 'default',
  image_sha TEXT NOT NULL,
  kernel_sha TEXT NOT NULL DEFAULT '',
  rootfs_sha TEXT NOT NULL DEFAULT '',
  snapshot_id TEXT NOT NULL DEFAULT '',
  cost_est REAL NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'running',
  started_at TEXT NOT NULL,
  ended_at TEXT,
  boot_ms INTEGER NOT NULL DEFAULT 0,
  resume_ms INTEGER NOT NULL DEFAULT 0,
  trace_path TEXT NOT NULL DEFAULT '',
  source_bundle TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  ts TEXT NOT NULL,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL DEFAULT '{}',
  FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS slack_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT NOT NULL,
  event_type TEXT NOT NULL,
  payload TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS artifacts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  path TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  bytes INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS tool_calls (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  req_id INTEGER NOT NULL DEFAULT 0,
  tool TEXT NOT NULL,
  input_hash TEXT NOT NULL,
  output_hash TEXT NOT NULL,
  input_ref TEXT NOT NULL DEFAULT '',
  output_ref TEXT NOT NULL DEFAULT '',
  stdout_ref TEXT NOT NULL DEFAULT '',
  stderr_ref TEXT NOT NULL DEFAULT '',
  rc INTEGER NOT NULL DEFAULT 0,
  dur_ms INTEGER NOT NULL DEFAULT 0,
  bytes_in INTEGER NOT NULL DEFAULT 0,
  bytes_out INTEGER NOT NULL DEFAULT 0,
  error_code TEXT NOT NULL DEFAULT '',
  FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS scores (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  skill TEXT NOT NULL,
  score REAL NOT NULL,
  pass INTEGER NOT NULL DEFAULT 0,
  critique TEXT NOT NULL DEFAULT '[]',
  judge_cfg_hash TEXT NOT NULL,
  artifact_hash TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS judge_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  skill TEXT NOT NULL,
  rubric_hash TEXT NOT NULL,
  judge_cfg_hash TEXT NOT NULL,
  artifact_hash TEXT NOT NULL,
  metrics_json TEXT NOT NULL DEFAULT '{}',
  critique TEXT NOT NULL DEFAULT '[]',
  score REAL NOT NULL,
  pass INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  model_id TEXT NOT NULL DEFAULT '',
  prompt_hash TEXT NOT NULL DEFAULT '',
  schema_ver TEXT NOT NULL DEFAULT '',
  mode TEXT NOT NULL DEFAULT 'rules',
  judge_invalid_count INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS eval_runs (
  id TEXT PRIMARY KEY,
  skill TEXT NOT NULL,
  cohort TEXT NOT NULL DEFAULT '',
  base_ref TEXT NOT NULL,
  head_ref TEXT NOT NULL,
  provider TEXT NOT NULL,
  fixtures_hash TEXT NOT NULL,
  cfg_sha256 TEXT NOT NULL,
  cfg_path TEXT NOT NULL DEFAULT '',
  results_sha256 TEXT NOT NULL,
  results_path TEXT NOT NULL DEFAULT '',
  verdict_sha256 TEXT NOT NULL,
  verdict_path TEXT NOT NULL DEFAULT '',
  score_p50_base REAL NOT NULL DEFAULT 0,
  score_p50_head REAL NOT NULL DEFAULT 0,
  fail_rate_base REAL NOT NULL DEFAULT 0,
  fail_rate_head REAL NOT NULL DEFAULT 0,
  cost_total_base REAL NOT NULL DEFAULT 0,
  cost_total_head REAL NOT NULL DEFAULT 0,
  score_p50_delta REAL NOT NULL DEFAULT 0,
  fail_rate_delta REAL NOT NULL DEFAULT 0,
  cost_delta REAL NOT NULL DEFAULT 0,
  pass INTEGER NOT NULL DEFAULT 0,
  verdict_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS eval_cases (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  eval_run_id TEXT NOT NULL,
  fixture_id TEXT NOT NULL,
  base_score REAL NOT NULL DEFAULT 0,
  head_score REAL NOT NULL DEFAULT 0,
  base_pass INTEGER NOT NULL DEFAULT 0,
  head_pass INTEGER NOT NULL DEFAULT 0,
  base_cost REAL NOT NULL DEFAULT 0,
  head_cost REAL NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  FOREIGN KEY(eval_run_id) REFERENCES eval_runs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS promotions (
  id TEXT PRIMARY KEY,
  skill TEXT NOT NULL,
  tag TEXT NOT NULL,
  base_ref TEXT NOT NULL,
  head_ref TEXT NOT NULL,
  from_ref TEXT NOT NULL DEFAULT '',
  to_ref TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  metrics_json TEXT NOT NULL DEFAULT '{}',
  commit_sha TEXT NOT NULL DEFAULT '',
  op TEXT NOT NULL DEFAULT 'promote',
  eval_run_id TEXT,
  verdict_sha256 TEXT NOT NULL,
  actor TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  FOREIGN KEY(eval_run_id) REFERENCES eval_runs(id) ON DELETE RESTRICT
);
CREATE TABLE IF NOT EXISTS refine_runs (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  skill TEXT NOT NULL,
  eval_run_id TEXT NOT NULL,
  branch TEXT NOT NULL,
  commit_sha TEXT NOT NULL,
  patch_hash TEXT NOT NULL,
  patch_path TEXT NOT NULL DEFAULT '',
  pr_body_path TEXT NOT NULL DEFAULT '',
  hunk_count INTEGER NOT NULL DEFAULT 0,
  tools_edit INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS suggest_runs (
  id TEXT PRIMARY KEY,
  skill TEXT NOT NULL,
  motif_key TEXT NOT NULL,
  branch TEXT NOT NULL,
  commit_sha TEXT NOT NULL,
  pr_body_hash TEXT NOT NULL,
  pr_body_path TEXT NOT NULL DEFAULT '',
  run_ids_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS experiments (
  id TEXT PRIMARY KEY,
  skill TEXT NOT NULL,
  base_ref TEXT NOT NULL,
  head_ref TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS comparisons (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  experiment_id TEXT NOT NULL,
  fixture_id TEXT NOT NULL,
  winner TEXT NOT NULL,
  rationale TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  FOREIGN KEY(experiment_id) REFERENCES experiments(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS canary_runs (
  id TEXT PRIMARY KEY,
  skill TEXT NOT NULL,
  eval_run_id TEXT NOT NULL,
  curated_eval_run_id TEXT NOT NULL DEFAULT '',
  dset_path TEXT NOT NULL DEFAULT '',
  dset_sha256 TEXT NOT NULL DEFAULT '',
  dset_count INTEGER NOT NULL DEFAULT 0,
  candidate_ref TEXT NOT NULL DEFAULT '',
  baseline_ref TEXT NOT NULL DEFAULT '',
  gate_verdict_json TEXT NOT NULL DEFAULT '{}',
  action TEXT NOT NULL DEFAULT '',
  action_ref TEXT NOT NULL DEFAULT '',
  caught_by_canary INTEGER NOT NULL DEFAULT 0,
  backlog_path TEXT NOT NULL DEFAULT '',
  summary_path TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  FOREIGN KEY(eval_run_id) REFERENCES eval_runs(id) ON DELETE RESTRICT,
  FOREIGN KEY(curated_eval_run_id) REFERENCES eval_runs(id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_events_run_id ON events(run_id);
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at);
CREATE INDEX IF NOT EXISTS idx_artifacts_run_id ON artifacts(run_id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_run_seq ON tool_calls(run_id,seq);
CREATE INDEX IF NOT EXISTS idx_tool_calls_tool_input_hash ON tool_calls(tool,input_hash);
CREATE INDEX IF NOT EXISTS idx_scores_run_created ON scores(run_id,created_at);
CREATE INDEX IF NOT EXISTS idx_scores_skill_pass ON scores(skill,pass);
CREATE INDEX IF NOT EXISTS idx_judge_runs_run_created ON judge_runs(run_id,created_at);
CREATE INDEX IF NOT EXISTS idx_eval_runs_skill_created ON eval_runs(skill,created_at);
CREATE INDEX IF NOT EXISTS idx_eval_runs_cohort_created ON eval_runs(cohort,created_at);
CREATE INDEX IF NOT EXISTS idx_eval_cases_run_fixture ON eval_cases(eval_run_id,fixture_id);
CREATE INDEX IF NOT EXISTS idx_promotions_skill_created ON promotions(skill,created_at);
CREATE INDEX IF NOT EXISTS idx_refine_runs_run_created ON refine_runs(run_id,created_at);
CREATE INDEX IF NOT EXISTS idx_refine_runs_skill_created ON refine_runs(skill,created_at);
CREATE INDEX IF NOT EXISTS idx_suggest_runs_skill_created ON suggest_runs(skill,created_at);
CREATE INDEX IF NOT EXISTS idx_experiments_skill_created ON experiments(skill, created_at);
CREATE INDEX IF NOT EXISTS idx_comparisons_experiment_fixture ON comparisons(experiment_id, fixture_id);
CREATE INDEX IF NOT EXISTS idx_canary_runs_skill_created ON canary_runs(skill,created_at);
CREATE INDEX IF NOT EXISTS idx_canary_runs_eval_run ON canary_runs(eval_run_id);
`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN agent_id TEXT NOT NULL DEFAULT 'default'`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure runs.agent_id: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN kernel_sha TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure runs.kernel_sha: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN rootfs_sha TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure runs.rootfs_sha: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN snapshot_id TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure runs.snapshot_id: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN cost_est REAL NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure runs.cost_est: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN source_bundle TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure runs.source_bundle: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE judge_runs ADD COLUMN model_id TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure judge_runs.model_id: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE judge_runs ADD COLUMN prompt_hash TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure judge_runs.prompt_hash: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE judge_runs ADD COLUMN schema_ver TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure judge_runs.schema_ver: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE judge_runs ADD COLUMN mode TEXT NOT NULL DEFAULT 'rules'`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure judge_runs.mode: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE judge_runs ADD COLUMN judge_invalid_count INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure judge_runs.judge_invalid_count: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_judge_runs_skill_created_mode ON judge_runs(skill,created_at,mode)`); err != nil {
		return fmt.Errorf("ensure idx_judge_runs_skill_created_mode: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE promotions ADD COLUMN from_ref TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure promotions.from_ref: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE promotions ADD COLUMN to_ref TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure promotions.to_ref: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE promotions ADD COLUMN reason TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure promotions.reason: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE promotions ADD COLUMN metrics_json TEXT NOT NULL DEFAULT '{}'`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure promotions.metrics_json: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE promotions ADD COLUMN commit_sha TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure promotions.commit_sha: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE promotions ADD COLUMN op TEXT NOT NULL DEFAULT 'promote'`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("ensure promotions.op: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_canary_runs_skill_created ON canary_runs(skill,created_at)`); err != nil {
		return fmt.Errorf("ensure idx_canary_runs_skill_created: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_canary_runs_eval_run ON canary_runs(eval_run_id)`); err != nil {
		return fmt.Errorf("ensure idx_canary_runs_eval_run: %w", err)
	}
	return nil
}

func (s *Store) StartRun(ctx context.Context, run Run) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO runs(id,task,label,agent_id,image_sha,kernel_sha,rootfs_sha,snapshot_id,cost_est,status,started_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		run.ID,
		run.Task,
		run.Label,
		run.AgentID,
		run.ImageSHA,
		run.KernelSHA,
		run.RootfsSHA,
		run.SnapshotID,
		run.CostEst,
		"running",
		run.StartedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	return nil
}

func (s *Store) FinishRun(ctx context.Context, runID, status string, bootMS, resumeMS int64, tracePath, snapshotID string, costEst float64, endedAt time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE runs SET status=?, ended_at=?, boot_ms=?, resume_ms=?, trace_path=?, snapshot_id=?, cost_est=? WHERE id=?`,
		status,
		endedAt.UTC().Format(time.RFC3339Nano),
		bootMS,
		resumeMS,
		tracePath,
		snapshotID,
		costEst,
		runID,
	)
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	return nil
}

func (s *Store) InsertArtifact(ctx context.Context, runID, path, sha256 string, sizeBytes int64) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO artifacts(run_id,path,sha256,bytes) VALUES(?,?,?,?)`,
		runID,
		path,
		sha256,
		sizeBytes,
	)
	if err != nil {
		return fmt.Errorf("insert artifact: %w", err)
	}
	return nil
}

func (s *Store) InsertEvent(ctx context.Context, runID, kind, payload string, ts time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO events(run_id,ts,kind,payload) VALUES(?,?,?,?)`,
		runID,
		ts.UTC().Format(time.RFC3339Nano),
		kind,
		payload,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func (s *Store) InsertToolCall(ctx context.Context, tc ToolCall) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO tool_calls(run_id,seq,req_id,tool,input_hash,output_hash,input_ref,output_ref,stdout_ref,stderr_ref,rc,dur_ms,bytes_in,bytes_out,error_code)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		tc.RunID,
		tc.Seq,
		tc.ReqID,
		tc.Tool,
		tc.InputHash,
		tc.OutputHash,
		tc.InputRef,
		tc.OutputRef,
		tc.StdoutRef,
		tc.StderrRef,
		tc.RC,
		tc.DurMS,
		tc.BytesIn,
		tc.BytesOut,
		tc.ErrorCode,
	)
	if err != nil {
		return fmt.Errorf("insert tool_call: %w", err)
	}
	return nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) InsertSlackEvent(ctx context.Context, eventType, payload string, ts time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO slack_events(ts,event_type,payload) VALUES(?,?,?)`,
		ts.UTC().Format(time.RFC3339Nano),
		eventType,
		payload,
	)
	if err != nil {
		return fmt.Errorf("insert slack event: %w", err)
	}
	return nil
}

func (s *Store) InsertScore(ctx context.Context, row Score) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	passInt := 0
	if row.Pass {
		passInt = 1
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO scores(run_id,skill,score,pass,critique,judge_cfg_hash,artifact_hash,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		row.RunID,
		row.Skill,
		row.Score,
		passInt,
		row.Critique,
		row.JudgeCfgHash,
		row.ArtifactHash,
		row.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert score: %w", err)
	}
	return nil
}

func (s *Store) InsertJudgeRun(ctx context.Context, row JudgeRun) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	passInt := 0
	if row.Pass {
		passInt = 1
	}
	if row.Mode == "" {
		row.Mode = "rules"
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO judge_runs(run_id,skill,rubric_hash,judge_cfg_hash,artifact_hash,metrics_json,critique,score,pass,created_at,model_id,prompt_hash,schema_ver,mode,judge_invalid_count) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		row.RunID,
		row.Skill,
		row.RubricHash,
		row.JudgeCfgHash,
		row.ArtifactHash,
		row.MetricsJSON,
		row.Critique,
		row.Score,
		passInt,
		row.CreatedAt.UTC().Format(time.RFC3339Nano),
		row.ModelID,
		row.PromptHash,
		row.SchemaVer,
		row.Mode,
		row.JudgeInvalidCount,
	)
	if err != nil {
		return fmt.Errorf("insert judge_run: %w", err)
	}
	return nil
}

func (s *Store) InsertEvalRun(ctx context.Context, row EvalRun) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	passInt := 0
	if row.Pass {
		passInt = 1
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO eval_runs(id,skill,cohort,base_ref,head_ref,provider,fixtures_hash,cfg_sha256,cfg_path,results_sha256,results_path,verdict_sha256,verdict_path,score_p50_base,score_p50_head,fail_rate_base,fail_rate_head,cost_total_base,cost_total_head,score_p50_delta,fail_rate_delta,cost_delta,pass,verdict_json,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		row.ID,
		row.Skill,
		row.Cohort,
		row.BaseRef,
		row.HeadRef,
		row.Provider,
		row.FixturesHash,
		row.CfgSHA256,
		row.CfgPath,
		row.ResultsSHA256,
		row.ResultsPath,
		row.VerdictSHA256,
		row.VerdictPath,
		row.ScoreP50Base,
		row.ScoreP50Head,
		row.FailRateBase,
		row.FailRateHead,
		row.CostTotalBase,
		row.CostTotalHead,
		row.ScoreP50Delta,
		row.FailRateDelta,
		row.CostDelta,
		passInt,
		row.VerdictJSON,
		row.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert eval_run: %w", err)
	}
	return nil
}

func (s *Store) InsertEvalCase(ctx context.Context, row EvalCase) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	basePass := 0
	headPass := 0
	if row.BasePass {
		basePass = 1
	}
	if row.HeadPass {
		headPass = 1
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO eval_cases(eval_run_id,fixture_id,base_score,head_score,base_pass,head_pass,base_cost,head_cost,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		row.EvalRunID,
		row.FixtureID,
		row.BaseScore,
		row.HeadScore,
		basePass,
		headPass,
		row.BaseCost,
		row.HeadCost,
		row.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert eval_case: %w", err)
	}
	return nil
}

func (s *Store) GetEvalRun(ctx context.Context, id string) (EvalRun, error) {
	var row EvalRun
	var passInt int
	var created string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id,skill,cohort,base_ref,head_ref,provider,fixtures_hash,cfg_sha256,cfg_path,results_sha256,results_path,verdict_sha256,verdict_path,score_p50_base,score_p50_head,fail_rate_base,fail_rate_head,cost_total_base,cost_total_head,score_p50_delta,fail_rate_delta,cost_delta,pass,verdict_json,created_at
		 FROM eval_runs WHERE id=?`,
		id,
	).Scan(
		&row.ID,
		&row.Skill,
		&row.Cohort,
		&row.BaseRef,
		&row.HeadRef,
		&row.Provider,
		&row.FixturesHash,
		&row.CfgSHA256,
		&row.CfgPath,
		&row.ResultsSHA256,
		&row.ResultsPath,
		&row.VerdictSHA256,
		&row.VerdictPath,
		&row.ScoreP50Base,
		&row.ScoreP50Head,
		&row.FailRateBase,
		&row.FailRateHead,
		&row.CostTotalBase,
		&row.CostTotalHead,
		&row.ScoreP50Delta,
		&row.FailRateDelta,
		&row.CostDelta,
		&passInt,
		&row.VerdictJSON,
		&created,
	)
	if err != nil {
		return EvalRun{}, fmt.Errorf("query eval_run %s: %w", id, err)
	}
	row.Pass = passInt != 0
	row.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return row, nil
}

func (s *Store) InsertPromotion(ctx context.Context, row Promotion) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	if row.Op == "" {
		row.Op = "promote"
	}
	var evalRunID sql.NullString
	if row.EvalRunID != "" {
		evalRunID = sql.NullString{String: row.EvalRunID, Valid: true}
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO promotions(id,skill,tag,base_ref,head_ref,from_ref,to_ref,reason,metrics_json,commit_sha,op,eval_run_id,verdict_sha256,actor,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		row.ID,
		row.Skill,
		row.Tag,
		row.BaseRef,
		row.HeadRef,
		row.FromRef,
		row.ToRef,
		row.Reason,
		row.MetricsJSON,
		row.CommitSHA,
		row.Op,
		evalRunID,
		row.VerdictSHA256,
		row.Actor,
		row.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert promotion: %w", err)
	}
	return nil
}

func (s *Store) InsertRefineRun(ctx context.Context, row RefineRun) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	toolsEdit := 0
	if row.ToolsEdit {
		toolsEdit = 1
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO refine_runs(id,run_id,skill,eval_run_id,branch,commit_sha,patch_hash,patch_path,pr_body_path,hunk_count,tools_edit,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		row.ID,
		row.RunID,
		row.Skill,
		row.EvalRunID,
		row.Branch,
		row.CommitSHA,
		row.PatchHash,
		row.PatchPath,
		row.PRBodyPath,
		row.HunkCount,
		toolsEdit,
		row.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert refine_run: %w", err)
	}
	return nil
}

func (s *Store) InsertSuggestRun(ctx context.Context, row SuggestRun) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO suggest_runs(id,skill,motif_key,branch,commit_sha,pr_body_hash,pr_body_path,run_ids_json,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		row.ID,
		row.Skill,
		row.MotifKey,
		row.Branch,
		row.CommitSHA,
		row.PRBodyHash,
		row.PRBodyPath,
		row.RunIDsJSON,
		row.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert suggest_run: %w", err)
	}
	return nil
}

func (s *Store) InsertCanaryRun(ctx context.Context, row CanaryRun) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	caught := 0
	if row.CaughtByCanary {
		caught = 1
	}
	var curated sql.NullString
	if row.CuratedEvalRunID != "" {
		curated = sql.NullString{String: row.CuratedEvalRunID, Valid: true}
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO canary_runs(id,skill,eval_run_id,curated_eval_run_id,dset_path,dset_sha256,dset_count,candidate_ref,baseline_ref,gate_verdict_json,action,action_ref,caught_by_canary,backlog_path,summary_path,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		row.ID,
		row.Skill,
		row.EvalRunID,
		curated,
		row.DsetPath,
		row.DsetSHA256,
		row.DsetCount,
		row.CandidateRef,
		row.BaselineRef,
		row.GateVerdictJSON,
		row.Action,
		row.ActionRef,
		caught,
		row.BacklogPath,
		row.SummaryPath,
		row.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert canary_run: %w", err)
	}
	return nil
}

func (s *Store) InsertExperiment(ctx context.Context, row Experiment) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO experiments(id,skill,base_ref,head_ref,created_at) VALUES(?,?,?,?,?)`,
		row.ID,
		row.Skill,
		row.BaseRef,
		row.HeadRef,
		row.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert experiment: %w", err)
	}
	return nil
}

func (s *Store) InsertComparison(ctx context.Context, row Comparison) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO comparisons(experiment_id,fixture_id,winner,rationale,created_at) VALUES(?,?,?,?,?)`,
		row.ExperimentID,
		row.FixtureID,
		row.Winner,
		row.Rationale,
		row.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert comparison: %w", err)
	}
	return nil
}

func (s *Store) GetExperimentReport(ctx context.Context, id string) (ExperimentReport, error) {
	var report ExperimentReport
	report.ExperimentID = id
	err := s.db.QueryRowContext(ctx, `SELECT skill FROM experiments WHERE id=?`, id).Scan(&report.Skill)
	if err != nil {
		return report, fmt.Errorf("query experiment skill: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT winner, COUNT(*) FROM comparisons WHERE experiment_id=? GROUP BY winner`, id)
	if err != nil {
		return report, fmt.Errorf("query comparison counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var winner string
		var count int
		if err := rows.Scan(&winner, &count); err != nil {
			return report, err
		}
		switch winner {
		case "A":
			report.WinsA = count
		case "B":
			report.WinsB = count
		case "tie":
			report.Ties = count
		}
	}
	report.Total = report.WinsA + report.WinsB + report.Ties
	if report.Total > 0 {
		report.WinRate = float64(report.WinsB) / float64(report.Total)
	}
	return report, nil
}

func (s *Store) LatestEvalRunBySkill(ctx context.Context, skill string) (EvalRun, error) {
	var row EvalRun
	var passInt int
	var created string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id,skill,cohort,base_ref,head_ref,provider,fixtures_hash,cfg_sha256,cfg_path,results_sha256,results_path,verdict_sha256,verdict_path,score_p50_base,score_p50_head,fail_rate_base,fail_rate_head,cost_total_base,cost_total_head,score_p50_delta,fail_rate_delta,cost_delta,pass,verdict_json,created_at
		 FROM eval_runs WHERE skill=? ORDER BY datetime(created_at) DESC, id DESC LIMIT 1`,
		skill,
	).Scan(
		&row.ID,
		&row.Skill,
		&row.Cohort,
		&row.BaseRef,
		&row.HeadRef,
		&row.Provider,
		&row.FixturesHash,
		&row.CfgSHA256,
		&row.CfgPath,
		&row.ResultsSHA256,
		&row.ResultsPath,
		&row.VerdictSHA256,
		&row.VerdictPath,
		&row.ScoreP50Base,
		&row.ScoreP50Head,
		&row.FailRateBase,
		&row.FailRateHead,
		&row.CostTotalBase,
		&row.CostTotalHead,
		&row.ScoreP50Delta,
		&row.FailRateDelta,
		&row.CostDelta,
		&passInt,
		&row.VerdictJSON,
		&created,
	)
	if err != nil {
		return EvalRun{}, fmt.Errorf("query latest eval_run skill=%s: %w", skill, err)
	}
	row.Pass = passInt != 0
	row.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return row, nil
}

func (s *Store) LatestPassingEvalRunBySkill(ctx context.Context, skill string) (EvalRun, error) {
	var row EvalRun
	var passInt int
	var created string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id,skill,cohort,base_ref,head_ref,provider,fixtures_hash,cfg_sha256,cfg_path,results_sha256,results_path,verdict_sha256,verdict_path,score_p50_base,score_p50_head,fail_rate_base,fail_rate_head,cost_total_base,cost_total_head,score_p50_delta,fail_rate_delta,cost_delta,pass,verdict_json,created_at
		 FROM eval_runs WHERE skill=? AND pass=1 ORDER BY datetime(created_at) DESC, id DESC LIMIT 1`,
		skill,
	).Scan(
		&row.ID,
		&row.Skill,
		&row.Cohort,
		&row.BaseRef,
		&row.HeadRef,
		&row.Provider,
		&row.FixturesHash,
		&row.CfgSHA256,
		&row.CfgPath,
		&row.ResultsSHA256,
		&row.ResultsPath,
		&row.VerdictSHA256,
		&row.VerdictPath,
		&row.ScoreP50Base,
		&row.ScoreP50Head,
		&row.FailRateBase,
		&row.FailRateHead,
		&row.CostTotalBase,
		&row.CostTotalHead,
		&row.ScoreP50Delta,
		&row.FailRateDelta,
		&row.CostDelta,
		&passInt,
		&row.VerdictJSON,
		&created,
	)
	if err != nil {
		return EvalRun{}, fmt.Errorf("query latest passing eval_run skill=%s: %w", skill, err)
	}
	row.Pass = passInt != 0
	row.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return row, nil
}
