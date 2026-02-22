package vm

import (
	"context"
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
