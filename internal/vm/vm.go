package vm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	models "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

type Artifacts struct {
	ImageSHA    string
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
	return art, nil
}

type session struct {
	machine   *firecracker.Machine
	stdin     *io.PipeWriter
	serialBuf *bytes.Buffer
	stdoutF   *os.File
	stderrF   *os.File
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
}

func startSession(ctx context.Context, art Artifacts, runDir string, memMiB int64, snapshot *firecracker.SnapshotConfig) (*session, int64, error) {
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
		KernelArgs:      "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw init=/bin/sh",
		Drives:          firecracker.NewDrivesBuilder(art.Rootfs).Build(),
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(1),
			MemSizeMib: firecracker.Int64(memMiB),
			Smt:        firecracker.Bool(false),
		},
	}
	if snapshot != nil {
		cfg.Snapshot = *snapshot
	}

	cmd := firecracker.VMCommandBuilder{}.
		WithBin(art.Firecracker).
		WithSocketPath(socketPath).
		WithStdin(stdinR).
		WithStdout(io.MultiWriter(stdoutF, serialBuf)).
		WithStderr(stderrF).
		Build(ctx)

	m, err := firecracker.NewMachine(ctx, cfg, firecracker.WithProcessRunner(cmd))
	if err != nil {
		stdoutF.Close()
		stderrF.Close()
		_ = stdinW.Close()
		return nil, 0, fmt.Errorf("new machine: %w", err)
	}

	started := time.Now()
	if err := m.Start(ctx); err != nil {
		stdoutF.Close()
		stderrF.Close()
		_ = stdinW.Close()
		return nil, 0, fmt.Errorf("start machine: %w", err)
	}

	bootMS := time.Since(started).Milliseconds()
	return &session{machine: m, stdin: stdinW, serialBuf: serialBuf, stdoutF: stdoutF, stderrF: stderrF}, bootMS, nil
}

func waitMachine(ctx context.Context, s *session, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.machine.Wait(waitCtx)
}

func Smoke(ctx context.Context, art Artifacts, runDir string, memMiB int64, timeout time.Duration) (Outcome, error) {
	s, bootMS, err := startSession(ctx, art, runDir, memMiB, nil)
	if err != nil {
		return Outcome{}, err
	}
	defer s.close()

	time.Sleep(1500 * time.Millisecond)
	_, _ = io.WriteString(s.stdin, "echo __virmux_smoke__\nuname -a\necho ok\npoweroff -f\n")
	_ = s.stdin.Close()

	time.Sleep(2 * time.Second)
	_ = s.machine.StopVMM()
	waitErr := waitMachine(ctx, s, timeout)
	serial := s.serialBuf.String()
	if !strings.Contains(serial, "Linux") || !strings.Contains(serial, "ok") {
		if waitErr != nil {
			return Outcome{}, fmt.Errorf("vm smoke output missing markers; wait err=%w", waitErr)
		}
		return Outcome{}, errors.New("vm smoke output missing required markers (Linux/ok)")
	}
	return Outcome{BootMS: bootMS, Serial: serial}, nil
}

func Zygote(ctx context.Context, art Artifacts, runDir, snapshotDir string, memMiB int64, timeout time.Duration) (Outcome, string, string, error) {
	s, bootMS, err := startSession(ctx, art, runDir, memMiB, nil)
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
	_ = waitMachine(ctx, s, timeout)

	return Outcome{BootMS: bootMS, Serial: s.serialBuf.String()}, memPath, statePath, nil
}

func Resume(ctx context.Context, art Artifacts, runDir, memPath, statePath string, memMiB int64, timeout time.Duration) (Outcome, error) {
	snap := &firecracker.SnapshotConfig{
		MemFilePath:  memPath,
		SnapshotPath: statePath,
		ResumeVM:     false,
	}
	s, resumeMS, err := startSession(ctx, art, runDir, memMiB, snap)
	if err != nil {
		return Outcome{}, err
	}
	defer s.close()

	if err := s.machine.ResumeVM(ctx); err != nil {
		return Outcome{}, fmt.Errorf("resume vm: %w", err)
	}
	time.Sleep(500 * time.Millisecond)
	_, _ = io.WriteString(s.stdin, "echo resumed_ok\npoweroff -f\n")
	_ = s.stdin.Close()

	time.Sleep(5 * time.Second)
	_ = s.machine.StopVMM()
	waitErr := waitMachine(ctx, s, timeout)
	serial := s.serialBuf.String()
	if !strings.Contains(serial, "resumed_ok") {
		if waitErr != nil {
			return Outcome{}, fmt.Errorf("vm resume output missing resumed_ok; wait err=%w", waitErr)
		}
		return Outcome{}, errors.New("vm resume output missing resumed_ok marker")
	}
	return Outcome{ResumeMS: resumeMS, Serial: serial}, nil
}
