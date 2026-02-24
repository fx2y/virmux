package main

import (
	"strings"
	"testing"
)

func TestRunUsageIncludesResearch(t *testing.T) {
	t.Parallel()
	err := run(nil)
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "research") {
		t.Fatalf("usage must include research command, got: %q", err.Error())
	}
}
