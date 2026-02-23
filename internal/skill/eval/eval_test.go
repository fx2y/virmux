package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	skillpkg "github.com/haris/virmux/internal/skill"
)

type fakeExec struct {
	handler func(skillpkg.Command) (skillpkg.CommandResult, error)
}

func (f fakeExec) Run(_ context.Context, c skillpkg.Command) (skillpkg.CommandResult, error) {
	return f.handler(c)
}

func TestBuildPromptfooConfig(t *testing.T) {
	t.Parallel()
	cfg, err := BuildPromptfooConfig(SkillSnapshot{
		Ref:  "HEAD",
		Body: "write memo",
		Fixtures: []Fixture{
			{ID: "case01", Path: "skills/dd/tests/case01.json", Raw: json.RawMessage(`{"id":"case01"}`)},
		},
	}, "openai:gpt-4.1-mini")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), `"providers":["openai:gpt-4.1-mini"]`) {
		t.Fatalf("providers missing: %s", cfg)
	}
	if !strings.Contains(string(cfg), `"fixture_id":"case01"`) {
		t.Fatalf("fixture id missing: %s", cfg)
	}
}

func TestParsePromptfooResultsAndCompareAB(t *testing.T) {
	t.Parallel()
	baseRaw := []byte(`{"results":[{"metadata":{"fixture_id":"case01"},"score":0.8,"success":true,"cost":1.1},{"metadata":{"fixture_id":"case02"},"score":0.9,"success":true,"cost":0.9}]}`)
	headRaw := []byte(`{"results":[{"metadata":{"fixture_id":"case01"},"score":0.7,"success":false,"cost":1.4},{"metadata":{"fixture_id":"case02"},"score":0.9,"success":true,"cost":1.0}]}`)
	base, _, err := ParsePromptfooResults(baseRaw, []string{"case01", "case02"})
	if err != nil {
		t.Fatal(err)
	}
	head, _, err := ParsePromptfooResults(headRaw, []string{"case01", "case02"})
	if err != nil {
		t.Fatal(err)
	}
	v := CompareAB(base, head, ABThresholds{MinScoreDelta: 0, MaxFailRateDelta: 0})
	if v.Pass {
		t.Fatalf("expected regression fail, got %+v", v)
	}
	if !strings.Contains(v.Reason, "score_p50_delta") {
		t.Fatalf("reason mismatch: %+v", v)
	}
}

func TestRunPromptfooCallsValidateAndEval(t *testing.T) {
	t.Parallel()
	calls := []string{}
	ex := fakeExec{handler: func(c skillpkg.Command) (skillpkg.CommandResult, error) {
		calls = append(calls, c.Name+" "+strings.Join(c.Args, " "))
		return skillpkg.CommandResult{ExitCode: 0, EndedAt: time.Now().UTC()}, nil
	}}
	if err := RunPromptfoo(context.Background(), ex, ".", "promptfoo", "cfg.json", "out.json", time.Second); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if !strings.Contains(calls[0], "validate") || !strings.Contains(calls[1], "eval") {
		t.Fatalf("unexpected calls: %#v", calls)
	}
}

func TestLoadSkillSnapshotViaGitExec(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.ToSlash(filepath.Join("skills", "dd", "SKILL.md"))
	testPath := filepath.ToSlash(filepath.Join("skills", "dd", "tests", "case01.json"))
	ex := fakeExec{handler: func(c skillpkg.Command) (skillpkg.CommandResult, error) {
		key := strings.Join(append([]string{c.Name}, c.Args...), " ")
		switch key {
		case "git show HEAD:" + skillPath:
			return skillpkg.CommandResult{ExitCode: 0, Stdout: []byte("---\nname: dd\ndescription: x\nos: [linux]\n---\n# body\n")}, nil
		case "git ls-tree -r --name-only HEAD -- skills/dd/tests":
			return skillpkg.CommandResult{ExitCode: 0, Stdout: []byte(testPath + "\n")}, nil
		case "git show HEAD:" + testPath:
			return skillpkg.CommandResult{ExitCode: 0, Stdout: []byte(`{"id":"case01","tool":"shell.exec","cmd":"echo ok"}`)}, nil
		default:
			t.Fatalf("unexpected command: %s", key)
			return skillpkg.CommandResult{}, nil
		}
	}}
	snap, err := LoadSkillSnapshot(context.Background(), ex, repo, "skills", "dd", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Fixtures) != 1 || snap.Fixtures[0].ID != "case01" {
		t.Fatalf("snapshot fixtures mismatch: %+v", snap)
	}
}
