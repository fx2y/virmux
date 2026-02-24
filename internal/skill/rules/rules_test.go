package rules

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haris/virmux/internal/skill/judge"
	"github.com/haris/virmux/internal/store"
	"github.com/haris/virmux/internal/trace"
)

func TestRuleEngine(t *testing.T) {
	tmp, err := os.MkdirTemp("", "rules-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dbPath := filepath.Join(tmp, "virmux.sqlite")

	e := &Engine{
		DBPath:  dbPath,
		RunsDir: tmp,
	}

	// Create a fake skill-run.json
	runDir := filepath.Join(tmp, "run-1")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "toolio"), 0755); err != nil {
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
	traceLine := `{"ts":"2026-02-24T00:00:00Z","run_id":"run-1","seq":1,"type":"tool","task":"skill:run","event":"vm.tool.result","tool":"shell.exec","args_hash":"` + reqHash + `","output_hash":"` + resHash + `","payload":{"tool_seq":1,"tool":"shell.exec","input_hash":"` + reqHash + `","output_hash":"` + resHash + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(runDir, "trace.ndjson"), []byte(traceLine), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"budget":{"tool_calls":5}}`
	if err := os.WriteFile(filepath.Join(runDir, "skill-run.json"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.StartRun(context.Background(), store.Run{
		ID:        "run-1",
		Task:      "skill:run",
		Label:     "rules",
		AgentID:   "default",
		ImageSHA:  "img",
		KernelSHA: "k",
		RootfsSHA: "r",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertToolCall(context.Background(), store.ToolCall{
		RunID:      "run-1",
		Seq:        1,
		ReqID:      1,
		Tool:       "shell.exec",
		InputHash:  reqHash,
		OutputHash: resHash,
		InputRef:   "toolio/000001.req.json",
		OutputRef:  "toolio/000001.res.json",
	}); err != nil {
		t.Fatal(err)
	}

	ev := judge.Evidence{
		RunID:     "run-1",
		RunDir:    runDir,
		ToolCalls: 3,
	}

	results, err := e.Evaluate(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}

	foundBudget := false
	for _, r := range results {
		if r.ID == "rule_budget_tool_calls" {
			foundBudget = true
			if !r.Pass {
				t.Errorf("expected budget rule to pass, got fail")
			}
		}
	}
	if !foundBudget {
		t.Errorf("rule_budget_tool_calls not found in results")
	}
}
