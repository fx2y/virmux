package vm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	models "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

type Artifacts struct {
	ImageSHA    string
	KernelSHA   string
	RootfsSHA   string
	Dir         string
	Firecracker string
	Kernel      string
	Rootfs      string
}

type Outcome struct {
	BootMS       int64
	ResumeMS     int64
	GuestReadyMS int64
	LostLogs     int64
	LostMetrics  int64
	Serial       string
}

type Event struct {
	Kind    string
	Payload map[string]any
}

type EventHook func(Event)

type RunConfig struct {
	MemMiB          int64
	Timeout         time.Duration
	Command         string
	RequiredMarkers []string
	DataVolumePath  string
	ChunkEventLimit int
	ChunkBytes      int
	VsockCID        int64
	VsockPath       string
	EventHook       EventHook
	AfterInject     func(context.Context) error
}

const defaultSmokeCommand = "echo __virmux_smoke__\nuname -a\necho ok"

func DefaultSmokeCommand() string {
	return defaultSmokeCommand
}

func defaultKernelArgs() string {
	return "console=ttyS0 reboot=k panic=1 pci=off quiet loglevel=1 8250.nr_uarts=1 root=/dev/vda ro rootflags=noload init=/bin/sh"
}

func LoadArtifacts(imagesLockPath string) (Artifacts, error) {
	raw, err := os.ReadFile(imagesLockPath)
	if err != nil {
		return Artifacts{}, fmt.Errorf("read images lock: %w", err)
	}
	sha := strings.TrimSpace(string(raw))
	if sha == "" {
		return Artifacts{}, errors.New("images lock is empty")
	}
	repoRoot := filepath.Dir(filepath.Dir(imagesLockPath))
	dir := filepath.Join(repoRoot, ".cache", "ghostfleet", "images", sha)
	art := Artifacts{
		ImageSHA:    sha,
		Dir:         dir,
		Firecracker: filepath.Join(dir, "firecracker"),
		Kernel:      filepath.Join(dir, "vmlinux"),
		Rootfs:      filepath.Join(dir, "rootfs.ext4"),
	}
	for _, p := range []string{art.Firecracker, art.Kernel, art.Rootfs} {
		if _, err := os.Stat(p); err != nil {
			return Artifacts{}, fmt.Errorf("artifact missing (%s): %w", p, err)
		}
	}
	kernelSHA, err := fileSHA256(art.Kernel)
	if err != nil {
		return Artifacts{}, fmt.Errorf("hash kernel: %w", err)
	}
	rootfsSHA, err := fileSHA256(art.Rootfs)
	if err != nil {
		return Artifacts{}, fmt.Errorf("hash rootfs: %w", err)
	}
	art.KernelSHA = kernelSHA
	art.RootfsSHA = rootfsSHA
	return art, nil
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

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *safeBuffer) Contains(marker string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Contains(s.b.String(), marker)
}

type lineTrackedWriter struct {
	out   io.Writer
	lines *atomic.Int64
}

func (w *lineTrackedWriter) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	if n > 0 {
		w.lines.Add(int64(bytes.Count(p[:n], []byte{'\n'})))
	}
	return n, err
}

type pipeSummary struct {
	LostLogs    int64
	LostMetrics int64
	LogLines    int64
	MetricLines int64
}

type sessionPipes struct {
	logFIFOPath     string
	metricsFIFOPath string
	logPath         string
	metricsPath     string

	logF     *os.File
	metricsF *os.File
	metricsR *os.File

	startMetricsOnce sync.Once
	wg               sync.WaitGroup
	closeOnce        sync.Once

	logLines    atomic.Int64
	metricLines atomic.Int64
	lostLogs    atomic.Int64
	lostMetrics atomic.Int64
}

func newSessionPipes(runDir string) (*sessionPipes, error) {
	logPath := filepath.Join(runDir, "fc.log")
	metricsPath := filepath.Join(runDir, "fc.metrics.log")
	logF, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create fc.log: %w", err)
	}
	metricsF, err := os.Create(metricsPath)
	if err != nil {
		_ = logF.Close()
		return nil, fmt.Errorf("create fc.metrics.log: %w", err)
	}
	return &sessionPipes{
		logFIFOPath:     filepath.Join(runDir, "fc.log.fifo"),
		metricsFIFOPath: filepath.Join(runDir, "fc.metrics.fifo"),
		logPath:         logPath,
		metricsPath:     metricsPath,
		logF:            logF,
		metricsF:        metricsF,
	}, nil
}

func (p *sessionPipes) configure(cfg *firecracker.Config) {
	cfg.LogFifo = p.logFIFOPath
	cfg.MetricsFifo = p.metricsFIFOPath
	cfg.LogLevel = "Info"
	cfg.FifoLogWriter = &lineTrackedWriter{out: p.logF, lines: &p.logLines}
}

func (p *sessionPipes) logHandler() firecracker.Handler {
	return firecracker.Handler{
		Name: "virmux.StartMetricsDrainer",
		Fn: func(context.Context, *firecracker.Machine) error {
			return p.startMetricsDrainer()
		},
	}
}

func (p *sessionPipes) startMetricsDrainer() error {
	var startErr error
	p.startMetricsOnce.Do(func() {
		metricsR, err := os.OpenFile(p.metricsFIFOPath, os.O_RDWR, 0)
		if err != nil {
			startErr = fmt.Errorf("open metrics fifo: %w", err)
			return
		}
		p.metricsR = metricsR
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			scanner := bufio.NewScanner(metricsR)
			for scanner.Scan() {
				line := scanner.Text()
				p.metricLines.Add(1)
				_, _ = io.WriteString(p.metricsF, line)
				_, _ = io.WriteString(p.metricsF, "\n")
				lostLogs, lostMetrics := extractLostCounters(line)
				updateMax(&p.lostLogs, lostLogs)
				updateMax(&p.lostMetrics, lostMetrics)
			}
		}()
	})
	return startErr
}

func updateMax(dst *atomic.Int64, v int64) {
	for {
		cur := dst.Load()
		if v <= cur {
			return
		}
		if dst.CompareAndSwap(cur, v) {
			return
		}
	}
}

func extractLostCounters(line string) (lostLogs int64, lostMetrics int64) {
	var payload any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return 0, 0
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch typed := v.(type) {
		case map[string]any:
			for key, value := range typed {
				next := key
				if path != "" {
					next = path + "." + key
				}
				walk(next, value)
			}
		case []any:
			for i, value := range typed {
				walk(fmt.Sprintf("%s[%d]", path, i), value)
			}
		case float64:
			key := strings.ToLower(path)
			if !strings.Contains(key, "lost") {
				return
			}
			count := int64(typed)
			if strings.Contains(key, "log") {
				lostLogs += count
			}
			if strings.Contains(key, "metric") {
				lostMetrics += count
			}
		}
	}
	walk("", payload)
	return lostLogs, lostMetrics
}

func (p *sessionPipes) summary() pipeSummary {
	return pipeSummary{
		LostLogs:    p.lostLogs.Load(),
		LostMetrics: p.lostMetrics.Load(),
		LogLines:    p.logLines.Load(),
		MetricLines: p.metricLines.Load(),
	}
}

func (p *sessionPipes) close() {
	p.closeOnce.Do(func() {
		if p.metricsR != nil {
			_ = p.metricsR.Close()
		}
		p.wg.Wait()
		if p.logF != nil {
			_ = p.logF.Close()
		}
		if p.metricsF != nil {
			_ = p.metricsF.Close()
		}
		_ = os.Remove(p.logFIFOPath)
		_ = os.Remove(p.metricsFIFOPath)
	})
}

type loggerSingleShot struct {
	configured atomic.Bool
}

func (l *loggerSingleShot) claim() error {
	if l.configured.CompareAndSwap(false, true) {
		return nil
	}
	return errors.New("firecracker logger configure called more than once")
}

type sessionBootRequest struct {
	Artifacts       Artifacts
	RunDir          string
	MemMiB          int64
	DataVolumePath  string
	Snapshot        *firecracker.SnapshotConfig
	Vsock           *firecracker.VsockDevice
	EventHook       EventHook
	KernelArgs      string
	EnableTelemetry bool
}

type sessionBooter interface {
	Start(context.Context, sessionBootRequest) (*session, int64, error)
}

type firecrackerBooter struct {
	loggerGuard *loggerSingleShot
}

func newFirecrackerBooter() *firecrackerBooter {
	return &firecrackerBooter{loggerGuard: &loggerSingleShot{}}
}

type session struct {
	machine    *firecracker.Machine
	stdin      *io.PipeWriter
	serialBuf  *safeBuffer
	stdoutF    *os.File
	stderrF    *os.File
	socketPath string
	pipes      *sessionPipes
}

func (b *firecrackerBooter) Start(ctx context.Context, req sessionBootRequest) (*session, int64, error) {
	if err := os.MkdirAll(req.RunDir, 0o755); err != nil {
		return nil, 0, fmt.Errorf("create run dir: %w", err)
	}

	stdoutF, err := os.Create(filepath.Join(req.RunDir, "serial.log"))
	if err != nil {
		return nil, 0, fmt.Errorf("create serial.log: %w", err)
	}
	stderrF, err := os.Create(filepath.Join(req.RunDir, "firecracker.stderr.log"))
	if err != nil {
		_ = stdoutF.Close()
		return nil, 0, fmt.Errorf("create firecracker.stderr.log: %w", err)
	}

	socketPath := filepath.Join(req.RunDir, "firecracker.sock")
	_ = os.Remove(socketPath)

	serialBuf := &safeBuffer{}
	stdinR, stdinW := io.Pipe()

	pipes, err := newSessionPipes(req.RunDir)
	if err != nil {
		_ = stdoutF.Close()
		_ = stderrF.Close()
		_ = stdinW.Close()
		return nil, 0, err
	}

	kernelArgs := strings.TrimSpace(req.KernelArgs)
	if kernelArgs == "" {
		kernelArgs = defaultKernelArgs()
	}
	cfg := firecracker.Config{
		SocketPath:      socketPath,
		KernelImagePath: req.Artifacts.Kernel,
		KernelArgs:      kernelArgs,
		Drives:          buildDrives(req.Artifacts, req.DataVolumePath),
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(1),
			MemSizeMib: firecracker.Int64(req.MemMiB),
			Smt:        firecracker.Bool(false),
		},
	}
	if req.Vsock != nil {
		cfg.VsockDevices = []firecracker.VsockDevice{*req.Vsock}
	}
	pipes.configure(&cfg)

	cmd := firecracker.VMCommandBuilder{}.
		WithBin(req.Artifacts.Firecracker).
		WithSocketPath(socketPath).
		WithStdin(stdinR).
		WithStdout(io.MultiWriter(stdoutF, serialBuf)).
		WithStderr(stderrF).
		Build(ctx)

	opts := []firecracker.Opt{firecracker.WithProcessRunner(cmd)}
	if req.Snapshot != nil {
		memPath := req.Snapshot.MemFilePath
		statePath := req.Snapshot.SnapshotPath
		opts = append(opts, firecracker.WithSnapshot(memPath, statePath, func(sc *firecracker.SnapshotConfig) {
			sc.EnableDiffSnapshots = req.Snapshot.EnableDiffSnapshots
			sc.ResumeVM = req.Snapshot.ResumeVM
		}))
	}

	m, err := firecracker.NewMachine(ctx, cfg, opts...)
	if err != nil {
		pipes.close()
		_ = stdoutF.Close()
		_ = stderrF.Close()
		_ = stdinW.Close()
		return nil, 0, fmt.Errorf("new machine: %w", err)
	}
	m.Handlers.FcInit = m.Handlers.FcInit.Swap(firecracker.Handler{
		Name: firecracker.BootstrapLoggingHandlerName,
		Fn: func(ctx context.Context, m *firecracker.Machine) error {
			if err := b.loggerGuard.claim(); err != nil {
				return err
			}
			return firecracker.BootstrapLoggingHandler.Fn(ctx, m)
		},
	})
	m.Handlers.FcInit = m.Handlers.FcInit.AppendAfter(firecracker.CreateLogFilesHandlerName, pipes.logHandler())

	started := time.Now()
	emitEvent(req.EventHook, "vm.boot.started", map[string]any{
		"socket_path":     socketPath,
		"kernel":          req.Artifacts.Kernel,
		"rootfs":          req.Artifacts.Rootfs,
		"fc_log_fifo":     pipes.logFIFOPath,
		"fc_metrics_fifo": pipes.metricsFIFOPath,
	})
	if err := m.Start(ctx); err != nil {
		pipes.close()
		_ = stdoutF.Close()
		_ = stderrF.Close()
		_ = stdinW.Close()
		return nil, 0, fmt.Errorf("start machine: %w", err)
	}

	bootMS := time.Since(started).Milliseconds()
	return &session{
		machine:    m,
		stdin:      stdinW,
		serialBuf:  serialBuf,
		stdoutF:    stdoutF,
		stderrF:    stderrF,
		socketPath: socketPath,
		pipes:      pipes,
	}, bootMS, nil
}

func (s *session) close() {
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.pipes != nil {
		s.pipes.close()
	}
	if s.stdoutF != nil {
		_ = s.stdoutF.Close()
	}
	if s.stderrF != nil {
		_ = s.stderrF.Close()
	}
	if strings.TrimSpace(s.socketPath) != "" {
		_ = os.Remove(s.socketPath)
	}
}

func emitEvent(hook EventHook, kind string, payload map[string]any) {
	if hook == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	hook(Event{Kind: kind, Payload: payload})
}

func EnsureExt4Volume(path string, sizeMiB int64) error {
	if sizeMiB <= 0 {
		sizeMiB = 128
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create volumes dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create volume: %w", err)
	}
	if err := f.Truncate(sizeMiB * 1024 * 1024); err != nil {
		f.Close()
		return fmt.Errorf("size volume: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close volume: %w", err)
	}
	cmd := exec.Command("mkfs.ext4", "-F", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkfs.ext4 volume: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func buildDrives(art Artifacts, dataVolumePath string) []models.Drive {
	drives := []models.Drive{
		{
			DriveID:      firecracker.String("rootfs"),
			PathOnHost:   firecracker.String(art.Rootfs),
			IsRootDevice: firecracker.Bool(true),
			IsReadOnly:   firecracker.Bool(true),
		},
	}
	if strings.TrimSpace(dataVolumePath) != "" {
		drives = append(drives, models.Drive{
			DriveID:      firecracker.String("data"),
			PathOnHost:   firecracker.String(dataVolumePath),
			IsRootDevice: firecracker.Bool(false),
			IsReadOnly:   firecracker.Bool(false),
		})
	}
	return drives
}

func startSession(ctx context.Context, art Artifacts, runDir string, memMiB int64, dataVolumePath string, snapshot *firecracker.SnapshotConfig, vsock *firecracker.VsockDevice, eventHook EventHook) (*session, int64, error) {
	return newFirecrackerBooter().Start(ctx, sessionBootRequest{
		Artifacts:      art,
		RunDir:         runDir,
		MemMiB:         memMiB,
		DataVolumePath: dataVolumePath,
		Snapshot:       snapshot,
		Vsock:          vsock,
		EventHook:      eventHook,
		KernelArgs:     defaultKernelArgs(),
	})
}

func waitMachine(ctx context.Context, s *session, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.machine.Wait(waitCtx)
}

func serialScript(command string) string {
	return strings.Join([]string{
		"mount -t proc proc /proc || true",
		"mount -t sysfs sysfs /sys || true",
		"mkdir -p /dev/virmux-data",
		"mount -t ext4 /dev/vdb /dev/virmux-data || mount /dev/vdb /dev/virmux-data || true",
		"echo __virmux_exec_start__",
		command,
		"__virmux_rc=$?",
		"printf '__virmux_exec_rc__=%s\\n' \"$__virmux_rc\"",
		"poweroff -f",
		"",
	}, "\n")
}

type guestReadyWaiter interface {
	Wait(context.Context, *safeBuffer, time.Duration) (int64, error)
}

type serialReadyWaiter struct {
	markers   []string
	pollEvery time.Duration
}

func defaultReadyWaiter() serialReadyWaiter {
	return serialReadyWaiter{
		markers:   []string{"# ", "/ #"},
		pollEvery: 50 * time.Millisecond,
	}
}

type guestReadyTimeoutError struct {
	Timeout time.Duration
	Markers []string
}

func (e guestReadyTimeoutError) Error() string {
	return fmt.Sprintf("guest readiness timeout after %s (markers=%s)", e.Timeout, strings.Join(e.Markers, ","))
}

func (w serialReadyWaiter) Wait(ctx context.Context, serial *safeBuffer, timeout time.Duration) (int64, error) {
	if timeout <= 0 {
		return 0, errors.New("guest readiness timeout must be > 0")
	}
	if w.pollEvery <= 0 {
		w.pollEvery = 50 * time.Millisecond
	}
	if len(w.markers) == 0 {
		w.markers = []string{"Linux"}
	}
	started := time.Now()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(w.pollEvery)
	defer ticker.Stop()

	checkReady := func() bool {
		for _, marker := range w.markers {
			if serial.Contains(marker) {
				return true
			}
		}
		return false
	}
	if checkReady() {
		return time.Since(started).Milliseconds(), nil
	}

	for {
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("guest readiness canceled: %w", ctx.Err())
		case <-deadline.C:
			return 0, guestReadyTimeoutError{Timeout: timeout, Markers: w.markers}
		case <-ticker.C:
			if checkReady() {
				return time.Since(started).Milliseconds(), nil
			}
		}
	}
}

type vmRuntime struct {
	booter sessionBooter
	ready  guestReadyWaiter
}

func defaultVMRuntime() vmRuntime {
	return vmRuntime{
		booter: newFirecrackerBooter(),
		ready:  defaultReadyWaiter(),
	}
}

func runWithRuntime(ctx context.Context, art Artifacts, runDir string, cfg RunConfig, runtime vmRuntime) (Outcome, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return Outcome{}, errors.New("vm command cannot be empty")
	}
	if cfg.Timeout <= 0 {
		return Outcome{}, errors.New("vm timeout must be > 0")
	}
	if runtime.booter == nil {
		runtime.booter = newFirecrackerBooter()
	}
	if runtime.ready == nil {
		ready := defaultReadyWaiter()
		runtime.ready = ready
	}

	vsock := resolveVsockDevice(cfg.VsockCID, cfg.VsockPath)
	s, bootMS, err := runtime.booter.Start(ctx, sessionBootRequest{
		Artifacts:      art,
		RunDir:         runDir,
		MemMiB:         cfg.MemMiB,
		DataVolumePath: cfg.DataVolumePath,
		Vsock:          vsock,
		EventHook:      cfg.EventHook,
		KernelArgs:     defaultKernelArgs(),
	})
	if err != nil {
		return Outcome{}, err
	}
	defer s.close()

	readyMS, readyErr := runtime.ready.Wait(ctx, s.serialBuf, cfg.Timeout)
	if readyErr != nil {
		emitEvent(cfg.EventHook, "vm.exec.injected", map[string]any{
			"command":  command,
			"injected": false,
			"error":    readyErr.Error(),
		})
		_ = s.machine.StopVMM()
		waitErr := waitMachine(ctx, s, cfg.Timeout)
		exitPayload := map[string]any{}
		if waitErr != nil {
			exitPayload["wait_error"] = waitErr.Error()
		}
		emitEvent(cfg.EventHook, "vm.exit.observed", exitPayload)
		serial := s.serialBuf.String()
		emitSerialChunks(cfg.EventHook, serial, cfg.ChunkEventLimit, cfg.ChunkBytes)
		summary := s.pipes.summary()
		return Outcome{
			BootMS:       bootMS,
			GuestReadyMS: 0,
			LostLogs:     summary.LostLogs,
			LostMetrics:  summary.LostMetrics,
			Serial:       serial,
		}, fmt.Errorf("wait for guest ready: %w", readyErr)
	}
	emitEvent(cfg.EventHook, "vm.guest.ready", map[string]any{
		"latency_ms": readyMS,
		"method":     "serial_marker",
	})

	_, _ = io.WriteString(s.stdin, serialScript(command))
	emitEvent(cfg.EventHook, "vm.exec.injected", map[string]any{
		"command":  command,
		"injected": true,
	})
	if cfg.AfterInject != nil {
		if err := cfg.AfterInject(ctx); err != nil {
			_ = s.machine.StopVMM()
			_ = waitMachine(ctx, s, cfg.Timeout)
			emitEvent(cfg.EventHook, "vm.exit.observed", map[string]any{"wait_error": err.Error()})
			serial := s.serialBuf.String()
			emitSerialChunks(cfg.EventHook, serial, cfg.ChunkEventLimit, cfg.ChunkBytes)
			summary := s.pipes.summary()
			return Outcome{BootMS: bootMS, GuestReadyMS: readyMS, LostLogs: summary.LostLogs, LostMetrics: summary.LostMetrics, Serial: serial}, err
		}
	}
	_ = s.stdin.Close()

	commandWaitErr := waitForExecCompletionLine(ctx, s.serialBuf, cfg.Timeout)
	_ = s.machine.StopVMM()
	waitErr := waitMachine(ctx, s, cfg.Timeout)
	exitPayload := map[string]any{}
	if waitErr != nil {
		exitPayload["wait_error"] = waitErr.Error()
	}
	emitEvent(cfg.EventHook, "vm.exit.observed", exitPayload)

	serial := s.serialBuf.String()
	emitSerialChunks(cfg.EventHook, serial, cfg.ChunkEventLimit, cfg.ChunkBytes)
	summary := s.pipes.summary()
	outcome := Outcome{
		BootMS:       bootMS,
		GuestReadyMS: readyMS,
		LostLogs:     summary.LostLogs,
		LostMetrics:  summary.LostMetrics,
		Serial:       serial,
	}
	if commandWaitErr != nil {
		return outcome, commandWaitErr
	}
	if err := validateSerialMarkers(serial, cfg.RequiredMarkers, waitErr); err != nil {
		return outcome, err
	}
	return outcome, nil
}

func waitForExecCompletionLine(ctx context.Context, serial *safeBuffer, timeout time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("wait for exec completion timeout must be > 0")
	}
	if _, _, ok := extractCommandOutput(serial.String()); ok {
		return nil
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for exec completion canceled: %w", ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("wait for exec completion timeout after %s", timeout)
		case <-ticker.C:
			if _, _, ok := extractCommandOutput(serial.String()); ok {
				return nil
			}
		}
	}
}

func waitForSerialMarker(ctx context.Context, serial *safeBuffer, marker string, timeout time.Duration) error {
	if marker == "" {
		return nil
	}
	if timeout <= 0 {
		return fmt.Errorf("wait for serial marker %q timeout must be > 0", marker)
	}
	if serial.Contains(marker) {
		return nil
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for serial marker %q canceled: %w", marker, ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("wait for serial marker %q timeout after %s", marker, timeout)
		case <-ticker.C:
			if serial.Contains(marker) {
				return nil
			}
		}
	}
}

func Run(ctx context.Context, art Artifacts, runDir string, cfg RunConfig) (Outcome, error) {
	return runWithRuntime(ctx, art, runDir, cfg, defaultVMRuntime())
}

func validateSerialMarkers(serial string, required []string, waitErr error) error {
	output, rc, ok := extractCommandOutput(serial)
	if !ok {
		if waitErr != nil {
			return fmt.Errorf("vm command completion marker missing; wait err=%w", waitErr)
		}
		return errors.New("vm command completion marker missing")
	}
	if rc != 0 {
		if waitErr != nil {
			return fmt.Errorf("vm command exit rc=%d; wait err=%w", rc, waitErr)
		}
		return fmt.Errorf("vm command exit rc=%d", rc)
	}
	for _, marker := range required {
		if strings.Contains(output, marker) {
			continue
		}
		if waitErr != nil {
			return fmt.Errorf("vm command output missing marker %q; wait err=%w", marker, waitErr)
		}
		return fmt.Errorf("vm command output missing marker %q", marker)
	}
	return nil
}

func extractCommandOutput(serial string) (string, int, bool) {
	lines := strings.Split(strings.ReplaceAll(serial, "\r\n", "\n"), "\n")
	const startLine = "__virmux_exec_start__"
	const rcPrefix = "__virmux_exec_rc__="
	start := -1
	for i, line := range lines {
		if trimShellPromptPrefix(line) == startLine {
			start = i
			break
		}
	}
	if start < 0 {
		return "", 0, false
	}
	for i := start + 1; i < len(lines); i++ {
		line := trimShellPromptPrefix(lines[i])
		if !strings.HasPrefix(line, rcPrefix) {
			continue
		}
		rcText := strings.TrimPrefix(line, rcPrefix)
		rc, err := strconv.Atoi(rcText)
		if err != nil {
			return "", 0, false
		}
		return strings.Join(lines[start+1:i], "\n"), rc, true
	}
	return "", 0, false
}

func trimShellPromptPrefix(line string) string {
	s := strings.TrimSpace(line)
	for strings.HasPrefix(s, "# ") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "# "))
	}
	if s == "#" {
		return ""
	}
	return s
}

func emitSerialChunks(hook EventHook, serial string, limit, chunkBytes int) {
	if hook == nil || limit <= 0 {
		return
	}
	if chunkBytes <= 0 {
		chunkBytes = 512
	}
	raw := []byte(serial)
	if len(raw) == 0 {
		return
	}
	start := 0
	emitted := 0
	for start < len(raw) && emitted < limit {
		end := start + chunkBytes
		if end > len(raw) {
			end = len(raw)
		}
		emitEvent(hook, "vm.serial.chunk", map[string]any{
			"index": emitted,
			"chunk": string(raw[start:end]),
			"total": len(raw),
		})
		emitted++
		start = end
	}
}

func resolveVsockDevice(cid int64, udsPath string) *firecracker.VsockDevice {
	if cid <= 0 || strings.TrimSpace(udsPath) == "" {
		return nil
	}
	return &firecracker.VsockDevice{
		ID:   "vsock0",
		Path: udsPath,
		CID:  uint32(cid),
	}
}

func Smoke(ctx context.Context, art Artifacts, runDir string, memMiB int64, timeout time.Duration, dataVolumePath string) (Outcome, error) {
	return SmokeWithHook(ctx, art, runDir, memMiB, timeout, dataVolumePath, nil)
}

func SmokeWithHook(ctx context.Context, art Artifacts, runDir string, memMiB int64, timeout time.Duration, dataVolumePath string, hook EventHook) (Outcome, error) {
	return Run(ctx, art, runDir, RunConfig{
		MemMiB:          memMiB,
		Timeout:         timeout,
		Command:         defaultSmokeCommand,
		RequiredMarkers: []string{"Linux", "ok"},
		DataVolumePath:  dataVolumePath,
		EventHook:       hook,
	})
}

func Zygote(ctx context.Context, art Artifacts, runDir, snapshotDir string, memMiB int64, timeout time.Duration, dataVolumePath string) (Outcome, string, string, error) {
	return ZygoteWithHook(ctx, art, runDir, snapshotDir, memMiB, timeout, dataVolumePath, nil)
}

func ZygoteWithHook(ctx context.Context, art Artifacts, runDir, snapshotDir string, memMiB int64, timeout time.Duration, dataVolumePath string, hook EventHook) (Outcome, string, string, error) {
	s, bootMS, err := startSession(ctx, art, runDir, memMiB, dataVolumePath, nil, nil, hook)
	if err != nil {
		return Outcome{}, "", "", err
	}
	defer s.close()

	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return Outcome{}, "", "", fmt.Errorf("create snapshot dir: %w", err)
	}
	memPath := filepath.Join(snapshotDir, "vm.mem")
	statePath := filepath.Join(snapshotDir, "vm.state")

	time.Sleep(4 * time.Second)
	if err := s.machine.PauseVM(ctx); err != nil {
		return Outcome{}, "", "", fmt.Errorf("pause vm: %w", err)
	}
	if err := s.machine.CreateSnapshot(ctx, memPath, statePath); err != nil {
		return Outcome{}, "", "", fmt.Errorf("create snapshot: %w", err)
	}
	_ = s.machine.StopVMM()
	waitErr := waitMachine(ctx, s, timeout)
	exitPayload := map[string]any{}
	if waitErr != nil {
		exitPayload["wait_error"] = waitErr.Error()
	}
	emitEvent(hook, "vm.exit.observed", exitPayload)
	summary := s.pipes.summary()

	return Outcome{
		BootMS:      bootMS,
		LostLogs:    summary.LostLogs,
		LostMetrics: summary.LostMetrics,
		Serial:      s.serialBuf.String(),
	}, memPath, statePath, nil
}

func Resume(ctx context.Context, art Artifacts, runDir, memPath, statePath string, memMiB int64, timeout time.Duration, dataVolumePath string) (Outcome, error) {
	return ResumeWithHook(ctx, art, runDir, memPath, statePath, memMiB, timeout, dataVolumePath, nil)
}

func ResumeWithHook(ctx context.Context, art Artifacts, runDir, memPath, statePath string, memMiB int64, timeout time.Duration, dataVolumePath string, hook EventHook) (Outcome, error) {
	snap := &firecracker.SnapshotConfig{
		MemFilePath:  memPath,
		SnapshotPath: statePath,
		ResumeVM:     true,
	}
	s, resumeMS, err := startSession(ctx, art, runDir, memMiB, dataVolumePath, snap, nil, hook)
	if err != nil {
		return Outcome{}, err
	}
	defer s.close()

	time.Sleep(1200 * time.Millisecond)
	emitEvent(hook, "vm.exec.injected", map[string]any{
		"command": "resume_noop_stop",
	})
	_ = s.machine.StopVMM()
	waitErr := waitMachine(ctx, s, 2*time.Second)
	exitPayload := map[string]any{}
	if waitErr != nil {
		exitPayload["wait_error"] = waitErr.Error()
	}
	emitEvent(hook, "vm.exit.observed", exitPayload)
	summary := s.pipes.summary()
	return Outcome{
		ResumeMS:    resumeMS,
		LostLogs:    summary.LostLogs,
		LostMetrics: summary.LostMetrics,
		Serial:      s.serialBuf.String(),
	}, nil
}
