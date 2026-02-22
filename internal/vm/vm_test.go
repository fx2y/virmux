package vm

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
)

func TestLoadArtifactsRequiresFiles(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "vm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".cache", "ghostfleet", "images", "abc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "vm", "images.lock"), []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadArtifacts(filepath.Join(repo, "vm", "images.lock")); err == nil {
		t.Fatalf("expected missing artifact error")
	}
}

func TestRunRejectsEmptyCommand(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), Artifacts{}, t.TempDir(), RunConfig{
		MemMiB:  128,
		Timeout: 5 * time.Second,
		Command: "   ",
	})
	if err == nil {
		t.Fatalf("expected empty command error")
	}
	if !strings.Contains(err.Error(), "vm command cannot be empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsNonPositiveTimeout(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), Artifacts{}, t.TempDir(), RunConfig{
		MemMiB:  128,
		Timeout: 0,
		Command: "uname -a",
	})
	if err == nil {
		t.Fatalf("expected timeout validation error")
	}
	if !strings.Contains(err.Error(), "vm timeout must be > 0") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSerialScriptWrapsMarkers(t *testing.T) {
	t.Parallel()
	got := serialScript("uname -a")
	for _, want := range []string{"mount -t ext4 /dev/vdb /dev/virmux-data", "__virmux_exec_start__", "uname -a", "__virmux_exec_rc__", "poweroff -f"} {
		if !strings.Contains(got, want) {
			t.Fatalf("script missing marker %q: %q", want, got)
		}
	}
}

func TestBuildDrivesRootfsReadonlyAndDataWritable(t *testing.T) {
	t.Parallel()
	art := Artifacts{Rootfs: "/tmp/rootfs.ext4"}
	drives := buildDrives(art, "/tmp/A.ext4")
	if len(drives) != 2 {
		t.Fatalf("expected 2 drives, got %d", len(drives))
	}
	if drives[0].IsRootDevice == nil || !*drives[0].IsRootDevice {
		t.Fatalf("expected rootfs root-device")
	}
	if drives[0].IsReadOnly == nil || !*drives[0].IsReadOnly {
		t.Fatalf("expected rootfs readonly")
	}
	if drives[1].IsReadOnly == nil || *drives[1].IsReadOnly {
		t.Fatalf("expected data drive writable")
	}
}

func TestEmitSerialChunksRespectsLimit(t *testing.T) {
	t.Parallel()
	var got []Event
	hook := func(evt Event) { got = append(got, evt) }
	emitSerialChunks(hook, "abcdefghijklmnopqrstuvwxyz", 2, 5)
	if len(got) != 2 {
		t.Fatalf("expected 2 chunk events, got %d", len(got))
	}
	if got[0].Kind != "vm.serial.chunk" || got[1].Kind != "vm.serial.chunk" {
		t.Fatalf("unexpected event kinds: %+v", got)
	}
	if got[0].Payload["chunk"] != "abcde" {
		t.Fatalf("unexpected first chunk: %#v", got[0].Payload["chunk"])
	}
}

func TestValidateSerialMarkersFailurePath(t *testing.T) {
	t.Parallel()
	err := validateSerialMarkers("Linux boot\n", []string{"ok"}, errors.New("wait timeout"))
	if err == nil {
		t.Fatalf("expected marker validation error")
	}
	if !strings.Contains(err.Error(), "vm command completion marker missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractCommandOutputIgnoresEchoedInput(t *testing.T) {
	t.Parallel()
	serial := strings.Join([]string{
		"# echo __virmux_exec_start__",
		"echo ok",
		"printf '__virmux_exec_rc__=%s\\n' \"$__virmux_rc\"",
		"__virmux_exec_start__",
		"Linux host guest",
		"ok",
		"__virmux_exec_rc__=0",
	}, "\n")
	out, rc, ok := extractCommandOutput(serial)
	if !ok {
		t.Fatalf("expected parsed command output")
	}
	if rc != 0 {
		t.Fatalf("expected rc=0 got %d", rc)
	}
	if strings.Contains(out, "printf '__virmux_exec_rc__") {
		t.Fatalf("parsed echoed input instead of executed output: %q", out)
	}
	if !strings.Contains(out, "Linux host guest") || !strings.Contains(out, "ok") {
		t.Fatalf("missing command output in parsed transcript: %q", out)
	}
}

func TestResolveVsockDeviceValidation(t *testing.T) {
	t.Parallel()
	if got := resolveVsockDevice(0, "/tmp/v.sock"); got != nil {
		t.Fatalf("expected nil vsock for cid=0")
	}
	if got := resolveVsockDevice(3, "   "); got != nil {
		t.Fatalf("expected nil vsock for blank uds path")
	}
	got := resolveVsockDevice(3, "/tmp/v.sock")
	if got == nil {
		t.Fatalf("expected non-nil vsock")
	}
	if got.CID != 3 {
		t.Fatalf("expected guest cid 3, got %d", got.CID)
	}
	if got.Path != "/tmp/v.sock" {
		t.Fatalf("expected uds path /tmp/v.sock, got %q", got.Path)
	}
}

func TestRuntimeUsesInjectedBooter(t *testing.T) {
	t.Parallel()
	errSentinel := errors.New("boom")
	fake := &fakeBooter{err: errSentinel}
	_, err := runWithRuntime(context.Background(), Artifacts{}, t.TempDir(), RunConfig{
		MemMiB:    128,
		Timeout:   time.Second,
		Command:   "echo ok",
		VsockCID:  3,
		VsockPath: "/tmp/vsock.sock",
	}, vmRuntime{booter: fake})
	if !errors.Is(err, errSentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if !fake.called {
		t.Fatalf("expected injected booter to be called")
	}
	if fake.req.Vsock == nil || fake.req.Vsock.CID != 3 {
		t.Fatalf("expected vsock request propagated, got %+v", fake.req.Vsock)
	}
}

func TestDefaultKernelArgsIncludeSerialHardening(t *testing.T) {
	t.Parallel()
	args := defaultKernelArgs()
	for _, want := range []string{"quiet", "loglevel=1", "8250.nr_uarts=1", "rootflags=noload"} {
		if !strings.Contains(args, want) {
			t.Fatalf("kernel args missing %q: %q", want, args)
		}
	}
}

func TestLoggerSingleShotRejectsSecondClaim(t *testing.T) {
	t.Parallel()
	guard := &loggerSingleShot{}
	if err := guard.claim(); err != nil {
		t.Fatalf("first claim should succeed: %v", err)
	}
	if err := guard.claim(); err == nil {
		t.Fatalf("second claim should fail")
	}
}

func TestExtractLostCounters(t *testing.T) {
	t.Parallel()
	line := `{"utc_timestamp_ms":1,"signals":{"lost_logs":2,"lost_metrics":3}}`
	lostLogs, lostMetrics := extractLostCounters(line)
	if lostLogs != 2 || lostMetrics != 3 {
		t.Fatalf("unexpected lost counters logs=%d metrics=%d", lostLogs, lostMetrics)
	}
}

func TestWaitMachineWithWatchdogKillsUnresponsiveMachine(t *testing.T) {
	t.Parallel()
	fm := &fakeWatchdogMachine{
		pid:     4242,
		waitErr: context.DeadlineExceeded,
	}
	var events []Event
	var killed atomic.Bool
	err := waitMachineWithWatchdogDeps(context.Background(), fm, "/tmp/fc.sock", 80*time.Millisecond, func(evt Event) {
		events = append(events, evt)
		if evt.Kind == "vm.watchdog.kill" {
			fm.unblockWait()
		}
	}, watchdogDeps{
		now: func() time.Time { return time.Now() },
		kill: func(pid int, sig syscall.Signal) error {
			if pid != 4242 || sig != syscall.SIGKILL {
				t.Fatalf("unexpected kill pid=%d sig=%v", pid, sig)
			}
			killed.Store(true)
			return nil
		},
		probeEvery: 5 * time.Millisecond,
		probeAfter: 10 * time.Millisecond,
		probeTTL:   5 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected wait deadline exceeded, got %v", err)
	}
	if !killed.Load() {
		t.Fatalf("expected watchdog kill")
	}
	found := false
	for _, evt := range events {
		if evt.Kind != "vm.watchdog.kill" {
			continue
		}
		found = true
		if evt.Payload["reason"] != "wait_unresponsive" {
			t.Fatalf("unexpected watchdog reason: %#v", evt.Payload["reason"])
		}
	}
	if !found {
		t.Fatalf("expected vm.watchdog.kill event")
	}
}

func TestWaitMachineWithWatchdogSkipsKillWhenProbeHealthy(t *testing.T) {
	t.Parallel()
	sock := filepath.Join(t.TempDir(), "fc.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	fm := &fakeWatchdogMachine{
		pid:      99,
		waitErr:  context.DeadlineExceeded,
		waitDone: make(chan struct{}),
	}
	defer fm.unblockWait()
	killCalled := false
	err = waitMachineWithWatchdogDeps(context.Background(), fm, sock, 40*time.Millisecond, nil, watchdogDeps{
		now: func() time.Time { return time.Now() },
		kill: func(pid int, sig syscall.Signal) error {
			killCalled = true
			return nil
		},
		probeEvery: 5 * time.Millisecond,
		probeAfter: 10 * time.Millisecond,
		probeTTL:   5 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected wait deadline exceeded, got %v", err)
	}
	if killCalled {
		t.Fatalf("watchdog should not kill healthy probe")
	}
	_ = ln.Close()
	<-done
}

func TestWatchdogDepsForTimeoutPushesProbeNearDeadline(t *testing.T) {
	t.Parallel()
	deps := watchdogDepsForTimeout(30*time.Second, watchdogDeps{
		probeEvery: 250 * time.Millisecond,
		probeAfter: 3 * time.Second,
	})
	if deps.probeAfter != 29500*time.Millisecond {
		t.Fatalf("expected probeAfter=29.5s, got %s", deps.probeAfter)
	}
}

func TestWatchdogDepsForTimeoutDelaysProbeWhenTimeoutIsShort(t *testing.T) {
	t.Parallel()
	deps := watchdogDepsForTimeout(10*time.Millisecond, watchdogDeps{
		probeEvery: 1 * time.Millisecond,
		probeAfter: 7 * time.Millisecond,
	})
	if deps.probeAfter != 8*time.Millisecond {
		t.Fatalf("expected probeAfter=8ms, got %s", deps.probeAfter)
	}
}

func TestResumeWithHookReturnsWaitError(t *testing.T) {
	origStart := startSessionFn
	origStop := stopVMMFn
	origWait := waitMachineWithWatchdogFn
	origSleep := sleepFn
	origAlive := processAliveFn
	t.Cleanup(func() {
		startSessionFn = origStart
		stopVMMFn = origStop
		waitMachineWithWatchdogFn = origWait
		sleepFn = origSleep
		processAliveFn = origAlive
	})

	startSessionFn = func(_ context.Context, _ Artifacts, _ string, _ int64, _ string, _ *firecracker.SnapshotConfig, _ *firecracker.VsockDevice, _ EventHook) (*session, int64, error) {
		return &session{
			serialBuf: &safeBuffer{},
			pipes:     &sessionPipes{},
		}, 42, nil
	}
	stopVMMFn = func(*session) error { return nil }
	waitMachineWithWatchdogFn = func(context.Context, *session, time.Duration, EventHook) error {
		return context.DeadlineExceeded
	}
	sleepFn = func(time.Duration) {}
	processAliveFn = func(int) bool { return true }

	var events []Event
	outcome, err := ResumeWithHook(context.Background(), Artifacts{}, t.TempDir(), "/tmp/mem", "/tmp/state", 128, time.Second, "", func(evt Event) {
		events = append(events, evt)
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected wait error, got %v", err)
	}
	if outcome.ResumeMS != 42 {
		t.Fatalf("expected ResumeMS=42 got %d", outcome.ResumeMS)
	}
	found := false
	for _, evt := range events {
		if evt.Kind != "vm.exit.observed" {
			continue
		}
		if evt.Payload["wait_error"] != context.DeadlineExceeded.Error() {
			t.Fatalf("expected wait_error payload, got %#v", evt.Payload["wait_error"])
		}
		found = true
	}
	if !found {
		t.Fatalf("expected vm.exit.observed event")
	}
}

func TestNormalizeResumeWaitErrIgnoresDeadlineWhenProcessExited(t *testing.T) {
	origAlive := processAliveFn
	t.Cleanup(func() { processAliveFn = origAlive })
	processAliveFn = func(int) bool { return false }
	err := normalizeResumeWaitErr(context.DeadlineExceeded, func() (int, error) {
		return 77, nil
	})
	if err != nil {
		t.Fatalf("expected nil error when process already exited, got %v", err)
	}
}

func TestNormalizeResumeWaitErrKeepsDeadlineWhenProcessAlive(t *testing.T) {
	origAlive := processAliveFn
	t.Cleanup(func() { processAliveFn = origAlive })
	processAliveFn = func(int) bool { return true }
	err := normalizeResumeWaitErr(context.DeadlineExceeded, func() (int, error) {
		return 88, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
}

type fakeBooter struct {
	called bool
	err    error
	req    sessionBootRequest
}

func (f *fakeBooter) Start(_ context.Context, req sessionBootRequest) (*session, int64, error) {
	f.called = true
	f.req = req
	return nil, 0, f.err
}

type fakeWatchdogMachine struct {
	pid      int
	waitErr  error
	waitDone chan struct{}
}

func (f *fakeWatchdogMachine) Wait(ctx context.Context) error {
	if f.waitDone == nil {
		f.waitDone = make(chan struct{})
	}
	select {
	case <-ctx.Done():
		if f.waitErr != nil {
			return f.waitErr
		}
		return ctx.Err()
	case <-f.waitDone:
		if f.waitErr != nil {
			return f.waitErr
		}
		return nil
	}
}

func (f *fakeWatchdogMachine) PID() (int, error) { return f.pid, nil }

func (f *fakeWatchdogMachine) unblockWait() {
	if f.waitDone == nil {
		return
	}
	select {
	case <-f.waitDone:
	default:
		close(f.waitDone)
	}
}
