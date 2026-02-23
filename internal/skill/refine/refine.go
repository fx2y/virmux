package refine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	skillpkg "github.com/haris/virmux/internal/skill"
	skillrun "github.com/haris/virmux/internal/skill/run"
	skillspec "github.com/haris/virmux/internal/skill/spec"
	yaml "gopkg.in/yaml.v2"
)

const (
	refineStartMarker = "<!-- virmux-refine:start -->"
	refineEndMarker   = "<!-- virmux-refine:end -->"
)

type Criterion struct {
	ID     string  `json:"id"`
	Value  float64 `json:"value"`
	Weight float64 `json:"weight"`
}

type Score struct {
	Score        float64     `json:"score"`
	Pass         bool        `json:"pass"`
	Critique     []string    `json:"critique"`
	Criterion    []Criterion `json:"criterion"`
	RubricHash   string      `json:"rubric_hash"`
	JudgeCfgHash string      `json:"judge_cfg_hash"`
}

type Suggestion struct {
	Path      string `json:"path"`
	Content   []byte `json:"-"`
	Rationale string `json:"rationale"`
}

// LoadScore reads C2/C3 score artifact as deterministic refine input.
func LoadScore(path string) (Score, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Score{}, fmt.Errorf("read score file %s: %w", path, err)
	}
	var s Score
	if err := json.Unmarshal(b, &s); err != nil {
		return Score{}, fmt.Errorf("parse score file %s: %w", path, err)
	}
	return s, nil
}

// ValidateFixtures enforces C4 pre-commit fixture parse gate.
func ValidateFixtures(skillDir string) error {
	files, err := filepath.Glob(filepath.Join(skillDir, "tests", "*.json"))
	if err != nil {
		return fmt.Errorf("glob fixtures: %w", err)
	}
	if len(files) == 0 {
		return errors.New("no fixtures found under tests/*.json")
	}
	sort.Strings(files)
	for _, p := range files {
		if _, err := skillrun.LoadFixture(p); err != nil {
			return fmt.Errorf("fixture %s: %w", p, err)
		}
	}
	return nil
}

// BuildSuggestions generates a tiny deterministic patch set (default SKILL.md only).
func BuildSuggestions(skillDir, runID string, score Score, allowToolsEdit bool) ([]Suggestion, error) {
	_ = allowToolsEdit // C4 default path keeps tools.yaml immutable.
	skillPath := filepath.Join(skillDir, skillpkg.CanonicalSkillFile)
	b, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", skillPath, err)
	}
	fm, body, err := skillspec.SplitFrontmatter(b)
	if err != nil {
		return nil, fmt.Errorf("parse frontmatter %s: %w", skillPath, err)
	}
	focus := weakestCriterion(score.Criterion)
	if focus == "" {
		focus = "completeness"
	}
	line := fmt.Sprintf("- run `%s`: tighten `%s` by adding one explicit acceptance bullet and one concrete output check.", runID, focus)
	nextBody := replaceRefineBlock(body, line)
	next, err := composeFrontmatter(fm, nextBody)
	if err != nil {
		return nil, fmt.Errorf("compose %s: %w", skillPath, err)
	}
	if string(next) == string(b) {
		return nil, errors.New("refine generated empty patch; adjust score input or skill body")
	}
	rationale := fmt.Sprintf("focus=%s score=%.4f pass=%t", focus, score.Score, score.Pass)
	return []Suggestion{{Path: skillPath, Content: next, Rationale: rationale}}, nil
}

func weakestCriterion(cs []Criterion) string {
	if len(cs) == 0 {
		return ""
	}
	best := strings.TrimSpace(cs[0].ID)
	minv := cs[0].Value
	for _, c := range cs[1:] {
		id := strings.TrimSpace(c.ID)
		if id == "" {
			continue
		}
		if c.Value < minv {
			minv = c.Value
			best = id
		}
	}
	return best
}

func replaceRefineBlock(body, line string) string {
	b := strings.TrimSpace(body)
	block := strings.Join([]string{
		refineStartMarker,
		"## Refinement Notes",
		line,
		refineEndMarker,
	}, "\n")
	if i := strings.Index(b, refineStartMarker); i >= 0 {
		if j := strings.Index(b[i:], refineEndMarker); j >= 0 {
			j = i + j + len(refineEndMarker)
			replaced := strings.TrimSpace(b[:i])
			if replaced == "" {
				return block + "\n"
			}
			return replaced + "\n\n" + block + "\n"
		}
	}
	if b == "" {
		return block + "\n"
	}
	return b + "\n\n" + block + "\n"
}

func composeFrontmatter(meta map[string]any, body string) ([]byte, error) {
	fm, err := yaml.Marshal(meta)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	if strings.TrimSpace(body) != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return []byte(b.String()), nil
}
