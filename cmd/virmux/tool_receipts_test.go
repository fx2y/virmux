package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	trpc "github.com/haris/virmux/internal/transport/rpc"
)

func TestBuildToolResultPayloadWritesToolIOArtifacts(t *testing.T) {
	t.Parallel()
	runDir := t.TempDir()
	req := trpc.Request{ReqID: 7, Tool: "shell.exec", Args: map[string]any{"cmd": "echo hi"}}
	res := trpc.Response{ReqID: 7, OK: true, RC: 0, StdoutRef: "artifacts/7.out", StderrRef: "artifacts/7.err", DurMS: 12}
	payload, err := buildToolResultPayload(runDir, req, res)
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	for _, key := range []string{"input_hash", "output_hash", "input_ref", "output_ref", "receipt_ref", "tool_seq"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("missing payload key %s", key)
		}
	}
	reqPath := filepath.Join(runDir, payload["input_ref"].(string))
	if _, err := os.Stat(reqPath); err != nil {
		t.Fatalf("missing req artifact: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(runDir, payload["receipt_ref"].(string)))
	if err != nil {
		t.Fatalf("read receipt artifact: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("unmarshal receipt: %v", err)
	}
	if rec["input_hash"] != payload["input_hash"] {
		t.Fatalf("receipt/input hash mismatch")
	}
}
