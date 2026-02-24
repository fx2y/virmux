package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpec06C0FreezeScriptPassesWithCoveredPrimeSet(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	writeSpec06C0Fixture(t, repo, false)

	out, err := runScriptFromRepo(t, repo, "spec06_c0_freeze.sh")
	if err != nil {
		t.Fatalf("expected success, err=%v output=\n%s", err, out)
	}
	if !strings.Contains(out, "spec06:c0: OK") {
		t.Fatalf("expected OK output, got:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "tmp", "spec06-c0-freeze.ok")); statErr != nil {
		t.Fatalf("expected marker file: %v", statErr)
	}
}

func TestSpec06C0FreezeScriptFailsOnUncoveredGap(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	writeSpec06C0Fixture(t, repo, true)

	out, err := runScriptFromRepo(t, repo, "spec06_c0_freeze.sh")
	if err == nil {
		t.Fatalf("expected failure, output=\n%s", out)
	}
	if !strings.Contains(out, "uncovered prime gaps") {
		t.Fatalf("expected uncovered-gap diagnostics, output=\n%s", out)
	}
	if !strings.Contains(out, "spec06.tests.gap") {
		t.Fatalf("expected missing gap id in diagnostics, output=\n%s", out)
	}
}

func writeSpec06C0Fixture(t *testing.T, repo string, omitSpec06TestsGap bool) {
	t.Helper()
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "spec-0", "06-htn.jsonl"), `{"id":"s06","k":"root"}`+"\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "spec-0", "06-prime.jsonl"), strings.Join([]string{
		`{"id":"betA.plan_compiler.gap.cli","k":"gap"}`,
		`{"id":"spec06.tests.gap","k":"gap"}`,
		`{"id":"spec06.naming_drift","k":"risk"}`,
		`{"id":"spec06.trace_name_drift","k":"risk"}`,
		`{"id":"plan_first_before_tools.risk","k":"risk"}`,
		`{"id":"anti_pattern.no_cli_canon_split","k":"risk"}`,
		`{"id":"anti_pattern.no_new_evidence_plane","k":"risk"}`,
		`{"id":"kernelarg.serial_bias","k":"risk"}`,
	}, "\n")+"\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "spec-0", "06", "c0-cli-map.jsonl"), strings.Join([]string{
		`{"id":"map.cli.canon","k":"canon","cmd_canon":"virmux research <plan|map|reduce|replay|run>"}`,
		`{"id":"map.cli.alias-policy","k":"decision","alias":false,"why":"single canon only"}`,
	}, "\n")+"\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "spec-0", "06", "c0-data-map.jsonl"), `{"id":"map.data.trace","k":"decision","canonical":"runs/<id>/trace.ndjson","compat":"runs/<id>/trace.jsonl"}`+"\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "spec-0", "06", "c0-seams.jsonl"), strings.Join([]string{
		`{"id":"planner","k":"seam","svc":"planner","anch":["cmd/virmux/main.go:77-101#dispatch"]}`,
		`{"id":"scheduler","k":"seam","svc":"scheduler","anch":["cmd/virmux/main.go:77-101#dispatch"]}`,
		`{"id":"mapper","k":"seam","svc":"mapper","anch":["cmd/virmux/main.go:77-101#dispatch"]}`,
		`{"id":"reducer","k":"seam","svc":"reducer","anch":["cmd/virmux/main.go:77-101#dispatch"]}`,
		`{"id":"replay","k":"seam","svc":"replay","anch":["cmd/virmux/main.go:77-101#dispatch"]}`,
	}, "\n")+"\n")

	guardRows := []string{
		`{"id":"g.plan","k":"guard","owner":"C1","covers":["betA.plan_compiler.gap.cli","plan_first_before_tools.risk"]}`,
		`{"id":"g.docs","k":"guard","owner":"C0","covers":["spec06.naming_drift","spec06.trace_name_drift","anti_pattern.no_cli_canon_split","anti_pattern.no_new_evidence_plane"]}`,
	}
	if !omitSpec06TestsGap {
		guardRows = append(guardRows, `{"id":"g.tests","k":"guard","owner":"C6","covers":["spec06.tests.gap"]}`)
	}
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "spec-0", "06", "c0-guard-matrix.jsonl"), strings.Join(guardRows, "\n")+"\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "spec-0", "06-tasks.jsonl"), `{"id":"t.c0","k":"task","status":"done"}`+"\n")
}
