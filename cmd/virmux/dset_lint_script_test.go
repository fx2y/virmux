package main

import (
	"path/filepath"
	"testing"
)

func TestDsetLintFailsWhenEarlyRowIsInvalid(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	path := filepath.Join(repo, "dsets", "core", "core-v1.jsonl")
	mustWriteScriptFixtureFile(t, path, `{"id":"bad-1","input":{},"context_refs":[],"expected_properties":{}}
{"id":"ok-2","input":{},"context_refs":[],"expected_properties":{},"tags":["core"]}
`)
	out, err := runScriptFromRepo(t, repo, "dset_lint.sh")
	if err == nil {
		t.Fatalf("expected lint failure for invalid first row, output:\n%s", out)
	}
}

func TestDsetLintPassesValidFile(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	path := filepath.Join(repo, "dsets", "core", "core-v1.jsonl")
	mustWriteScriptFixtureFile(t, path, `{"id":"ok-1","input":{},"context_refs":[],"expected_properties":{},"tags":["core"]}
{"id":"ok-2","input":{},"context_refs":[],"expected_properties":{},"tags":["core"]}
`)
	out, err := runScriptFromRepo(t, repo, "dset_lint.sh")
	if err != nil {
		t.Fatalf("expected lint success, err=%v output:\n%s", err, out)
	}
}
