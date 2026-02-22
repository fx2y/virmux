package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/haris/virmux/internal/agent"
	"github.com/haris/virmux/internal/slack"
	"github.com/haris/virmux/internal/store"
	"github.com/haris/virmux/internal/trace"
	"github.com/haris/virmux/internal/vm"
)

type runCommon struct {
	imagesLock string
	runsDir    string
	dbPath     string
	label      string
	agentID    string
	memMiB     int64
	vsockCID   int64
	timeout    time.Duration
}

type runRuntime struct {
	now   func() time.Time
	runID func(task string, started time.Time) string
}

const (
	tracePrimaryName = "trace.ndjson"
	traceCompatName  = "trace.jsonl"
	runMetaName      = "meta.json"
)

var defaultRunRuntime = runRuntime{
	now: func() time.Time { return time.Now().UTC() },
	runID: func(task string, started time.Time) string {
		return fmt.Sprintf("%d-%s", started.UnixNano(), stringsForTask(task))
	},
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: virmux <vm-run|vm-smoke|vm-zygote|vm-resume|slack-server>")
	}
	switch args[0] {
	case "vm-run":
		return cmdVMRun(args[1:])
	case "vm-smoke":
		return cmdVMSmoke(args[1:])
	case "vm-zygote":
		return cmdVMZygote(args[1:])
	case "vm-resume":
		return cmdVMResume(args[1:])
	case "slack-server":
		return cmdSlackServer(args[1:])
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func commonFlags(name string) (*flag.FlagSet, *runCommon, *int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	cfg := &runCommon{}
	fs.StringVar(&cfg.imagesLock, "images-lock", "vm/images.lock", "path to vm/images.lock")
	fs.StringVar(&cfg.runsDir, "runs-dir", "runs", "run output directory")
	fs.StringVar(&cfg.dbPath, "db", "runs/virmux.sqlite", "sqlite db path")
	fs.StringVar(&cfg.label, "label", "", "run label")
	fs.StringVar(&cfg.agentID, "agent", "default", "agent id")
	timeoutSec := fs.Int("timeout-sec", 30, "vm timeout in seconds")
	fs.Int64Var(&cfg.memMiB, "mem-mib", 512, "microVM memory MiB")
	fs.Int64Var(&cfg.vsockCID, "vsock-cid", 0, "experimental vsock CID for vm-run (0 disables)")
	return fs, cfg, timeoutSec
}

func parseVMRunArgs(name string, args []string, defaultCmd string) (*runCommon, string, error) {
	fs, cfg, timeoutSec := commonFlags(name)
	cmd := fs.String("cmd", defaultCmd, "command(s) to run in guest over ttyS0")
	if err := fs.Parse(args); err != nil {
		return nil, "", err
	}
	cfg.timeout = time.Duration(*timeoutSec) * time.Second
	command := strings.TrimSpace(*cmd)
	if command == "" {
		return nil, "", errors.New("--cmd cannot be empty")
	}
	return cfg, command, nil
}

func newVMRunRunner(cfg *runCommon, command string, requiredMarkers []string) vmRunner {
	return func(ctx context.Context, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
		var emitErr error
		vsockPath := ""
		if cfg.vsockCID > 0 {
			vsockPath = filepath.Join(runDir, fmt.Sprintf("vsock-%d.sock", cfg.vsockCID))
		}
		outcome, err := vm.Run(ctx, art, runDir, vm.RunConfig{
			MemMiB:          cfg.memMiB,
			Timeout:         cfg.timeout,
			Command:         command,
			RequiredMarkers: requiredMarkers,
			DataVolumePath:  meta.VolumePath,
			ChunkEventLimit: 8,
			ChunkBytes:      512,
			VsockCID:        cfg.vsockCID,
			VsockPath:       vsockPath,
			EventHook: func(evt vm.Event) {
				if emitErr != nil {
					return
				}
				emitErr = emitVM(evt.Kind, evt.Payload)
			},
		})
		if emitErr != nil {
			return vm.Outcome{}, nil, emitErr
		}
		details := map[string]any{
			"serial_log":     filepath.Join(runDir, "serial.log"),
			"fc_log":         filepath.Join(runDir, "fc.log"),
			"fc_metrics_log": filepath.Join(runDir, "fc.metrics.log"),
			"lost_logs":      outcome.LostLogs,
			"lost_metrics":   outcome.LostMetrics,
			"guest_ready_ms": outcome.GuestReadyMS,
		}
		if vsockPath != "" {
			details["vsock_uds_path"] = vsockPath
		}
		return outcome, details, err
	}
}

const (
	resumeModeSnapshot       = "snapshot_resume"
	resumeModeFallback       = "fallback_cold_boot"
	resumeModeSnapshotLegacy = "snapshot"
)

type resumeLookup struct {
	memPath    string
	statePath  string
	source     string
	snapshotID string
}

func resolveResumeSnapshotPaths(base string, meta agent.Meta, inMemPath, inStatePath string) (resumeLookup, error) {
	out := resumeLookup{
		memPath:   inMemPath,
		statePath: inStatePath,
		source:    "explicit_flags",
	}
	if out.memPath != "" && out.statePath != "" {
		return out, nil
	}
	if meta.LastSnapshotID != "" {
		if out.memPath == "" {
			out.memPath = filepath.Join(base, meta.LastSnapshotID, "vm.mem")
		}
		if out.statePath == "" {
			out.statePath = filepath.Join(base, meta.LastSnapshotID, "vm.state")
		}
		if out.memPath != "" && out.statePath != "" {
			out.source = "agent_last_snapshot"
			out.snapshotID = meta.LastSnapshotID
			return out, nil
		}
	}
	data, err := os.ReadFile(filepath.Join(base, "latest.json"))
	if err != nil {
		return resumeLookup{}, fmt.Errorf("read latest snapshot metadata: %w", err)
	}
	var latest struct {
		SnapshotID string `json:"snapshot_id"`
		MemPath    string `json:"mem_path"`
		StatePath  string `json:"state_path"`
	}
	if err := json.Unmarshal(data, &latest); err != nil {
		return resumeLookup{}, fmt.Errorf("parse latest snapshot metadata: %w", err)
	}
	if out.memPath == "" {
		out.memPath = latest.MemPath
	}
	if out.statePath == "" {
		out.statePath = latest.StatePath
	}
	out.source = "latest_json"
	out.snapshotID = latest.SnapshotID
	return out, nil
}

func cmdVMRun(args []string) error {
	cfg, command, err := parseVMRunArgs("vm-run", args, "uname -a")
	if err != nil {
		return err
	}
	return runWithStore(
		cfg,
		"vm:run",
		map[string]any{"label": cfg.label, "cmd": command},
		newVMRunRunner(cfg, command, nil),
		defaultRunRuntime,
	)
}

func cmdVMSmoke(args []string) error {
	cfg, command, err := parseVMRunArgs("vm-smoke", args, vm.DefaultSmokeCommand())
	if err != nil {
		return err
	}
	return runWithStore(
		cfg,
		"vm:smoke",
		map[string]any{"label": cfg.label, "cmd": command},
		newVMRunRunner(cfg, command, []string{"Linux", "ok"}),
		defaultRunRuntime,
	)
}

func cmdVMZygote(args []string) error {
	fs, cfg, timeoutSec := commonFlags("vm-zygote")
	snapshotDir := fs.String("snapshot-dir", "", "snapshot directory (default vm/snapshots/<image_sha>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg.timeout = time.Duration(*timeoutSec) * time.Second

	return runWithStore(cfg, "vm:zygote", map[string]any{"label": cfg.label}, func(ctx context.Context, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
		base := *snapshotDir
		if base == "" {
			repoRoot := filepath.Dir(filepath.Dir(cfg.imagesLock))
			base = filepath.Join(repoRoot, "vm", "snapshots", art.ImageSHA, cfg.agentID)
		}
		snapshotID := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
		snapshotPath := filepath.Join(base, snapshotID)
		outcome, memPath, statePath, err := vm.ZygoteWithHook(ctx, art, runDir, snapshotPath, cfg.memMiB, cfg.timeout, meta.VolumePath, func(evt vm.Event) {
			_ = emitVM(evt.Kind, evt.Payload)
		})
		if err != nil {
			return outcome, nil, err
		}
		latest := map[string]any{
			"snapshot_id": snapshotID,
			"mem_path":    memPath,
			"state_path":  statePath,
			"image_sha":   art.ImageSHA,
			"agent_id":    cfg.agentID,
			"updated_at":  time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := os.MkdirAll(base, 0o755); err != nil {
			return outcome, nil, err
		}
		data, _ := json.MarshalIndent(latest, "", "  ")
		if err := os.WriteFile(filepath.Join(base, "latest.json"), data, 0o644); err != nil {
			return outcome, nil, err
		}
		meta.LastSnapshotID = snapshotID
		repoRoot := filepath.Dir(filepath.Dir(cfg.imagesLock))
		agentStore := agent.NewStore(filepath.Join(repoRoot, "agents"), filepath.Join(repoRoot, "volumes"))
		if err := agentStore.Save(meta); err != nil {
			return outcome, nil, err
		}
		return outcome, map[string]any{"snapshot_dir": base, "snapshot_id": snapshotID, "mem_path": memPath, "state_path": statePath}, nil
	}, defaultRunRuntime)
}

func cmdVMResume(args []string) error {
	fs, cfg, timeoutSec := commonFlags("vm-resume")
	snapshotDir := fs.String("snapshot-dir", "", "snapshot directory (default vm/snapshots/<image_sha>)")
	memPath := fs.String("mem-path", "", "override mem snapshot path")
	statePath := fs.String("state-path", "", "override state snapshot path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg.timeout = time.Duration(*timeoutSec) * time.Second

	return runWithStore(cfg, "vm:resume", map[string]any{"label": cfg.label}, func(ctx context.Context, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
		base := *snapshotDir
		if base == "" {
			repoRoot := filepath.Dir(filepath.Dir(cfg.imagesLock))
			base = filepath.Join(repoRoot, "vm", "snapshots", art.ImageSHA, cfg.agentID)
		}
		runFallback := func(reason error, source string) (vm.Outcome, map[string]any, error) {
			details := map[string]any{
				"resume_mode":        resumeModeFallback,
				"resume_mode_legacy": resumeModeSnapshotLegacy,
				"resume_source":      source,
				"resume_error":       reason.Error(),
			}
			fallbackDir := filepath.Join(runDir, "fallback-coldboot")
			fallback, fbErr := vm.SmokeWithHook(ctx, art, fallbackDir, cfg.memMiB, cfg.timeout, meta.VolumePath, func(evt vm.Event) {
				_ = emitVM(evt.Kind, evt.Payload)
			})
			if fbErr != nil {
				return vm.Outcome{}, details, fmt.Errorf("snapshot resume failed (%v); fallback smoke failed (%v)", reason, fbErr)
			}
			fallback.ResumeMS = fallback.BootMS
			fallback.BootMS = 0
			details["fallback_trace"] = filepath.Join(fallbackDir, "serial.log")
			return fallback, details, nil
		}
		resume, err := resolveResumeSnapshotPaths(base, meta, *memPath, *statePath)
		if err != nil {
			return runFallback(err, "snapshot_lookup_error")
		}
		details := map[string]any{
			"mem_path":           resume.memPath,
			"state_path":         resume.statePath,
			"resume_source":      resume.source,
			"resume_mode_legacy": resumeModeSnapshotLegacy,
			"resume_error":       "",
		}
		if resume.snapshotID != "" {
			details["snapshot_id"] = resume.snapshotID
		}
		outcome, err := vm.ResumeWithHook(ctx, art, runDir, resume.memPath, resume.statePath, cfg.memMiB, cfg.timeout, meta.VolumePath, func(evt vm.Event) {
			_ = emitVM(evt.Kind, evt.Payload)
		})
		if err == nil {
			details["resume_mode"] = resumeModeSnapshot
			return outcome, details, nil
		}
		return runFallback(err, "snapshot_resume_error")
	}, defaultRunRuntime)
}

type vmEventEmitter func(event string, payload map[string]any) error
type vmRunner func(ctx context.Context, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error)

func prepareRunFiles(runDir, runID, task string, started time.Time) (string, string, string, error) {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", "", "", err
	}
	tracePath := filepath.Join(runDir, tracePrimaryName)
	traceCompatPath := filepath.Join(runDir, traceCompatName)
	if err := os.Remove(traceCompatPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", "", fmt.Errorf("remove stale trace alias: %w", err)
	}
	if err := os.Symlink(tracePrimaryName, traceCompatPath); err != nil {
		return "", "", "", fmt.Errorf("create trace alias %s -> %s: %w", traceCompatPath, tracePrimaryName, err)
	}
	metaPath := filepath.Join(runDir, runMetaName)
	metaSkeleton := map[string]any{
		"run_id":            runID,
		"task":              task,
		"status":            "running",
		"started_at":        started.Format(time.RFC3339Nano),
		"trace_path":        tracePrimaryName,
		"trace_compat_path": traceCompatName,
	}
	metaBytes, err := json.MarshalIndent(metaSkeleton, "", "  ")
	if err != nil {
		return "", "", "", fmt.Errorf("marshal meta skeleton: %w", err)
	}
	if err := os.WriteFile(metaPath, append(metaBytes, '\n'), 0o644); err != nil {
		return "", "", "", fmt.Errorf("write meta skeleton: %w", err)
	}
	return tracePath, traceCompatPath, metaPath, nil
}

func runWithStore(cfg *runCommon, task string, startedPayload map[string]any, runner vmRunner, runtime runRuntime) error {
	if runtime.now == nil {
		runtime.now = func() time.Time { return time.Now().UTC() }
	}
	if runtime.runID == nil {
		runtime.runID = func(task string, started time.Time) string {
			return fmt.Sprintf("%d-%s", started.UnixNano(), stringsForTask(task))
		}
	}
	ctx := context.Background()
	art, err := vm.LoadArtifacts(cfg.imagesLock)
	if err != nil {
		return err
	}
	repoRoot := filepath.Dir(filepath.Dir(cfg.imagesLock))
	agentStore := agent.NewStore(filepath.Join(repoRoot, "agents"), filepath.Join(repoRoot, "volumes"))
	meta, err := agentStore.Ensure(cfg.agentID)
	if err != nil {
		return err
	}
	if err := vm.EnsureExt4Volume(meta.VolumePath, 128); err != nil {
		return err
	}
	st, err := store.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	started := runtime.now().UTC()
	runID := runtime.runID(task, started)
	runDir := filepath.Join(cfg.runsDir, runID)
	tracePath, traceCompatPath, metaPath, err := prepareRunFiles(runDir, runID, task, started)
	if err != nil {
		return err
	}

	tw, err := trace.NewWriter(tracePath)
	if err != nil {
		return err
	}
	defer tw.Close()

	if err := st.StartRun(ctx, store.Run{
		ID:        runID,
		Task:      task,
		Label:     cfg.label,
		AgentID:   cfg.agentID,
		ImageSHA:  art.ImageSHA,
		KernelSHA: art.KernelSHA,
		RootfsSHA: art.RootfsSHA,
		StartedAt: started,
	}); err != nil {
		return err
	}
	if startedPayload == nil {
		startedPayload = map[string]any{}
	}
	if _, ok := startedPayload["label"]; !ok {
		startedPayload["label"] = cfg.label
	}
	startedPayload["agent_id"] = cfg.agentID
	if err := emit(ctx, st, tw, runID, task, "run.started", startedPayload, runtime.now); err != nil {
		_ = st.FinishRun(ctx, runID, "failed", 0, 0, tracePath, "", 0, runtime.now().UTC())
		return err
	}

	outcome, details, runErr := runner(ctx, art, meta, runDir, func(event string, payload map[string]any) error {
		return emit(ctx, st, tw, runID, task, event, payload, runtime.now)
	})
	status := "ok"
	if runErr != nil {
		status = "failed"
		if details == nil {
			details = map[string]any{}
		}
		details["error"] = runErr.Error()
	}
	payload := map[string]any{
		"status":    status,
		"boot_ms":   outcome.BootMS,
		"resume_ms": outcome.ResumeMS,
	}
	for k, v := range details {
		payload[k] = v
	}
	if task == "vm:resume" {
		ensureResumeTerminalPayload(payload)
	}
	finishEmitErr := emit(ctx, st, tw, runID, task, "run.finished", payload, runtime.now)
	snapshotID := stringDetail(payload, "snapshot_id")
	costEst := estimateCost(outcome.BootMS, outcome.ResumeMS, cfg.memMiB)
	finishErr := st.FinishRun(ctx, runID, status, outcome.BootMS, outcome.ResumeMS, tracePath, snapshotID, costEst, runtime.now().UTC())
	artifactErr := persistRunArtifacts(ctx, st, runID, []string{
		filepath.Join(runDir, "serial.log"),
		filepath.Join(runDir, "firecracker.stderr.log"),
		filepath.Join(runDir, "fc.log"),
		filepath.Join(runDir, "fc.metrics.log"),
		tracePath,
		traceCompatPath,
		metaPath,
		stringDetail(payload, "mem_path"),
		stringDetail(payload, "state_path"),
		stringDetail(payload, "fallback_trace"),
		stringDetail(payload, "vsock_uds_path"),
	})

	summary, _ := json.Marshal(map[string]any{
		"run_id":    runID,
		"task":      task,
		"status":    status,
		"boot_ms":   outcome.BootMS,
		"resume_ms": outcome.ResumeMS,
		"trace":     tracePath,
		"run_dir":   runDir,
	})
	fmt.Println(string(summary))

	if finishEmitErr != nil || finishErr != nil || artifactErr != nil {
		return errors.Join(finishEmitErr, finishErr, artifactErr)
	}
	if runErr != nil {
		return runErr
	}
	return nil
}

func ensureResumeTerminalPayload(payload map[string]any) {
	if _, ok := payload["resume_mode"]; !ok {
		payload["resume_mode"] = resumeModeFallback
	}
	if _, ok := payload["resume_mode_legacy"]; !ok {
		payload["resume_mode_legacy"] = resumeModeSnapshotLegacy
	}
	if _, ok := payload["resume_source"]; !ok {
		payload["resume_source"] = "unknown"
	}
	if _, ok := payload["resume_error"]; !ok {
		payload["resume_error"] = ""
	}
}

func estimateCost(bootMS, resumeMS, memMiB int64) float64 {
	totalSeconds := float64(bootMS+resumeMS) / 1000.0
	memGiB := float64(memMiB) / 1024.0
	return totalSeconds * memGiB
}

func stringDetail(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func persistRunArtifacts(ctx context.Context, st *store.Store, runID string, paths []string) error {
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		sum, size, include, err := collectArtifactRecord(p)
		if err != nil {
			return err
		}
		if !include {
			continue
		}
		if err := st.InsertArtifact(ctx, runID, p, sum, size); err != nil {
			return err
		}
	}
	return nil
}

func collectArtifactRecord(path string) (sha string, size int64, include bool, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", 0, false, nil
		}
		return "", 0, false, fmt.Errorf("stat artifact %s: %w", path, err)
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return "meta:symlink", 0, true, nil
	}
	if mode.IsRegular() {
		sum, hashErr := fileSHA256(path)
		if hashErr != nil {
			return "", 0, false, fmt.Errorf("hash artifact %s: %w", path, hashErr)
		}
		return sum, info.Size(), true, nil
	}
	switch {
	case mode&os.ModeSocket != 0:
		return "meta:socket", 0, true, nil
	case mode&os.ModeNamedPipe != 0:
		return "meta:fifo", 0, true, nil
	case mode.IsDir():
		return "meta:dir", 0, true, nil
	default:
		return fmt.Sprintf("meta:mode:%#o", uint32(mode.Type())), 0, true, nil
	}
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func emit(ctx context.Context, st *store.Store, tw *trace.Writer, runID, task, event string, payload map[string]any, now func() time.Time) error {
	if err := tw.Emit(runID, task, event, payload); err != nil {
		return err
	}
	data, _ := json.Marshal(payload)
	if err := st.InsertEvent(ctx, runID, event, string(data), now().UTC()); err != nil {
		return err
	}
	return nil
}

func stringsForTask(task string) string {
	clean := make([]byte, 0, len(task))
	for i := 0; i < len(task); i++ {
		b := task[i]
		if (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') {
			clean = append(clean, b)
		}
	}
	if len(clean) == 0 {
		return "run"
	}
	return string(clean)
}

func cmdSlackServer(args []string) error {
	fs := flag.NewFlagSet("slack-server", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:18080", "listen address")
	dbPath := fs.String("db", "runs/virmux.sqlite", "sqlite db path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	mux := slack.NewMux(st)
	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-sigCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
