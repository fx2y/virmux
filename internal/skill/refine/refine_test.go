package refine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSuggestionsWritesRefineBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "dd")
	if err := os.MkdirAll(filepath.Join(skillDir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: dd\ndescription: x\nrequires: {bins: [], env: [], config: []}\nos: [linux]\n---\n# Steps\nDo x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "tests", "case01.json"), []byte(`{"id":"case01","tool":"shell.exec","args":{"cmd":"echo ok"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sugs, err := BuildSuggestions(skillDir, "run1", Score{Score: 0.4, Pass: false, Criterion: []Criterion{{ID: "format", Value: 0.2}, {ID: "actionability", Value: 0.8}}}, false)
	if err != nil {
		t.Fatalf("build suggestions: %v", err)
	}
	if len(sugs) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(sugs))
	}
	got := string(sugs[0].Content)
	if !strings.Contains(got, "## Refinement Notes") || !strings.Contains(got, "tighten `format`") {
		t.Fatalf("missing refine block:\n%s", got)
	}
}

func TestValidateFixtures(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "dd")
	if err := os.MkdirAll(filepath.Join(skillDir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "tests", "case01.json"), []byte(`{"id":"case01","tool":"shell.exec","args":{"cmd":"echo ok"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFixtures(skillDir); err != nil {
		t.Fatalf("validate fixtures: %v", err)
	}
}
