package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	for _, want := range []string{"mount -t ext4 /dev/vdb /mnt/data", "__cmd_start__", "uname -a", "__cmd_end__", "poweroff -f"} {
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
	if !strings.Contains(err.Error(), "vm command markers missing") {
		t.Fatalf("unexpected error: %v", err)
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
