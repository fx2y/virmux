package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haris/virmux/internal/agent"
	"github.com/haris/virmux/internal/vm"
)

func TestParseVMRunArgsOverridesCommand(t *testing.T) {
	t.Parallel()
	cfg, command, err := parseVMRunArgs("vm-run", []string{"--cmd", "echo hello", "--label", "demo", "--agent", "A", "--timeout-sec", "9", "--mem-mib", "1024"}, "uname -a")
	if err != nil {
		t.Fatalf("parse vm-run args: %v", err)
	}
	if command != "echo hello" {
		t.Fatalf("expected command override, got %q", command)
	}
	if cfg.label != "demo" {
		t.Fatalf("expected label demo, got %q", cfg.label)
	}
	if cfg.timeout != 9*time.Second {
		t.Fatalf("expected timeout 9s, got %s", cfg.timeout)
	}
	if cfg.agentID != "A" {
		t.Fatalf("expected agent A, got %q", cfg.agentID)
	}
	if cfg.memMiB != 1024 {
		t.Fatalf("expected mem_mib 1024, got %d", cfg.memMiB)
	}
}

func TestParseVMRunArgsUsesSmokeDefaultForCompatibility(t *testing.T) {
	t.Parallel()
	cfg, command, err := parseVMRunArgs("vm-smoke", nil, vm.DefaultSmokeCommand())
	if err != nil {
		t.Fatalf("parse vm-smoke args: %v", err)
	}
	if command != vm.DefaultSmokeCommand() {
		t.Fatalf("expected vm-smoke default command, got %q", command)
	}
	if cfg.timeout != 30*time.Second {
		t.Fatalf("expected default timeout 30s, got %s", cfg.timeout)
	}
}

func TestParseVMRunArgsRejectsEmptyCommand(t *testing.T) {
	t.Parallel()
	_, _, err := parseVMRunArgs("vm-run", []string{"--cmd", "   "}, "uname -a")
	if err == nil {
		t.Fatalf("expected empty command error")
	}
	if !strings.Contains(err.Error(), "--cmd cannot be empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunUsageIncludesVMRun(t *testing.T) {
	t.Parallel()
	err := run(nil)
	if err == nil {
		t.Fatalf("expected usage error")
	}
	if !strings.Contains(err.Error(), "vm-run") {
		t.Fatalf("usage should mention vm-run, got %q", err.Error())
	}
}

func TestParseVMRunArgsIncludesVsockCID(t *testing.T) {
	t.Parallel()
	cfg, _, err := parseVMRunArgs("vm-run", []string{"--cmd", "echo ok", "--vsock-cid", "7"}, "uname -a")
	if err != nil {
		t.Fatalf("parse vm-run args: %v", err)
	}
	if cfg.vsockCID != 7 {
		t.Fatalf("expected vsock cid 7, got %d", cfg.vsockCID)
	}
}

func TestResolveResumeSnapshotPathsPrefersAgentSnapshot(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	base := filepath.Join(tmp, "snapshots")
	meta := agent.Meta{LastSnapshotID: "snap-42"}
	got, err := resolveResumeSnapshotPaths(base, meta, "", "")
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	if got.source != "agent_last_snapshot" {
		t.Fatalf("expected agent_last_snapshot source, got %q", got.source)
	}
	if !strings.Contains(got.memPath, "snap-42") || !strings.Contains(got.statePath, "snap-42") {
		t.Fatalf("expected snapshot-id paths, got mem=%q state=%q", got.memPath, got.statePath)
	}
	if got.snapshotID != "snap-42" {
		t.Fatalf("expected snapshotID snap-42, got %q", got.snapshotID)
	}
}

func TestResolveResumeSnapshotPathsFallsBackToLatestJSON(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	base := filepath.Join(tmp, "snapshots")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "latest.json"), []byte(`{"snapshot_id":"snap-json","mem_path":"/m","state_path":"/s"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveResumeSnapshotPaths(base, agent.Meta{}, "", "")
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	if got.source != "latest_json" {
		t.Fatalf("expected latest_json source, got %q", got.source)
	}
	if got.memPath != "/m" || got.statePath != "/s" || got.snapshotID != "snap-json" {
		t.Fatalf("unexpected latest.json mapping: %+v", got)
	}
}

func TestEnsureResumeTerminalPayloadDefaults(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"status": "failed",
	}
	ensureResumeTerminalPayload(payload)
	if payload["resume_mode"] != resumeModeFallback {
		t.Fatalf("expected fallback resume_mode, got %#v", payload["resume_mode"])
	}
	if payload["resume_mode_legacy"] != resumeModeSnapshotLegacy {
		t.Fatalf("expected legacy snapshot mode, got %#v", payload["resume_mode_legacy"])
	}
	if payload["resume_source"] == "" {
		t.Fatalf("expected non-empty resume_source")
	}
	if _, ok := payload["resume_error"]; !ok {
		t.Fatalf("expected resume_error key")
	}
}
