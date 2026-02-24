package judgeflow

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	skilljudge "github.com/haris/virmux/internal/skill/judge"
	"github.com/haris/virmux/internal/store"
	"github.com/haris/virmux/internal/trace"
)

func TestServiceRunStartedEmitFailureStopsWrites(t *testing.T) {
	t.Parallel()
	inserted := 0
	svc := Service{
		Emit: func(_ context.Context, event string, _ map[string]any) error {
			if event == "skill.judge.started" {
				return errors.New("inject emit failure")
			}
			return nil
		},
		PersistArtifacts: func(context.Context, string, []string) error { return nil },
		InsertScore: func(context.Context, store.Score) error {
			inserted++
			return nil
		},
		InsertJudgeRun: func(context.Context, store.JudgeRun) error {
			inserted++
			return nil
		},
	}
	_, err := svc.Run(context.Background(), Input{RunID: "r1", RunDir: t.TempDir(), Skill: "dd", RubricPath: "rubric.yaml", RubricHash: "h", Rubric: skilljudge.Rubric{}})
	if err == nil || !strings.Contains(err.Error(), "inject emit failure") {
		t.Fatalf("expected emit failure, got %v", err)
	}
	if inserted != 0 {
		t.Fatalf("expected no writes after started emit failure, got %d", inserted)
	}
}

func TestServiceRunUnsupportedModeFailsClosedWithJudgeInvalid(t *testing.T) {
	t.Parallel()
	inserted := 0
	svc := Service{
		Emit:             func(_ context.Context, _ string, _ map[string]any) error { return nil },
		PersistArtifacts: func(context.Context, string, []string) error { return nil },
		InsertScore: func(context.Context, store.Score) error {
			inserted++
			return nil
		},
		InsertJudgeRun: func(context.Context, store.JudgeRun) error {
			inserted++
			return nil
		},
	}
	_, err := svc.Run(context.Background(), Input{Mode: "llm_abs"})
	if err == nil || !strings.Contains(err.Error(), "JUDGE_INVALID") {
		t.Fatalf("expected JUDGE_INVALID, got %v", err)
	}
	if inserted != 0 {
		t.Fatalf("expected no score/judge inserts, got %d", inserted)
	}
}

func TestServiceRunSuccessEmitsPersistsAndInserts(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runDir := filepath.Join(tmp, "runs", "r1")
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
		t.Fatal(err)
	}
	reqRaw := []byte(`{"req":1,"tool":"shell.exec","args":{"cmd":"echo ok"}}` + "\n")
	resRaw := []byte(`{"req":1,"ok":true}` + "\n")
	reqHash := trace.SHA256Hex(bytes.TrimSuffix(reqRaw, []byte{'\n'}))
	resHash := trace.SHA256Hex(bytes.TrimSuffix(resRaw, []byte{'\n'}))
	if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.req.json"), reqRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.res.json"), resRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "skill-run.json"), []byte(`{"budget":{"tool_calls":2}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	traceLine := `{"ts":"2026-02-22T00:00:00Z","run_id":"r1","seq":1,"type":"event","task":"skill:run","event":"run.started","payload":{}}` + "\n" +
		`{"ts":"2026-02-22T00:00:01Z","run_id":"r1","seq":2,"type":"tool","task":"skill:run","event":"vm.tool.result","tool":"shell.exec","args_hash":"` + reqHash + `","output_hash":"` + resHash + `","stdout_ref":"artifacts/1.out","stderr_ref":"artifacts/1.err","exit_code":0,"dur_ms":1,"bytes_in":1,"bytes_out":1,"payload":{"tool_seq":1,"tool":"shell.exec","input_hash":"` + reqHash + `","output_hash":"` + resHash + `","stdout_ref":"artifacts/1.out","stderr_ref":"artifacts/1.err","exit_code":0,"dur_ms":1,"bytes_in":1,"bytes_out":1}}` + "\n"
	if err := os.WriteFile(filepath.Join(runDir, "trace.ndjson"), []byte(traceLine), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "artifacts", "1.out"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "artifacts", "1.err"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	rubricPath := filepath.Join(tmp, "rubric.yaml")
	if err := os.WriteFile(rubricPath, []byte("criteria:\n- {id: format, w: 0.4, must: true}\n- {id: completeness, w: 0.4}\n- {id: actionability, w: 0.2}\npass: 0.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rubric, hash, err := skilljudge.LoadRubric(rubricPath)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(tmp, "runs.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.StartRun(context.Background(), store.Run{
		ID:        "r1",
		Task:      "skill:run",
		Label:     "judgeflow",
		AgentID:   "default",
		ImageSHA:  "img",
		KernelSHA: "k",
		RootfsSHA: "r",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertToolCall(context.Background(), store.ToolCall{
		RunID:      "r1",
		Seq:        1,
		ReqID:      1,
		Tool:       "shell.exec",
		InputHash:  reqHash,
		OutputHash: resHash,
		InputRef:   "toolio/000001.req.json",
		OutputRef:  "toolio/000001.res.json",
		StdoutRef:  "artifacts/1.out",
		StderrRef:  "artifacts/1.err",
		RC:         0,
		DurMS:      1,
		BytesIn:    1,
		BytesOut:   1,
	}); err != nil {
		t.Fatal(err)
	}
	events := []string{}
	scoreRows := 0
	judgeRows := 0
	persisted := 0
	svc := Service{
		Emit: func(_ context.Context, event string, _ map[string]any) error {
			events = append(events, event)
			return nil
		},
		PersistArtifacts: func(_ context.Context, _ string, paths []string) error {
			persisted += len(paths)
			return nil
		},
		InsertScore: func(context.Context, store.Score) error {
			scoreRows++
			return nil
		},
		InsertJudgeRun: func(context.Context, store.JudgeRun) error {
			judgeRows++
			return nil
		},
		Now:     time.Now,
		DBPath:  dbPath,
		RunsDir: filepath.Join(tmp, "runs"),
	}
	out, err := svc.Run(context.Background(), Input{RunID: "r1", RunDir: runDir, RunStatus: "ok", Skill: "dd", RubricPath: rubricPath, RubricHash: hash, Rubric: rubric, ToolCalls: 1})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.ScorePath == "" {
		t.Fatalf("expected score path")
	}
	if scoreRows != 1 || judgeRows != 1 {
		t.Fatalf("expected one score and one judge row, got %d/%d", scoreRows, judgeRows)
	}
	if persisted != 1 {
		t.Fatalf("expected one persisted artifact, got %d", persisted)
	}
	if len(events) != 2 || events[0] != "skill.judge.started" || events[1] != "skill.judge.scored" {
		t.Fatalf("unexpected event order: %v", events)
	}
}

func TestServiceRunReplayMismatchFailsClosedWithoutRubricRuleIDs(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runDir := filepath.Join(tmp, "runs", "r2")
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
		t.Fatal(err)
	}
	reqRaw := []byte(`{"req":1,"tool":"shell.exec","args":{"cmd":"echo ok"}}` + "\n")
	resRaw := []byte(`{"req":1,"ok":true}` + "\n")
	reqHash := trace.SHA256Hex(bytes.TrimSuffix(reqRaw, []byte{'\n'}))
	resHash := trace.SHA256Hex(bytes.TrimSuffix(resRaw, []byte{'\n'}))
	if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.req.json"), reqRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.res.json"), resRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "skill-run.json"), []byte(`{"budget":{"tool_calls":2}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	traceLine := `{"ts":"2026-02-22T00:00:00Z","run_id":"r2","seq":1,"type":"event","task":"skill:run","event":"run.started","payload":{}}` + "\n" +
		`{"ts":"2026-02-22T00:00:01Z","run_id":"r2","seq":2,"type":"tool","task":"skill:run","event":"vm.tool.result","tool":"shell.exec","args_hash":"` + reqHash + `","output_hash":"` + resHash + `","stdout_ref":"artifacts/1.out","stderr_ref":"artifacts/1.err","exit_code":0,"dur_ms":1,"bytes_in":1,"bytes_out":1,"payload":{"tool_seq":1,"tool":"shell.exec","input_hash":"` + reqHash + `","output_hash":"` + resHash + `","stdout_ref":"artifacts/1.out","stderr_ref":"artifacts/1.err","exit_code":0,"dur_ms":1,"bytes_in":1,"bytes_out":1}}` + "\n"
	if err := os.WriteFile(filepath.Join(runDir, "trace.ndjson"), []byte(traceLine), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "artifacts", "1.out"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "artifacts", "1.err"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmp, "runs.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.StartRun(context.Background(), store.Run{
		ID:        "r2",
		Task:      "skill:run",
		Label:     "judgeflow",
		AgentID:   "default",
		ImageSHA:  "img",
		KernelSHA: "k",
		RootfsSHA: "r",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertToolCall(context.Background(), store.ToolCall{
		RunID:      "r2",
		Seq:        1,
		ReqID:      1,
		Tool:       "shell.exec",
		InputHash:  "sha256:wrong", // force replay mismatch
		OutputHash: resHash,
		InputRef:   "toolio/000001.req.json",
		OutputRef:  "toolio/000001.res.json",
		StdoutRef:  "artifacts/1.out",
		StderrRef:  "artifacts/1.err",
	}); err != nil {
		t.Fatal(err)
	}

	svc := Service{
		Now:     time.Now,
		DBPath:  dbPath,
		RunsDir: filepath.Join(tmp, "runs"),
	}
	res, err := svc.Run(context.Background(), Input{
		RunID:      "r2",
		RunDir:     runDir,
		RunStatus:  "ok",
		Skill:      "dd",
		RubricPath: "rubric.yaml",
		RubricHash: "sha256:rubric",
		Rubric: skilljudge.Rubric{
			Criteria: []skilljudge.Criterion{{ID: "format", W: 1, Threshold: 0.5}},
			Pass:     0.5,
		},
		ToolCalls: 1,
		Mode:      "rules",
		ReadOnly:  true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Judge.Pass {
		t.Fatalf("expected fail-closed replay mismatch")
	}
	foundReplay := false
	for _, c := range res.Judge.Criterion {
		if c.ID == "rule_replay" {
			foundReplay = true
			if c.Pass {
				t.Fatalf("expected rule_replay to fail")
			}
		}
	}
	if !foundReplay {
		t.Fatalf("expected rule_replay criterion in output")
	}
}
