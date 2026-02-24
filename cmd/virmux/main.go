package main

import (
	"bufio"
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
	trpc "github.com/haris/virmux/internal/transport/rpc"
	tvsock "github.com/haris/virmux/internal/transport/vsock"
	"github.com/haris/virmux/internal/vm"
)

type runCommon struct {
	imagesLock   string
	runsDir      string
	dbPath       string
	label        string
	agentID      string
	memMiB       int64
	vsockCID     int64
	timeout      time.Duration
	tool         string
	allowCSV     string
	toolArgsJSON string
	exportOnFail bool
}

type runRuntime struct {
	now   func() time.Time
	runID func(task string, started time.Time) string
}

type transportStats struct {
	Attempts    int   `json:"attempts"`
	HandshakeMS int64 `json:"handshake_ms"`
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

var exportRunBundleFunc = exportRunBundle

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: virmux <vm-run|vm-smoke|vm-zygote|vm-resume|skill|export|import|slack-server>")
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
	case "skill":
		return cmdSkill(args[1:])
	case "export":
		return cmdExport(args[1:])
	case "import":
		return cmdImport(args[1:])
	case "research":
		return cmdResearch(args[1:])
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
	fs.BoolVar(&cfg.exportOnFail, "export-on-fail", true, "auto-export partial run bundle on failed runs")
	return fs, cfg, timeoutSec
}

func parseVMRunArgs(name string, args []string, defaultCmd string) (*runCommon, string, error) {
	fs, cfg, timeoutSec := commonFlags(name)
	cmd := fs.String("cmd", defaultCmd, "command(s) to run in guest over ttyS0")
	fs.StringVar(&cfg.tool, "tool", "shell.exec", "tool to execute when vsock agentd path is enabled")
	fs.StringVar(&cfg.allowCSV, "allow", "shell.exec,fs.read,fs.write,http.fetch", "comma-separated allowlist for agentd request")
	fs.StringVar(&cfg.toolArgsJSON, "tool-args-json", "", "raw JSON object for agentd tool args (overrides --cmd mapping)")
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
		trStats := transportStats{}
		toolSucceeded := false
		vsockPath := ""
		if cfg.vsockCID > 0 {
			vsockPath = filepath.Join(runDir, fmt.Sprintf("vsock-%d.sock", cfg.vsockCID))
		}
		vmCommand := command
		if cfg.vsockCID > 0 {
			vmCommand = "export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin; mkdir -p /data; mount --bind /dev/virmux-data /data 2>/dev/null || true; /usr/local/bin/virmux-agentd --port 10001"
		}
		outcome, err := vm.Run(ctx, art, runDir, vm.RunConfig{
			MemMiB:          cfg.memMiB,
			Timeout:         cfg.timeout,
			Command:         vmCommand,
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
			AfterInject: func(ctx context.Context) error {
				if cfg.vsockCID <= 0 {
					return nil
				}
				toolCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
				defer cancel()
				stats, err := runAgentdTool(toolCtx, vsockPath, cfg, command, runDir, emitVM)
				trStats = stats
				if err == nil {
					toolSucceeded = true
				}
				return err
			},
		})
		if emitErr != nil {
			return vm.Outcome{}, nil, emitErr
		}
		details := makeRunnerDetails(runDir, outcome, trStats)
		if vsockPath != "" {
			details["vsock_uds_path"] = vsockPath
		}
		if cfg.vsockCID > 0 && toolSucceeded && isBridgeCommandExitError(err) {
			err = nil
		}
		return outcome, details, err
	}
}

func isBridgeCommandExitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "vm command exit rc=1")
}

func runAgentdTool(ctx context.Context, vsockPath string, cfg *runCommon, command, runDir string, emitVM vmEventEmitter) (transportStats, error) {
	started := time.Now()
	dialRes, err := tvsock.DialWithRetry(ctx, vsockPath, 10001, tvsock.DefaultRetryPolicy())
	if err != nil {
		return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, err
	}
	defer dialRes.Conn.Close()
	reader := bufio.NewReader(dialRes.Conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, err
	}
	caps, err := parseReadyBanner(line)
	if err != nil {
		return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, err
	}
	if err := emitVM("vm.agent.ready", map[string]any{"latency_ms": time.Since(started).Milliseconds(), "method": "vsock_ready_banner", "caps": caps}); err != nil {
		return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, err
	}
	rw := struct {
		io.Reader
		io.Writer
	}{Reader: reader, Writer: dialRes.Conn}
	client := trpc.NewClient(rw)
	defer client.Close()
	allow := splitCSV(cfg.allowCSV)
	req := trpc.Request{ReqID: 1, Tool: cfg.tool, Allow: allow}
	if strings.TrimSpace(cfg.toolArgsJSON) != "" {
		var raw map[string]any
		if err := json.Unmarshal([]byte(cfg.toolArgsJSON), &raw); err != nil {
			return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, fmt.Errorf("parse --tool-args-json: %w", err)
		}
		req.Args = raw
	} else {
		switch cfg.tool {
		case "shell.exec":
			req.Args = map[string]any{"cmd": command, "cwd": "/dev/virmux-data", "timeout_ms": int(cfg.timeout / time.Millisecond)}
		default:
			return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, fmt.Errorf("unsupported --tool in vm-run bridge without --tool-args-json: %s", cfg.tool)
		}
	}
	res, err := client.Call(ctx, req)
	if err != nil {
		return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, err
	}
	if err := hydrateLegacyToolStreams(ctx, client, req, &res); err != nil {
		return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, err
	}
	if err := materializeToolRefs(runDir, &res); err != nil {
		return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, err
	}
	payload, err := buildToolResultPayload(runDir, req, res)
	if err != nil {
		return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, err
	}
	payload["connect_attempts"] = dialRes.Stats.Attempts
	payload["handshake_ms"] = dialRes.Stats.HandshakeMS
	if err := emitVM("vm.tool.result", payload); err != nil {
		return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, err
	}
	if err := bridgeToolResultError(req.Tool, res); err != nil {
		return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, err
	}
	return transportStats{Attempts: dialRes.Stats.Attempts, HandshakeMS: dialRes.Stats.HandshakeMS}, nil
}

func bridgeToolResultError(tool string, res trpc.Response) error {
	if res.OK {
		return nil
	}
	code := "INTERNAL"
	msg := "tool returned ok=false"
	if res.Error != nil {
		if v, _ := res.Error["code"].(string); strings.TrimSpace(v) != "" {
			code = strings.TrimSpace(v)
		}
		if v, _ := res.Error["msg"].(string); strings.TrimSpace(v) != "" {
			msg = strings.TrimSpace(v)
		}
	}
	return fmt.Errorf("tool %s failed code=%s msg=%s", tool, code, msg)
}

func hydrateLegacyToolStreams(ctx context.Context, client *trpc.Client, req trpc.Request, res *trpc.Response) error {
	if client == nil || res == nil || req.Tool != "shell.exec" {
		return nil
	}
	if hasStringData(res.Data, "stdout") && hasStringData(res.Data, "stderr") {
		return nil
	}
	allowSet := map[string]struct{}{}
	for _, a := range req.Allow {
		allowSet[a] = struct{}{}
	}
	if _, ok := allowSet["fs.read"]; !ok {
		return nil
	}
	if res.Data == nil {
		res.Data = map[string]any{}
	}
	if !hasStringData(res.Data, "stdout") && strings.TrimSpace(res.StdoutRef) != "" {
		b, err := readToolPathViaFS(ctx, client, req, res.ReqID+1, "/data/"+filepath.ToSlash(strings.TrimPrefix(res.StdoutRef, "/")))
		if err != nil {
			return err
		}
		res.Data["stdout"] = b
	}
	if !hasStringData(res.Data, "stderr") && strings.TrimSpace(res.StderrRef) != "" {
		b, err := readToolPathViaFS(ctx, client, req, res.ReqID+2, "/data/"+filepath.ToSlash(strings.TrimPrefix(res.StderrRef, "/")))
		if err != nil {
			return err
		}
		res.Data["stderr"] = b
	}
	return nil
}

func readToolPathViaFS(ctx context.Context, client *trpc.Client, req trpc.Request, reqID int64, dataPath string) (string, error) {
	fsReq := trpc.Request{
		ReqID: reqID,
		Tool:  "fs.read",
		Allow: req.Allow,
		Args:  map[string]any{"path": dataPath},
	}
	fsRes, err := client.Call(ctx, fsReq)
	if err != nil {
		return "", err
	}
	if !fsRes.OK {
		return "", fmt.Errorf("legacy stream read failed: req=%d path=%s", reqID, dataPath)
	}
	v, _ := fsRes.Data["bytes"].(string)
	return v, nil
}

func hasStringData(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	if !ok {
		return false
	}
	_, ok = v.(string)
	return ok
}

func materializeToolRefs(runDir string, res *trpc.Response) error {
	if res == nil {
		return nil
	}
	if err := writeToolRef(runDir, res.StdoutRef, stringData(res.Data, "stdout")); err != nil {
		return err
	}
	if err := writeToolRef(runDir, res.StderrRef, stringData(res.Data, "stderr")); err != nil {
		return err
	}
	return nil
}

func writeToolRef(runDir, ref, content string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	if filepath.IsAbs(ref) {
		return fmt.Errorf("tool ref must be relative: %s", ref)
	}
	clean := filepath.Clean(ref)
	if strings.HasPrefix(clean, "..") {
		return fmt.Errorf("tool ref escapes run dir: %s", ref)
	}
	full := filepath.Join(runDir, clean)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("mkdir tool ref parent: %w", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write tool ref %s: %w", ref, err)
	}
	return nil
}

func stringData(m map[string]any, key string) string {
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

func parseReadyBanner(line string) ([]string, error) {
	s := strings.TrimSpace(line)
	const prefix = "READY v0 tools="
	if !strings.HasPrefix(s, prefix) {
		return nil, fmt.Errorf("agent READY mismatch: %q", s)
	}
	csv := strings.TrimSpace(strings.TrimPrefix(s, prefix))
	if csv == "" {
		return []string{}, nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func makeRunnerDetails(runDir string, outcome vm.Outcome, trStats transportStats) map[string]any {
	return map[string]any{
		"serial_log":       filepath.Join(runDir, "serial.log"),
		"fc_log":           filepath.Join(runDir, "fc.log"),
		"fc_metrics_log":   filepath.Join(runDir, "fc.metrics.log"),
		"lost_logs":        outcome.LostLogs,
		"lost_metrics":     outcome.LostMetrics,
		"guest_ready_ms":   outcome.GuestReadyMS,
		"connect_attempts": trStats.Attempts,
		"handshake_ms":     trStats.HandshakeMS,
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
	started := map[string]any{"label": cfg.label, "cmd": command}
	if cfg.vsockCID > 0 {
		started["tool"] = cfg.tool
		started["allow"] = splitCSV(cfg.allowCSV)
	}
	return runWithStore(
		cfg,
		"vm:run",
		started,
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
	}
	payload := map[string]any{
		"status":    status,
		"boot_ms":   outcome.BootMS,
		"resume_ms": outcome.ResumeMS,
	}
	for k, v := range details {
		payload[k] = v
	}
	addFailurePayload(payload, runErr)
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
		stringDetail(payload, "score_path"),
		stringDetail(payload, "skill_meta_path"),
	})
	cleanupErr := cleanupRunTransientPaths([]string{stringDetail(payload, "vsock_uds_path")})

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

	if finishEmitErr != nil || finishErr != nil || artifactErr != nil || cleanupErr != nil {
		return errors.Join(finishEmitErr, finishErr, artifactErr, cleanupErr)
	}
	if exportErr := maybeAutoExportFailure(ctx, cfg, st, tw, runtime.now, runID, task, status); exportErr != nil {
		return exportErr
	}
	if runErr != nil {
		return runErr
	}
	return nil
}

func cleanupRunTransientPaths(paths []string) error {
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		info, err := os.Lstat(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat transient path %s: %w", p, err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove transient socket %s: %w", p, err)
		}
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

type runFailure struct {
	Code      string `json:"code"`
	Msg       string `json:"msg"`
	Retryable bool   `json:"retryable"`
}

func classifyRunFailure(err error) runFailure {
	msg := err.Error()
	low := strings.ToLower(msg)
	code := "INTERNAL"
	switch {
	case strings.Contains(low, "denied"):
		code = "DENIED"
	case strings.Contains(low, "timeout"):
		code = "TIMEOUT"
	case strings.Contains(low, "disconnect"),
		strings.Contains(low, "broken pipe"),
		strings.Contains(low, "connection reset"),
		strings.Contains(low, "eof"),
		strings.Contains(low, "connect ack mismatch"),
		strings.Contains(low, "ready mismatch"):
		code = "DISCONNECT"
	case strings.Contains(low, "crash"),
		strings.Contains(low, "exit rc="),
		strings.Contains(low, "completion marker"),
		strings.Contains(low, "start machine"):
		code = "CRASH"
	}
	return runFailure{Code: code, Msg: msg, Retryable: false}
}

func addFailurePayload(payload map[string]any, err error) {
	if err == nil {
		return
	}
	f := classifyRunFailure(err)
	payload["error"] = f.Msg
	payload["error_code"] = f.Code
	payload["error_retryable"] = f.Retryable
	payload["error_obj"] = map[string]any{
		"code":      f.Code,
		"msg":       f.Msg,
		"retryable": f.Retryable,
	}
}

func maybeAutoExportFailure(ctx context.Context, cfg *runCommon, st *store.Store, tw *trace.Writer, now func() time.Time, runID, task, status string) error {
	if status != "failed" || !cfg.exportOnFail {
		return nil
	}
	bundlePath := filepath.Join(cfg.runsDir, runID+".partial.tar.zst")
	err := exportRunBundleFunc(ctx, cfg.dbPath, cfg.runsDir, runID, bundlePath, exportOptions{Partial: true})
	payload := map[string]any{
		"partial":     true,
		"bundle_path": bundlePath,
	}
	if err != nil {
		payload["status"] = "failed"
		payload["error"] = err.Error()
		_ = emit(ctx, st, tw, runID, task, "run.export.partial", payload, now)
		return fmt.Errorf("auto export partial bundle: %w", err)
	}
	payload["status"] = "ok"
	emitErr := emit(ctx, st, tw, runID, task, "run.export.partial", payload, now)
	artifactErr := persistRunArtifacts(ctx, st, runID, []string{bundlePath})
	if emitErr != nil || artifactErr != nil {
		return errors.Join(emitErr, artifactErr)
	}
	return nil
}

func persistRunArtifacts(ctx context.Context, st *store.Store, runID string, paths []string) error {
	seen := map[string]string{} // path -> sha256
	// Deduplicate input paths and existing registrations for this run
	existing, _ := st.DB().Query(`SELECT path, sha256 FROM artifacts WHERE run_id=?`, runID)
	if existing != nil {
		defer existing.Close()
		for existing.Next() {
			var p, s string
			if err := existing.Scan(&p, &s); err == nil {
				seen[p] = s
			}
		}
	}

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
		if oldSum, ok := seen[p]; ok {
			if oldSum == sum {
				continue
			}
			// Update: remove old registration if hash changed
			st.DB().ExecContext(ctx, `DELETE FROM artifacts WHERE run_id=? AND path=?`, runID, p)
		}
		seen[p] = sum
		if err := st.InsertArtifact(ctx, runID, p, sum, size); err != nil {
			return err
		}
	}
	for _, p := range extraRunArtifactPaths(paths) {
		if _, ok := seen[p]; ok {
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
	if receipt, ok := trace.ExtractToolReceipt(event, payload); ok {
		seq := receipt.Seq
		if seq <= 0 {
			seq = 1
		}
		if err := st.InsertToolCall(ctx, store.ToolCall{
			RunID:      runID,
			Seq:        seq,
			ReqID:      receipt.ReqID,
			Tool:       receipt.Tool,
			InputHash:  receipt.InputHash,
			OutputHash: receipt.OutputHash,
			InputRef:   receipt.InputRef,
			OutputRef:  receipt.OutputRef,
			StdoutRef:  receipt.StdoutRef,
			StderrRef:  receipt.StderrRef,
			RC:         receipt.RC,
			DurMS:      receipt.DurMS,
			BytesIn:    receipt.BytesIn,
			BytesOut:   receipt.BytesOut,
			ErrorCode:  receipt.ErrorCode,
		}); err != nil {
			return err
		}
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
