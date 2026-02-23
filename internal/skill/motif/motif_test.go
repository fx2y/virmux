package motif

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillpkg "github.com/haris/virmux/internal/skill"
)

func TestRankCandidatesTriggersAtThreeWithPassRate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "dd")
	features := make([]RunFeature, 0, 3)
	for i := 0; i < 3; i++ {
		f, err := BuildFeature(BuildInput{
			RunID:   "r" + string('1'+rune(i)),
			Skill:   "dd",
			Score:   0.9,
			Pass:    true,
			CostEst: 0.1,
			ToolCalls: []ToolCallRow{
				{Seq: 1, Tool: "shell.exec", InputHash: "sha256:in"},
			},
			Artifacts: []ArtifactRow{{Path: "runs/r/artifacts/out.txt", SHA256: "sha256:x"}},
		}, root)
		if err != nil {
			t.Fatal(err)
		}
		features = append(features, f)
	}
	cands := RankCandidates(features, root, Thresholds{MinRepeats: 3, MinPassRate: 0.66, MinScoreP50: 0.8})
	if len(cands) != 1 {
		t.Fatalf("expected one candidate, got %d", len(cands))
	}
	if cands[0].Verdict != VerdictTriggered {
		t.Fatalf("expected trigger, got %s (%s)", cands[0].Verdict, cands[0].Reason)
	}
}

func TestRankCandidatesBelowPassRateDoesNotTrigger(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "dd")
	var features []RunFeature
	for i := 0; i < 3; i++ {
		pass := i == 0
		f, err := BuildFeature(BuildInput{
			RunID:   "r" + string('1'+rune(i)),
			Skill:   "dd",
			Score:   0.9,
			Pass:    pass,
			CostEst: 0.1,
			ToolCalls: []ToolCallRow{
				{Seq: 1, Tool: "shell.exec", InputHash: "sha256:in"},
			},
			Artifacts: []ArtifactRow{{Path: "runs/r/artifacts/out.txt", SHA256: "sha256:x"}},
		}, root)
		if err != nil {
			t.Fatal(err)
		}
		features = append(features, f)
	}
	cands := RankCandidates(features, root, Thresholds{MinRepeats: 3, MinPassRate: 0.8, MinScoreP50: 0.8})
	if len(cands) != 1 {
		t.Fatalf("expected one candidate, got %d", len(cands))
	}
	if cands[0].Verdict != VerdictLowPassRate {
		t.Fatalf("expected low pass-rate, got %s", cands[0].Verdict)
	}
}

func TestBuildSuggestionFilesProducesExpectedLayout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	c := Candidate{
		MotifKey:      strings.Repeat("a", 64),
		Skill:         "dd",
		ProposedSkill: "suggest-dd-aaaaaaaaaaaa",
	}
	files, err := BuildSuggestionFiles(root, c, "shell.exec", map[string]any{"cmd": "echo ok"})
	if err != nil {
		t.Fatal(err)
	}
	if files.SkillName != "suggest-dd-aaaaaaaaaaaa" {
		t.Fatalf("unexpected skill name: %s", files.SkillName)
	}
	for _, rel := range []string{
		filepath.Join(files.SkillDir, skillpkg.CanonicalSkillFile),
		filepath.Join(files.SkillDir, skillpkg.ToolsConfigFile),
		filepath.Join(files.SkillDir, skillpkg.RubricConfigFile),
		filepath.Join(files.SkillDir, "tests", "case01.json"),
	} {
		if _, ok := files.Files[rel]; !ok {
			t.Fatalf("missing scaffold file: %s", rel)
		}
	}
}

func TestBuildFeatureUsesProvidedFingerprintsOverWorkspaceHead(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "dd")
	in := BuildInput{
		RunID:                "r1",
		Skill:                "dd",
		Score:                0.9,
		Pass:                 true,
		CostEst:              0.1,
		ToolCalls:            []ToolCallRow{{Seq: 1, Tool: "shell.exec", InputHash: "sha256:in"}},
		Artifacts:            []ArtifactRow{{Path: "artifacts/out.txt", SHA256: "sha256:x"}},
		PromptFingerprint:    "sha256:prompt-from-run",
		SkillBaseFingerprint: "sha256:base-from-run",
	}
	a, err := BuildFeature(in, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dd", skillpkg.CanonicalSkillFile), []byte("---\nname: dd\ndescription: changed\nrequires:\n  bins: []\n  env: []\n  config: []\nos: [linux]\n---\n# Steps\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := BuildFeature(in, root)
	if err != nil {
		t.Fatal(err)
	}
	if a.MotifKey != b.MotifKey {
		t.Fatalf("motif key drifted despite run-provided fingerprints: %s != %s", a.MotifKey, b.MotifKey)
	}
}

func writeSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: x\nrequires:\n  bins: []\n  env: []\n  config: []\nos: [linux]\n---\n# Steps\nhello\n"
	if err := os.WriteFile(filepath.Join(dir, skillpkg.CanonicalSkillFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
