package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haris/virmux/internal/agent"
	"github.com/haris/virmux/internal/store"
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

func TestMakeRunnerDetailsIncludesTransportStats(t *testing.T) {
	t.Parallel()
	outcome := vm.Outcome{LostLogs: 2, LostMetrics: 3, GuestReadyMS: 12}
	details := makeRunnerDetails("/tmp/run", outcome, transportStats{Attempts: 4, HandshakeMS: 55})
	if got := details["connect_attempts"]; got != 4 {
		t.Fatalf("expected connect_attempts=4, got %#v", got)
	}
	if got := details["handshake_ms"]; got != int64(55) {
		t.Fatalf("expected handshake_ms=55, got %#v", got)
	}
	if got := details["guest_ready_ms"]; got != int64(12) {
		t.Fatalf("expected guest_ready_ms=12, got %#v", got)
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

func TestCollectArtifactRecordSocketMetadataOnly(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	sockPath := filepath.Join(tmp, "probe.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer ln.Close()

	sha, size, include, err := collectArtifactRecord(sockPath)
	if err != nil {
		t.Fatalf("collect artifact record: %v", err)
	}
	if !include {
		t.Fatalf("expected socket artifact to be included")
	}
	if sha != "meta:socket" {
		t.Fatalf("expected meta:socket sha, got %q", sha)
	}
	if size != 0 {
		t.Fatalf("expected socket artifact size 0, got %d", size)
	}
}

func TestPersistRunArtifactsStoresSocketMetadata(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	runID := "rid"
	if err := st.StartRun(context.Background(), store.Run{
		ID:        runID,
		Task:      "vm:run",
		Label:     "t",
		AgentID:   "A",
		ImageSHA:  "img",
		KernelSHA: "k",
		RootfsSHA: "r",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("start run: %v", err)
	}

	sockPath := filepath.Join(tmp, "art.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer ln.Close()

	if err := persistRunArtifacts(context.Background(), st, runID, []string{sockPath}); err != nil {
		t.Fatalf("persist artifacts: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	row := db.QueryRow(`SELECT sha256, bytes FROM artifacts WHERE run_id=? AND path=?`, runID, sockPath)
	var sha string
	var size int64
	if err := row.Scan(&sha, &size); err != nil {
		t.Fatalf("scan artifact row: %v", err)
	}
	if sha != "meta:socket" {
		t.Fatalf("expected meta:socket sha, got %q", sha)
	}
	if size != 0 {
		t.Fatalf("expected size 0, got %d", size)
	}
}

func TestCollectArtifactRecordSymlinkMetadataOnly(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(tmp, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	sha, size, include, err := collectArtifactRecord(link)
	if err != nil {
		t.Fatalf("collect symlink record: %v", err)
	}
	if !include {
		t.Fatalf("expected symlink artifact included")
	}
	if sha != "meta:symlink" {
		t.Fatalf("expected meta:symlink, got %q", sha)
	}
	if size != 0 {
		t.Fatalf("expected symlink size=0, got %d", size)
	}
}

func Test_runWithStorePrepareRunFilesCreatesAliasAndMeta(t *testing.T) {
	t.Parallel()
	runDir := t.TempDir()
	started := time.Date(2026, 2, 22, 18, 0, 0, 0, time.UTC)
	tracePath, compatPath, metaPath, err := prepareRunFiles(runDir, "rid-1", "vm:smoke", started)
	if err != nil {
		t.Fatalf("prepare run files: %v", err)
	}
	if filepath.Base(tracePath) != tracePrimaryName {
		t.Fatalf("expected primary trace filename %q, got %q", tracePrimaryName, tracePath)
	}
	info, err := os.Lstat(compatPath)
	if err != nil {
		t.Fatalf("lstat compat path: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be symlink", compatPath)
	}
	target, err := os.Readlink(compatPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != tracePrimaryName {
		t.Fatalf("expected symlink target %q, got %q", tracePrimaryName, target)
	}
	metaRaw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta skeleton: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatalf("unmarshal meta skeleton: %v", err)
	}
	if got := meta["trace_path"]; got != tracePrimaryName {
		t.Fatalf("expected trace_path=%q, got %#v", tracePrimaryName, got)
	}
	if got := meta["trace_compat_path"]; got != traceCompatName {
		t.Fatalf("expected trace_compat_path=%q, got %#v", traceCompatName, got)
	}
}
