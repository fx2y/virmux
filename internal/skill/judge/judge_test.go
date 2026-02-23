package judge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRubricRejectsMissingDefaultCriterion(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rubric.yaml")
	if err := os.WriteFile(path, []byte("criteria:\n- {id: format, w: 0.5}\n- {id: completeness, w: 0.5}\npass: 0.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadRubric(path)
	if err == nil || !strings.Contains(err.Error(), "actionability") {
		t.Fatalf("expected default criterion rejection, got %v", err)
	}
}

func TestEvaluateEmptyArtifactFails(t *testing.T) {
	t.Parallel()
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "trace.ndjson"), []byte(`{"ts":"2026-02-22T00:00:00Z","run_id":"r1","seq":1,"type":"event","task":"skill:run","event":"run.finished","payload":{"status":"ok"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rubric.yaml")
	if err := os.WriteFile(path, []byte("criteria:\n- {id: format, w: 0.4, must: true}\n- {id: completeness, w: 0.4}\n- {id: actionability, w: 0.2}\npass: 0.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, rh, err := LoadRubric(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Evaluate(r, rh, Evidence{RunID: "r1", Skill: "dd", RunDir: runDir, RunStatus: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Fatalf("expected empty artifact to fail, got %+v", res)
	}
}

func TestEvaluateDeterministicForSameInput(t *testing.T) {
	t.Parallel()
	runDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "trace.ndjson"), []byte(`{"ts":"2026-02-22T00:00:00Z","run_id":"r1","seq":1,"type":"event","task":"skill:run","event":"run.finished","payload":{"status":"ok"}}`+"\n"+`{"ts":"2026-02-22T00:00:01Z","run_id":"r1","seq":2,"type":"tool","task":"skill:run","event":"vm.tool.result","tool":"shell.exec","args_hash":"sha256:x","stdout_ref":"artifacts/1.out","stderr_ref":"artifacts/1.err","exit_code":0,"dur_ms":1,"bytes_in":1,"bytes_out":1,"payload":{"tool":"shell.exec","input_hash":"sha256:x","output_hash":"sha256:y","stdout_ref":"artifacts/1.out","stderr_ref":"artifacts/1.err","exit_code":0,"dur_ms":1,"bytes_in":1,"bytes_out":1}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "artifacts", "1.out"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "toolio", "000001.req.json"), []byte("{\"req\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rubric.yaml")
	if err := os.WriteFile(path, []byte("criteria:\n- {id: format, w: 0.4, must: true}\n- {id: completeness, w: 0.4}\n- {id: actionability, w: 0.2}\npass: 0.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, rh, err := LoadRubric(path)
	if err != nil {
		t.Fatal(err)
	}
	ev := Evidence{RunID: "r1", Skill: "dd", RunDir: runDir, RunStatus: "ok"}
	a, err := Evaluate(r, rh, ev)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Evaluate(r, rh, ev)
	if err != nil {
		t.Fatal(err)
	}
	if a.Score != b.Score || a.Pass != b.Pass || a.JudgeCfgHash != b.JudgeCfgHash || a.ArtifactHash != b.ArtifactHash {
		t.Fatalf("expected deterministic result, a=%+v b=%+v", a, b)
	}
}
