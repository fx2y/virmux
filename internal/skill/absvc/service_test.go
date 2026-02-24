package absvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	skillpkg "github.com/haris/virmux/internal/skill"
	"github.com/haris/virmux/internal/store"
)

type fakeExec struct{}

func (fakeExec) Run(_ context.Context, c skillpkg.Command) (skillpkg.CommandResult, error) {
	if c.Name == "git" {
		if len(c.Args) >= 2 && c.Args[0] == "show" {
			switch c.Args[1] {
			case "h1:skills/dd/SKILL.md", "b1:skills/dd/SKILL.md":
				return skillpkg.CommandResult{ExitCode: 0, Stdout: []byte("---\nname: dd\ndescription: x\nrequires: {bins: [], env: [], config: []}\nos: [linux]\n---\n# Steps\nUse fixture.\n")}, nil
			case "h1:skills/dd/tests/case01.json":
				return skillpkg.CommandResult{ExitCode: 0, Stdout: []byte(`{"id":"case01","tool":"shell.exec","args":{"cmd":"echo head"}}`)}, nil
			case "b1:skills/dd/tests/case01.json":
				return skillpkg.CommandResult{ExitCode: 0, Stdout: []byte(`{"id":"case01","tool":"shell.exec","args":{"cmd":"echo base"}}`)}, nil
			}
		}
		if len(c.Args) >= 4 && c.Args[0] == "ls-tree" {
			return skillpkg.CommandResult{ExitCode: 0, Stdout: []byte("skills/dd/tests/case01.json\n")}, nil
		}
	}
	if c.Name == "pf" {
		if len(c.Args) > 0 && c.Args[0] == "validate" {
			return skillpkg.CommandResult{ExitCode: 0}, nil
		}
		if len(c.Args) > 0 && c.Args[0] == "eval" {
			var outPath string
			for i := 0; i < len(c.Args)-1; i++ {
				if c.Args[i] == "--output" {
					outPath = c.Args[i+1]
					break
				}
			}
			if outPath == "" {
				return skillpkg.CommandResult{}, fmt.Errorf("missing --output")
			}
			body := `{"results":[{"metadata":{"fixture_id":"case01"},"score":0.9,"success":true,"cost":1.0}]}`
			if strings.Contains(outPath, ".base.") {
				body = `{"results":[{"metadata":{"fixture_id":"case01"},"score":0.8,"success":true,"cost":1.0}]}`
			}
			if err := os.WriteFile(outPath, []byte(body), 0o644); err != nil {
				return skillpkg.CommandResult{}, err
			}
			return skillpkg.CommandResult{ExitCode: 0}, nil
		}
	}
	return skillpkg.CommandResult{}, fmt.Errorf("unexpected command %s %v", c.Name, c.Args)
}

func TestServiceRunPersistsEvalRowsAndFrozenFixtures(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runsDir := filepath.Join(tmp, "runs")
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc := Service{Store: st, Exec: fakeExec{}, Now: func() time.Time { return time.Unix(1700000000, 0).UTC() }}
	res, err := svc.Run(context.Background(), Input{
		RepoDir:      tmp,
		SkillsDir:    "skills",
		RunsDir:      runsDir,
		SkillName:    "dd",
		BaseRef:      "b1",
		HeadRef:      "h1",
		Provider:     "openai:gpt-4.1-mini",
		PromptfooBin: "pf",
		TimeoutSec:   30,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Pass {
		t.Fatalf("expected pass verdict")
	}
	baseCfg, err := os.ReadFile(filepath.Join(runsDir, res.EvalID, "promptfoo.base.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(baseCfg), "echo head") || strings.Contains(string(baseCfg), "echo base") {
		t.Fatalf("base cfg fixture freeze mismatch: %s", string(baseCfg))
	}
	var rows int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM eval_runs WHERE id=?`, res.EvalID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("expected one eval_run row, got %d", rows)
	}
}

func TestServicePairwiseMode(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	runsDir := filepath.Join(tmp, "runs")
	dbPath := filepath.Join(runsDir, "virmux.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc := Service{Store: st, Exec: fakeExec{}, Now: func() time.Time { return time.Unix(1700000000, 0).UTC() }}
	res, err := svc.Run(context.Background(), Input{
		RepoDir:      tmp,
		SkillsDir:    "skills",
		RunsDir:      runsDir,
		SkillName:    "dd",
		BaseRef:      "b1",
		HeadRef:      "h1",
		Provider:     "openai:gpt-4.1-mini",
		PromptfooBin: "pf",
		TimeoutSec:   30,
		JudgeMode:    "pairwise",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if res.ExperimentID == "" {
		t.Fatal("expected ExperimentID in result")
	}
	if res.Winner != "B" {
		t.Fatalf("expected winner B, got %s", res.Winner)
	}

	var expCount int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM experiments WHERE id=?`, res.ExperimentID).Scan(&expCount); err != nil {
		t.Fatal(err)
	}
	if expCount != 1 {
		t.Fatalf("expected one experiment row, got %d", expCount)
	}

	var compCount int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM comparisons WHERE experiment_id=?`, res.ExperimentID).Scan(&compCount); err != nil {
		t.Fatal(err)
	}
	if compCount != 1 {
		t.Fatalf("expected one comparison row, got %d", compCount)
	}

	report, err := st.GetExperimentReport(context.Background(), res.ExperimentID)
	if err != nil {
		t.Fatal(err)
	}
	if report.WinsB != 1 || report.WinsA != 0 || report.Ties != 0 {
		t.Fatalf("report mismatch: %+v", report)
	}
}
