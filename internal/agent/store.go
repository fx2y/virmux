package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Meta struct {
	AgentID        string `json:"agent_id"`
	VolumePath     string `json:"volume_path"`
	LastSnapshotID string `json:"last_snapshot_id,omitempty"`
	UpdatedAt      string `json:"updated_at"`
}

type Store struct {
	agentsDir  string
	volumesDir string
}

func NewStore(agentsDir, volumesDir string) *Store {
	return &Store{agentsDir: agentsDir, volumesDir: volumesDir}
}

func (s *Store) Ensure(agentID string) (Meta, error) {
	id := strings.TrimSpace(agentID)
	if id == "" {
		return Meta{}, fmt.Errorf("agent id cannot be empty")
	}
	if err := os.MkdirAll(s.agentsDir, 0o755); err != nil {
		return Meta{}, fmt.Errorf("create agents dir: %w", err)
	}
	if err := os.MkdirAll(s.volumesDir, 0o755); err != nil {
		return Meta{}, fmt.Errorf("create volumes dir: %w", err)
	}

	metaPath := filepath.Join(s.agentsDir, id+".json")
	if _, err := os.Stat(metaPath); err == nil {
		return s.Load(id)
	}

	meta := Meta{
		AgentID:    id,
		VolumePath: filepath.Join(s.volumesDir, id+".ext4"),
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.Save(meta); err != nil {
		return Meta{}, err
	}
	return meta, nil
}

func (s *Store) Load(agentID string) (Meta, error) {
	id := strings.TrimSpace(agentID)
	if id == "" {
		return Meta{}, fmt.Errorf("agent id cannot be empty")
	}
	metaPath := filepath.Join(s.agentsDir, id+".json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return Meta{}, fmt.Errorf("read agent metadata: %w", err)
	}
	var meta Meta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return Meta{}, fmt.Errorf("parse agent metadata: %w", err)
	}
	if strings.TrimSpace(meta.AgentID) == "" {
		meta.AgentID = id
	}
	if strings.TrimSpace(meta.VolumePath) == "" {
		meta.VolumePath = filepath.Join(s.volumesDir, id+".ext4")
	}
	return meta, nil
}

func (s *Store) Save(meta Meta) error {
	id := strings.TrimSpace(meta.AgentID)
	if id == "" {
		return fmt.Errorf("agent id cannot be empty")
	}
	if strings.TrimSpace(meta.VolumePath) == "" {
		meta.VolumePath = filepath.Join(s.volumesDir, id+".ext4")
	}
	meta.AgentID = id
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := os.MkdirAll(s.agentsDir, 0o755); err != nil {
		return fmt.Errorf("create agents dir: %w", err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent metadata: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(s.agentsDir, id+".json"), data, 0o644); err != nil {
		return fmt.Errorf("write agent metadata: %w", err)
	}
	return nil
}
