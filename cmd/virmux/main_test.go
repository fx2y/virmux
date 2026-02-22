package main

import (
	"strings"
	"testing"
	"time"

	"github.com/haris/virmux/internal/vm"
)

func TestParseVMRunArgsOverridesCommand(t *testing.T) {
	t.Parallel()
	cfg, command, err := parseVMRunArgs("vm-run", []string{"--cmd", "echo hello", "--label", "demo", "--agent", "A", "--timeout-sec", "9", "--mem-mib", "1024"}, "uname -a")
	if err != nil {
		t.Fatalf("parse vm-run args: %v", err)
	}
	if command != "echo hello" {
		t.Fatalf("expected command override, got %q", command)
	}
	if cfg.label != "demo" {
		t.Fatalf("expected label demo, got %q", cfg.label)
	}
	if cfg.timeout != 9*time.Second {
		t.Fatalf("expected timeout 9s, got %s", cfg.timeout)
	}
	if cfg.agentID != "A" {
		t.Fatalf("expected agent A, got %q", cfg.agentID)
	}
	if cfg.memMiB != 1024 {
		t.Fatalf("expected mem_mib 1024, got %d", cfg.memMiB)
	}
}

func TestParseVMRunArgsUsesSmokeDefaultForCompatibility(t *testing.T) {
	t.Parallel()
	cfg, command, err := parseVMRunArgs("vm-smoke", nil, vm.DefaultSmokeCommand())
	if err != nil {
		t.Fatalf("parse vm-smoke args: %v", err)
	}
	if command != vm.DefaultSmokeCommand() {
		t.Fatalf("expected vm-smoke default command, got %q", command)
	}
	if cfg.timeout != 30*time.Second {
		t.Fatalf("expected default timeout 30s, got %s", cfg.timeout)
	}
}

func TestParseVMRunArgsRejectsEmptyCommand(t *testing.T) {
	t.Parallel()
	_, _, err := parseVMRunArgs("vm-run", []string{"--cmd", "   "}, "uname -a")
	if err == nil {
		t.Fatalf("expected empty command error")
	}
	if !strings.Contains(err.Error(), "--cmd cannot be empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunUsageIncludesVMRun(t *testing.T) {
	t.Parallel()
	err := run(nil)
	if err == nil {
		t.Fatalf("expected usage error")
	}
	if !strings.Contains(err.Error(), "vm-run") {
		t.Fatalf("usage should mention vm-run, got %q", err.Error())
	}
}
