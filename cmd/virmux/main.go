package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

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
	memMiB     int64
	timeout    time.Duration
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: virmux <vm-smoke|vm-zygote|vm-resume|slack-server>")
	}
	switch args[0] {
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
	timeoutSec := fs.Int("timeout-sec", 30, "vm timeout in seconds")
	fs.Int64Var(&cfg.memMiB, "mem-mib", 512, "microVM memory MiB")
	return fs, cfg, timeoutSec
}

func cmdVMSmoke(args []string) error {
	fs, cfg, timeoutSec := commonFlags("vm-smoke")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg.timeout = time.Duration(*timeoutSec) * time.Second
	return runWithStore(cfg, "vm:smoke", func(ctx context.Context, art vm.Artifacts, runDir string) (vm.Outcome, map[string]any, error) {
		outcome, err := vm.Smoke(ctx, art, runDir, cfg.memMiB, cfg.timeout)
		return outcome, map[string]any{"serial_log": filepath.Join(runDir, "serial.log")}, err
	})
}

func cmdVMZygote(args []string) error {
	fs, cfg, timeoutSec := commonFlags("vm-zygote")
	snapshotDir := fs.String("snapshot-dir", "", "snapshot directory (default vm/snapshots/<image_sha>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg.timeout = time.Duration(*timeoutSec) * time.Second

	return runWithStore(cfg, "vm:zygote", func(ctx context.Context, art vm.Artifacts, runDir string) (vm.Outcome, map[string]any, error) {
		base := *snapshotDir
		if base == "" {
			repoRoot := filepath.Dir(filepath.Dir(cfg.imagesLock))
			base = filepath.Join(repoRoot, "vm", "snapshots", art.ImageSHA)
		}
		outcome, memPath, statePath, err := vm.Zygote(ctx, art, runDir, base, cfg.memMiB, cfg.timeout)
		if err != nil {
			return outcome, nil, err
		}
		latest := map[string]any{
			"mem_path":   memPath,
			"state_path": statePath,
			"image_sha":  art.ImageSHA,
			"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := os.MkdirAll(base, 0o755); err != nil {
			return outcome, nil, err
		}
		data, _ := json.MarshalIndent(latest, "", "  ")
		if err := os.WriteFile(filepath.Join(base, "latest.json"), data, 0o644); err != nil {
			return outcome, nil, err
		}
		return outcome, map[string]any{"snapshot_dir": base, "mem_path": memPath, "state_path": statePath}, nil
	})
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

	return runWithStore(cfg, "vm:resume", func(ctx context.Context, art vm.Artifacts, runDir string) (vm.Outcome, map[string]any, error) {
		mPath := *memPath
		sPath := *statePath
		base := *snapshotDir
		if base == "" {
			repoRoot := filepath.Dir(filepath.Dir(cfg.imagesLock))
			base = filepath.Join(repoRoot, "vm", "snapshots", art.ImageSHA)
		}
		if mPath == "" || sPath == "" {
			data, err := os.ReadFile(filepath.Join(base, "latest.json"))
			if err != nil {
				return vm.Outcome{}, nil, fmt.Errorf("read latest snapshot metadata: %w", err)
			}
			var latest struct {
				MemPath   string `json:"mem_path"`
				StatePath string `json:"state_path"`
			}
			if err := json.Unmarshal(data, &latest); err != nil {
				return vm.Outcome{}, nil, fmt.Errorf("parse latest snapshot metadata: %w", err)
			}
			if mPath == "" {
				mPath = latest.MemPath
			}
			if sPath == "" {
				sPath = latest.StatePath
			}
		}

		outcome, err := vm.Resume(ctx, art, runDir, mPath, sPath, cfg.memMiB, cfg.timeout)
		if err == nil {
			return outcome, map[string]any{"mem_path": mPath, "state_path": sPath, "resume_mode": "snapshot"}, nil
		}
		fallbackDir := filepath.Join(runDir, "fallback-coldboot")
		fallback, fbErr := vm.Smoke(ctx, art, fallbackDir, cfg.memMiB, cfg.timeout)
		if fbErr != nil {
			return vm.Outcome{}, nil, fmt.Errorf("snapshot resume failed (%v); fallback smoke failed (%v)", err, fbErr)
		}
		fallback.ResumeMS = fallback.BootMS
		fallback.BootMS = 0
		return fallback, map[string]any{
			"mem_path":       mPath,
			"state_path":     sPath,
			"resume_mode":    "fallback_cold_boot",
			"resume_error":   err.Error(),
			"fallback_trace": filepath.Join(fallbackDir, "serial.log"),
		}, nil
	})
}

type vmRunner func(ctx context.Context, art vm.Artifacts, runDir string) (vm.Outcome, map[string]any, error)

func runWithStore(cfg *runCommon, task string, runner vmRunner) error {
	ctx := context.Background()
	art, err := vm.LoadArtifacts(cfg.imagesLock)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	runID := fmt.Sprintf("%d-%s", time.Now().UTC().UnixNano(), stringsForTask(task))
	runDir := filepath.Join(cfg.runsDir, runID)
	tracePath := filepath.Join(runDir, "trace.jsonl")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}

	tw, err := trace.NewWriter(tracePath)
	if err != nil {
		return err
	}
	defer tw.Close()

	started := time.Now().UTC()
	if err := st.StartRun(ctx, store.Run{ID: runID, Task: task, Label: cfg.label, ImageSHA: art.ImageSHA, StartedAt: started}); err != nil {
		return err
	}
	if err := emit(ctx, st, tw, runID, task, "run.started", map[string]any{"label": cfg.label}); err != nil {
		return err
	}

	outcome, details, runErr := runner(ctx, art, runDir)
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
	if err := emit(ctx, st, tw, runID, task, "run.finished", payload); err != nil {
		return err
	}
	if err := st.FinishRun(ctx, runID, status, outcome.BootMS, outcome.ResumeMS, tracePath, time.Now().UTC()); err != nil {
		return err
	}

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

	if runErr != nil {
		return runErr
	}
	return nil
}

func emit(ctx context.Context, st *store.Store, tw *trace.Writer, runID, task, event string, payload map[string]any) error {
	if err := tw.Emit(runID, task, event, payload); err != nil {
		return err
	}
	data, _ := json.Marshal(payload)
	if err := st.InsertEvent(ctx, runID, event, string(data), time.Now()); err != nil {
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
