package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitFrontmatterStrictSentinels(t *testing.T) {
	t.Parallel()
	_, _, err := SplitFrontmatter([]byte("name: x\n"))
	if err == nil {
		t.Fatalf("expected missing sentinel error")
	}
}

func TestLoadDirRejectsUnknownFrontmatterKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkillFiles(t, dir,
		"---\nname: dd\ndescription: x\nos: [linux]\nunknown: nope\n---\n# Steps\n",
		"allowed_tools: [shell.exec]\nbudget: {tool_calls: 1, seconds: 10, tokens: 0}\n",
	)
	_, err := LoadDir(dir, DefaultEligibilityEnv())
	if err == nil || !strings.Contains(err.Error(), "unknown frontmatter key") {
		t.Fatalf("expected unknown key rejection, got %v", err)
	}
}

func TestLoadDirDormantOnMissingEnvNoCrash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSkillFiles(t, dir,
		"---\nname: dd\ndescription: x\nrequires: {env: [MISSING_ENV]}\nos: [linux]\n---\n# Steps\n",
		"allowed_tools: [shell.exec]\nbudget: {tool_calls: 1, seconds: 10, tokens: 0}\n",
	)
	s, err := LoadDir(dir, EligibilityEnv{
		GOOS:      "linux",
		LookupEnv: func(string) (string, bool) { return "", false },
		LookPath:  func(string) error { return nil },
		Config:    map[string]string{},
	})
	if err != nil {
		t.Fatalf("load dir: %v", err)
	}
	if !s.Dormant {
		t.Fatalf("expected dormant skill")
	}
	if len(s.Reasons) == 0 || s.Reasons[0] != "requires.env:MISSING_ENV" {
		t.Fatalf("unexpected dormant reasons: %#v", s.Reasons)
	}
}

func TestLintDirsPassesAndReturnsDormantInfo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "dd")
	writeSkillFiles(t, dir,
		"---\nname: dd\ndescription: x\nrequires: {env: [X]}\nos: [linux]\nmetadata: {foo: bar}\n---\n# Steps\n",
		"allowed_tools: [shell.exec,fs.read]\nbudget: {tool_calls: 1, seconds: 10, tokens: 0}\n",
	)
	results, err := LintDirs([]string{dir}, EligibilityEnv{
		GOOS:      "linux",
		LookupEnv: func(k string) (string, bool) { return "", false },
		LookPath:  func(string) error { return nil },
		Config:    map[string]string{},
	})
	if err != nil {
		t.Fatalf("lint dirs: %v", err)
	}
	if len(results) != 1 || results[0].Name != "dd" || !results[0].Dormant {
		t.Fatalf("unexpected lint results: %#v", results)
	}
}

func TestLoadToolsRejectsUnknownBudgetKey(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tools.yaml")
	if err := os.WriteFile(path, []byte("allowed_tools: [shell.exec]\nbudget: {oops: 1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadTools(path)
	if err == nil || !strings.Contains(err.Error(), "unknown tools.yaml budget key") {
		t.Fatalf("expected strict tools budget key rejection, got %v", err)
	}
}

func TestLoadToolsRejectsFractionalBudgetValue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tools.yaml")
	if err := os.WriteFile(path, []byte("allowed_tools: [shell.exec]\nbudget: {tool_calls: 1, seconds: 0.5, tokens: 0}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadTools(path)
	if err == nil || !strings.Contains(err.Error(), "must be integer") {
		t.Fatalf("expected integer budget rejection, got %v", err)
	}
}

func TestLoadToolsRejectsStringBudgetValue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tools.yaml")
	if err := os.WriteFile(path, []byte("allowed_tools: [shell.exec]\nbudget: {tool_calls: \"1\", seconds: 10, tokens: 0}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadTools(path)
	if err == nil || !strings.Contains(err.Error(), "must be integer") {
		t.Fatalf("expected integer budget rejection, got %v", err)
	}
}

func writeSkillFiles(t *testing.T, dir, skillMD, toolsYAML string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "case01.json"), []byte("{\"id\":\"case01\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rubric.yaml"), []byte("criteria:\n- {id: format, w: 0.4, must: true}\n- {id: completeness, w: 0.4}\n- {id: actionability, w: 0.2}\npass: 0.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools.yaml"), []byte(toolsYAML), 0o644); err != nil {
		t.Fatal(err)
	}
}
