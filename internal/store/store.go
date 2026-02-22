package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Run struct {
	ID        string
	Task      string
	Label     string
	ImageSHA  string
	StartedAt time.Time
}

type Store struct {
	db *sql.DB
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
  image_sha TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'running',
  started_at TEXT NOT NULL,
  ended_at TEXT,
  boot_ms INTEGER NOT NULL DEFAULT 0,
  resume_ms INTEGER NOT NULL DEFAULT 0,
  trace_path TEXT NOT NULL DEFAULT ''
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
CREATE INDEX IF NOT EXISTS idx_events_run_id ON events(run_id);
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at);
`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}
	return nil
}

func (s *Store) StartRun(ctx context.Context, run Run) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO runs(id,task,label,image_sha,status,started_at) VALUES(?,?,?,?,?,?)`,
		run.ID,
		run.Task,
		run.Label,
		run.ImageSHA,
		"running",
		run.StartedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	return nil
}

func (s *Store) FinishRun(ctx context.Context, runID, status string, bootMS, resumeMS int64, tracePath string, endedAt time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE runs SET status=?, ended_at=?, boot_ms=?, resume_ms=?, trace_path=? WHERE id=?`,
		status,
		endedAt.UTC().Format(time.RFC3339Nano),
		bootMS,
		resumeMS,
		tracePath,
		runID,
	)
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
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
