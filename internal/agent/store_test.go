package agent

import (
	"path/filepath"
	"testing"
)

func TestEnsureAndRoundTrip(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	s := NewStore(filepath.Join(base, "agents"), filepath.Join(base, "volumes"))

	meta, err := s.Ensure("A")
	if err != nil {
		t.Fatalf("ensure agent: %v", err)
	}
	if meta.AgentID != "A" {
		t.Fatalf("expected agent A, got %q", meta.AgentID)
	}
	if filepath.Base(meta.VolumePath) != "A.ext4" {
		t.Fatalf("expected volume A.ext4, got %q", meta.VolumePath)
	}

	meta.LastSnapshotID = "snap-1"
	if err := s.Save(meta); err != nil {
		t.Fatalf("save agent: %v", err)
	}

	got, err := s.Load("A")
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	if got.LastSnapshotID != "snap-1" {
		t.Fatalf("expected snapshot id snap-1, got %q", got.LastSnapshotID)
	}
	if got.UpdatedAt == "" {
		t.Fatalf("expected updated_at")
	}
}

func TestLoadDefaultsMissingVolumePath(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	s := NewStore(filepath.Join(base, "agents"), filepath.Join(base, "volumes"))

	if err := s.Save(Meta{AgentID: "B", VolumePath: ""}); err != nil {
		t.Fatalf("save defaulted: %v", err)
	}
	got, err := s.Load("B")
	if err != nil {
		t.Fatalf("load defaulted: %v", err)
	}
	if filepath.Base(got.VolumePath) != "B.ext4" {
		t.Fatalf("expected default volume path, got %q", got.VolumePath)
	}
}
