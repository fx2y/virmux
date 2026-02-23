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
	RunID        string
	Skill        string
	RubricHash   string
	JudgeCfgHash string
	ArtifactHash string
	MetricsJSON  string
	Critique     string
	Score        float64
	Pass         bool
	CreatedAt    time.Time
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
  FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_events_run_id ON events(run_id);
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at);
CREATE INDEX IF NOT EXISTS idx_artifacts_run_id ON artifacts(run_id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_run_seq ON tool_calls(run_id,seq);
CREATE INDEX IF NOT EXISTS idx_tool_calls_tool_input_hash ON tool_calls(tool,input_hash);
CREATE INDEX IF NOT EXISTS idx_scores_run_created ON scores(run_id,created_at);
CREATE INDEX IF NOT EXISTS idx_scores_skill_pass ON scores(skill,pass);
CREATE INDEX IF NOT EXISTS idx_judge_runs_run_created ON judge_runs(run_id,created_at);
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
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO judge_runs(run_id,skill,rubric_hash,judge_cfg_hash,artifact_hash,metrics_json,critique,score,pass,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
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
	)
	if err != nil {
		return fmt.Errorf("insert judge_run: %w", err)
	}
	return nil
}
