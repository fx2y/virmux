package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/haris/virmux/internal/store"
	"github.com/haris/virmux/internal/trace"
)

func TestCmdSkillJudgeSanityReadOnlyDoesNotMutateRunDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runsDir, skillsDir, dbPath, dsetPath := seedJudgeSanityFixture(t, tmp, true)
	before := hashDirState(t, filepath.Join(runsDir, "rid-sanity"))
	if err := cmdSkillJudgeSanity([]string{
		"--db", dbPath,
		"--runs-dir", runsDir,
		"--skills-dir", skillsDir,
		"--sanity-dset", dsetPath,
		"--min-samples", "1",
		"--min-agreement", "1",
	}); err != nil {
		t.Fatalf("cmdSkillJudgeSanity: %v", err)
	}
	after := hashDirState(t, filepath.Join(runsDir, "rid-sanity"))
	if before != after {
		t.Fatalf("expected read-only sanity run dir, hash changed before=%s after=%s", before, after)
	}
}

func TestCmdSkillJudgeSanityEnforcesAgreementThreshold(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runsDir, skillsDir, dbPath, dsetPath := seedJudgeSanityFixture(t, tmp, false)
	err := cmdSkillJudgeSanity([]string{
		"--db", dbPath,
		"--runs-dir", runsDir,
		"--skills-dir", skillsDir,
		"--sanity-dset", dsetPath,
		"--min-samples", "1",
		"--min-agreement", "1",
	})
	if err == nil {
		t.Fatalf("expected agreement threshold failure")
	}
}

func seedJudgeSanityFixture(t *testing.T, root string, expectPass bool) (runsDir, skillsDir, dbPath, dsetPath string) {
	t.Helper()
	runsDir = filepath.Join(root, "runs")
	skillsDir = filepath.Join(root, "skills")
	runDir := filepath.Join(runsDir, "rid-sanity")
	if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "dd"), 0o755); err != nil {
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
	if err := os.WriteFile(filepath.Join(runDir, "artifacts", "1.out"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "artifacts", "1.err"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "skill-run.json"), []byte(`{"skill":"dd","tool_calls":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	traceLine := `{"ts":"2026-02-24T00:00:00Z","run_id":"rid-sanity","seq":1,"type":"event","task":"skill:run","event":"run.started","payload":{}}` + "\n" +
		`{"ts":"2026-02-24T00:00:01Z","run_id":"rid-sanity","seq":2,"type":"tool","task":"skill:run","event":"vm.tool.result","tool":"shell.exec","args_hash":"` + reqHash + `","output_hash":"` + resHash + `","stdout_ref":"artifacts/1.out","stderr_ref":"artifacts/1.err","exit_code":0,"dur_ms":1,"bytes_in":1,"bytes_out":1,"payload":{"tool_seq":1,"tool":"shell.exec","input_hash":"` + reqHash + `","output_hash":"` + resHash + `","stdout_ref":"artifacts/1.out","stderr_ref":"artifacts/1.err","exit_code":0,"dur_ms":1,"bytes_in":1,"bytes_out":1}}` + "\n"
	if err := os.WriteFile(filepath.Join(runDir, "trace.ndjson"), []byte(traceLine), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "dd", "rubric.yaml"), []byte("criteria:\n- {id: format, w: 0.4, must: true}\n- {id: completeness, w: 0.4}\n- {id: actionability, w: 0.2}\npass: 0.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath = filepath.Join(runsDir, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.StartRun(context.Background(), store.Run{
		ID:        "rid-sanity",
		Task:      "skill:run",
		Label:     "sanity",
		AgentID:   "default",
		ImageSHA:  "img",
		KernelSHA: "k",
		RootfsSHA: "r",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishRun(context.Background(), "rid-sanity", "ok", 0, 0, filepath.Join(runDir, "trace.ndjson"), "", 0, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertToolCall(context.Background(), store.ToolCall{
		RunID:      "rid-sanity",
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
	}); err != nil {
		t.Fatal(err)
	}

	dsetPath = filepath.Join(root, "judge_sanity.jsonl")
	row := map[string]any{
		"id":     "sanity-1",
		"run_id": "rid-sanity",
		"skill":  "dd",
		"expected": map[string]any{
			"score": 1.0,
			"pass":  expectPass,
		},
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dsetPath, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return runsDir, skillsDir, dbPath, dsetPath
}

func hashDirState(t *testing.T, dir string) string {
	t.Helper()
	var rels []string
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(raw)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
