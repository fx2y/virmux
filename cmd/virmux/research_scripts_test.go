package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResearchDocsDriftWritesCertMarker(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "docs", "ops", "spec06-card.md"), "virmux research plan --query q\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "docs", "ops", "spec06-rollback-playbook.md"), "virmux research replay --run x\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "spec-0", "06-htn.jsonl"), "{\"id\":\"s06\"}\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "spec-0", "06", "50-c0-translation-seams.jsonl"), "virmux research <plan|map|reduce|replay|run>\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "spec-0", "06", "c0-cli-map.jsonl"), "{\"cmd_canon\":\"virmux research <plan|map|reduce|replay|run>\"}\n")

	out, err := runScriptFromRepo(t, repo, "research_docs_drift.sh", "--cert-ts", "2026-02-24T00:00:00Z", "--cert-id", "c1")
	if err != nil {
		t.Fatalf("expected success, got err=%v\n%s", err, out)
	}

	markerPath := filepath.Join(repo, "tmp", "research-docs-drift.ok")
	data, readErr := os.ReadFile(markerPath)
	if readErr != nil {
		t.Fatalf("expected marker file: %v", readErr)
	}
	var marker map[string]string
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("marker must be json: %v\n%s", err, string(data))
	}
	if marker["cert_ts"] != "2026-02-24T00:00:00Z" {
		t.Fatalf("unexpected cert_ts in marker: %q", marker["cert_ts"])
	}
}

func TestShipResearchRunsCleanupAndProducesSummary(t *testing.T) {
	t.Parallel()
	repo := newScriptTestRepo(t)
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "scripts", "research_cert.sh"), "#!/usr/bin/env bash\nset -euo pipefail\nmkdir -p tmp\ncat > tmp/research-sql-cert-summary.json <<'JSON'\n{\"cert_ts\":\"2026-02-24T00:00:00Z\",\"research_run_count\":1,\"research_reduce_count\":1,\"research_replay_count\":1,\"evidence_count\":1,\"reports_count\":1}\nJSON\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "scripts", "spec06_dod_matrix.sh"), "#!/usr/bin/env bash\nset -euo pipefail\ncert_ts=\"\"\nwhile [[ $# -gt 0 ]]; do\n  case \"$1\" in\n    --cert-ts) cert_ts=\"$2\"; shift 2 ;;\n    *) shift ;;\n  esac\ndone\n[[ \"$cert_ts\" == \"2026-02-24T00:00:00Z\" ]] || { echo \"bad cert_ts=$cert_ts\" >&2; exit 1; }\nmkdir -p tmp\ncat > tmp/spec06-dod-matrix.json <<'JSON'\n{\"dod\":[{\"id\":\"DOD-S06-1\",\"pass\":true}]}\nJSON\necho \"ok\" > tmp/spec06-residual-risk.md\n")
	mustWriteScriptFixtureFile(t, filepath.Join(repo, "scripts", "cleanup_audit.sh"), "#!/usr/bin/env bash\nset -euo pipefail\nmkdir -p tmp\necho ok > tmp/cleanup-audit.ok\n")
	for _, rel := range []string{
		filepath.Join(repo, "scripts", "research_cert.sh"),
		filepath.Join(repo, "scripts", "spec06_dod_matrix.sh"),
		filepath.Join(repo, "scripts", "cleanup_audit.sh"),
	} {
		if err := os.Chmod(rel, 0o755); err != nil {
			t.Fatalf("chmod %s: %v", rel, err)
		}
	}

	out, err := runScriptFromRepo(t, repo, "ship_research.sh")
	if err != nil {
		t.Fatalf("expected success, got err=%v\n%s", err, out)
	}
	if !strings.Contains(out, "ship:research: OK cert_ts=2026-02-24T00:00:00Z") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(repo, "tmp", "cleanup-audit.ok")); err != nil {
		t.Fatalf("cleanup audit marker missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "tmp", "ship-research-summary.json")); err != nil {
		t.Fatalf("ship summary missing: %v", err)
	}
}
