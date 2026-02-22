package vm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	BootMS   int64
	ResumeMS int64
	Serial   string
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
}

const defaultSmokeCommand = "echo __virmux_smoke__\nuname -a\necho ok"

func DefaultSmokeCommand() string {
	return defaultSmokeCommand
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

type session struct {
	machine    *firecracker.Machine
	stdin      *io.PipeWriter
	serialBuf  *bytes.Buffer
	stdoutF    *os.File
	stderrF    *os.File
	socketPath string
}

func (s *session) close() {
	if s.stdin != nil {
		_ = s.stdin.Close()
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
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, 0, fmt.Errorf("create run dir: %w", err)
	}

	stdoutF, err := os.Create(filepath.Join(runDir, "serial.log"))
	if err != nil {
		return nil, 0, fmt.Errorf("create serial.log: %w", err)
	}
	stderrF, err := os.Create(filepath.Join(runDir, "firecracker.stderr.log"))
	if err != nil {
		stdoutF.Close()
		return nil, 0, fmt.Errorf("create firecracker.stderr.log: %w", err)
	}

	socketPath := filepath.Join(runDir, "firecracker.sock")
	_ = os.Remove(socketPath)

	serialBuf := &bytes.Buffer{}
	stdinR, stdinW := io.Pipe()

	cfg := firecracker.Config{
		SocketPath:      socketPath,
		KernelImagePath: art.Kernel,
		KernelArgs:      "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda ro rootflags=noload init=/bin/sh",
		Drives:          buildDrives(art, dataVolumePath),
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(1),
			MemSizeMib: firecracker.Int64(memMiB),
			Smt:        firecracker.Bool(false),
		},
	}
	if vsock != nil {
		cfg.VsockDevices = []firecracker.VsockDevice{*vsock}
	}

	cmd := firecracker.VMCommandBuilder{}.
		WithBin(art.Firecracker).
		WithSocketPath(socketPath).
		WithStdin(stdinR).
		WithStdout(io.MultiWriter(stdoutF, serialBuf)).
		WithStderr(stderrF).
		Build(ctx)

	opts := []firecracker.Opt{firecracker.WithProcessRunner(cmd)}
	if snapshot != nil {
		memPath := snapshot.MemFilePath
		statePath := snapshot.SnapshotPath
		opts = append(opts, firecracker.WithSnapshot(memPath, statePath, func(sc *firecracker.SnapshotConfig) {
			sc.EnableDiffSnapshots = snapshot.EnableDiffSnapshots
			sc.ResumeVM = snapshot.ResumeVM
		}))
	}
	m, err := firecracker.NewMachine(ctx, cfg, opts...)
	if err != nil {
		stdoutF.Close()
		stderrF.Close()
		_ = stdinW.Close()
		return nil, 0, fmt.Errorf("new machine: %w", err)
	}

	started := time.Now()
	emitEvent(eventHook, "vm.boot.started", map[string]any{
		"socket_path": socketPath,
		"kernel":      art.Kernel,
		"rootfs":      art.Rootfs,
	})
	if err := m.Start(ctx); err != nil {
		stdoutF.Close()
		stderrF.Close()
		_ = stdinW.Close()
		return nil, 0, fmt.Errorf("start machine: %w", err)
	}

	bootMS := time.Since(started).Milliseconds()
	return &session{machine: m, stdin: stdinW, serialBuf: serialBuf, stdoutF: stdoutF, stderrF: stderrF, socketPath: socketPath}, bootMS, nil
}

func waitMachine(ctx context.Context, s *session, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.machine.Wait(waitCtx)
}

func serialScript(command string) string {
	return strings.Join([]string{
		"mkdir -p /dev/virmux-data",
		"mount -t ext4 /dev/vdb /dev/virmux-data || mount /dev/vdb /dev/virmux-data || true",
		"echo __cmd_start__",
		command,
		"echo __cmd_end__",
		"poweroff -f",
		"",
	}, "\n")
}

func Run(ctx context.Context, art Artifacts, runDir string, cfg RunConfig) (Outcome, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return Outcome{}, errors.New("vm command cannot be empty")
	}
	if cfg.Timeout <= 0 {
		return Outcome{}, errors.New("vm timeout must be > 0")
	}

	vsock := resolveVsockDevice(cfg.VsockCID, cfg.VsockPath)
	s, bootMS, err := startSession(ctx, art, runDir, cfg.MemMiB, cfg.DataVolumePath, nil, vsock, cfg.EventHook)
	if err != nil {
		return Outcome{}, err
	}
	defer s.close()

	time.Sleep(1500 * time.Millisecond)
	_, _ = io.WriteString(s.stdin, serialScript(command))
	emitEvent(cfg.EventHook, "vm.exec.injected", map[string]any{
		"command": command,
	})
	_ = s.stdin.Close()

	time.Sleep(4 * time.Second)
	_ = s.machine.StopVMM()
	waitErr := waitMachine(ctx, s, cfg.Timeout)
	exitPayload := map[string]any{}
	if waitErr != nil {
		exitPayload["wait_error"] = waitErr.Error()
	}
	emitEvent(cfg.EventHook, "vm.exit.observed", exitPayload)

	serial := s.serialBuf.String()
	emitSerialChunks(cfg.EventHook, serial, cfg.ChunkEventLimit, cfg.ChunkBytes)
	if err := validateSerialMarkers(serial, cfg.RequiredMarkers, waitErr); err != nil {
		return Outcome{}, err
	}
	return Outcome{BootMS: bootMS, Serial: serial}, nil
}

func validateSerialMarkers(serial string, required []string, waitErr error) error {
	if !strings.Contains(serial, "__cmd_end__") {
		if waitErr != nil {
			return fmt.Errorf("vm command markers missing; wait err=%w", waitErr)
		}
		return errors.New("vm command markers missing (__cmd_end__)")
	}
	for _, marker := range required {
		if strings.Contains(serial, marker) {
			continue
		}
		if waitErr != nil {
			return fmt.Errorf("vm output missing marker %q; wait err=%w", marker, waitErr)
		}
		return fmt.Errorf("vm output missing marker %q", marker)
	}
	return nil
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

	return Outcome{BootMS: bootMS, Serial: s.serialBuf.String()}, memPath, statePath, nil
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
	return Outcome{ResumeMS: resumeMS, Serial: s.serialBuf.String()}, nil
}
