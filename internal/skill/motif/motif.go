package motif

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	skillpkg "github.com/haris/virmux/internal/skill"
	skillspec "github.com/haris/virmux/internal/skill/spec"
)

type RunFeature struct {
	RunID                string
	Skill                string
	Score                float64
	Pass                 bool
	CostEst              float64
	ToolSeqHash          string
	ArtifactSchemaHash   string
	PromptFingerprint    string
	SkillBaseFingerprint string
	MotifKey             string
	ToolNames            []string
}

type ToolCallRow struct {
	Seq       int64
	Tool      string
	InputHash string
}

type ArtifactRow struct {
	Path   string
	SHA256 string
}

type BuildInput struct {
	RunID                string
	Skill                string
	Score                float64
	Pass                 bool
	CostEst              float64
	ToolCalls            []ToolCallRow
	Artifacts            []ArtifactRow
	PromptFingerprint    string
	SkillBaseFingerprint string
}

type Thresholds struct {
	MinRepeats  int
	MinPassRate float64
	MinScoreP50 float64
}

type VerdictCode string

const (
	VerdictTriggered   VerdictCode = "TRIGGERED"
	VerdictLowRepeats  VerdictCode = "LOW_REPEATS"
	VerdictLowPassRate VerdictCode = "LOW_PASS_RATE"
	VerdictLowScore    VerdictCode = "LOW_SCORE"
	VerdictNotNovel    VerdictCode = "NOT_NOVEL"
)

type Candidate struct {
	MotifKey             string      `json:"motif_key"`
	Skill                string      `json:"skill"`
	Repeats              int         `json:"repeats"`
	Passes               int         `json:"passes"`
	PassRate             float64     `json:"pass_rate"`
	ScoreP50             float64     `json:"score_p50"`
	AvgCost              float64     `json:"avg_cost"`
	ExpectedReuseValue   float64     `json:"expected_reuse_value"`
	RunIDs               []string    `json:"run_ids"`
	ToolSeqHash          string      `json:"tool_seq_hash"`
	ArtifactSchemaHash   string      `json:"artifact_schema_hash"`
	PromptFingerprint    string      `json:"prompt_fingerprint"`
	SkillBaseFingerprint string      `json:"skill_base_fingerprint"`
	Verdict              VerdictCode `json:"verdict"`
	Reason               string      `json:"reason,omitempty"`
	ProposedSkill        string      `json:"proposed_skill,omitempty"`
}

type SuggestionFiles struct {
	SkillName  string
	SkillDir   string
	FixtureRel string
	Files      map[string][]byte
}

func BuildFeature(in BuildInput, skillsDir string) (RunFeature, error) {
	if strings.TrimSpace(in.RunID) == "" {
		return RunFeature{}, fmt.Errorf("run id required")
	}
	if strings.TrimSpace(in.Skill) == "" {
		return RunFeature{}, fmt.Errorf("skill required")
	}
	toolSeqHash, toolNames := calcToolSeqHash(in.ToolCalls)
	artifactHash := calcArtifactSchemaHash(in.Artifacts)
	promptFP := strings.TrimSpace(in.PromptFingerprint)
	baseFP := strings.TrimSpace(in.SkillBaseFingerprint)
	if promptFP == "" || baseFP == "" {
		promptFP, baseFP = loadSkillFingerprints(skillsDir, in.Skill)
	}
	key := calcMotifKey(toolSeqHash, artifactHash, promptFP, baseFP)
	return RunFeature{
		RunID:                in.RunID,
		Skill:                in.Skill,
		Score:                in.Score,
		Pass:                 in.Pass,
		CostEst:              in.CostEst,
		ToolSeqHash:          toolSeqHash,
		ArtifactSchemaHash:   artifactHash,
		PromptFingerprint:    promptFP,
		SkillBaseFingerprint: baseFP,
		MotifKey:             key,
		ToolNames:            toolNames,
	}, nil
}

func RankCandidates(features []RunFeature, skillsRoot string, th Thresholds) []Candidate {
	if th.MinRepeats <= 0 {
		th.MinRepeats = 3
	}
	if th.MinPassRate <= 0 {
		th.MinPassRate = 0.66
	}
	if th.MinScoreP50 <= 0 {
		th.MinScoreP50 = 0.8
	}

	type agg struct {
		key                  string
		skill                string
		runIDs               []string
		scores               []float64
		passes               int
		costs                []float64
		toolSeqHash          string
		artifactSchemaHash   string
		promptFingerprint    string
		skillBaseFingerprint string
	}
	grouped := map[string]*agg{}
	for _, f := range features {
		a, ok := grouped[f.MotifKey]
		if !ok {
			a = &agg{
				key:                  f.MotifKey,
				skill:                f.Skill,
				toolSeqHash:          f.ToolSeqHash,
				artifactSchemaHash:   f.ArtifactSchemaHash,
				promptFingerprint:    f.PromptFingerprint,
				skillBaseFingerprint: f.SkillBaseFingerprint,
			}
			grouped[f.MotifKey] = a
		}
		a.runIDs = append(a.runIDs, f.RunID)
		a.scores = append(a.scores, f.Score)
		a.costs = append(a.costs, f.CostEst)
		if f.Pass {
			a.passes++
		}
	}
	out := make([]Candidate, 0, len(grouped))
	for _, a := range grouped {
		sort.Strings(a.runIDs)
		scoreP50 := median(a.scores)
		passRate := 0.0
		if len(a.runIDs) > 0 {
			passRate = float64(a.passes) / float64(len(a.runIDs))
		}
		avgCost := average(a.costs)
		reuseValue := float64(len(a.runIDs)) * scoreP50 * (1.0 / (1.0 + avgCost))
		proposed := proposedSkillName(a.skill, a.key)
		c := Candidate{
			MotifKey:             a.key,
			Skill:                a.skill,
			Repeats:              len(a.runIDs),
			Passes:               a.passes,
			PassRate:             passRate,
			ScoreP50:             scoreP50,
			AvgCost:              avgCost,
			ExpectedReuseValue:   reuseValue,
			RunIDs:               a.runIDs,
			ToolSeqHash:          a.toolSeqHash,
			ArtifactSchemaHash:   a.artifactSchemaHash,
			PromptFingerprint:    a.promptFingerprint,
			SkillBaseFingerprint: a.skillBaseFingerprint,
			ProposedSkill:        proposed,
		}
		c.Verdict, c.Reason = triggerVerdict(c, skillsRoot, th)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Verdict != out[j].Verdict {
			return out[i].Verdict == VerdictTriggered
		}
		if out[i].ExpectedReuseValue == out[j].ExpectedReuseValue {
			return out[i].MotifKey < out[j].MotifKey
		}
		return out[i].ExpectedReuseValue > out[j].ExpectedReuseValue
	})
	return out
}

func BuildSuggestionFiles(skillsRoot string, c Candidate, exemplarTool string, exemplarArgs map[string]any) (SuggestionFiles, error) {
	name := strings.TrimSpace(c.ProposedSkill)
	if name == "" {
		return SuggestionFiles{}, fmt.Errorf("proposed skill required")
	}
	dir := filepath.Join(skillsRoot, name)
	if _, err := os.Stat(dir); err == nil {
		return SuggestionFiles{}, fmt.Errorf("skill already exists: %s", name)
	}
	if strings.TrimSpace(exemplarTool) == "" {
		exemplarTool = "shell.exec"
	}
	if exemplarArgs == nil {
		exemplarArgs = map[string]any{}
	}
	if exemplarTool == "shell.exec" {
		if _, ok := exemplarArgs["cmd"]; !ok {
			exemplarArgs["cmd"] = "echo ok"
		}
	}
	argsJSON, err := json.Marshal(exemplarArgs)
	if err != nil {
		return SuggestionFiles{}, fmt.Errorf("marshal exemplar args: %w", err)
	}

	skillDoc := strings.Join([]string{
		"---",
		"name: " + name,
		"description: scaffolded from repeated successful motif mining",
		"requires:",
		"  bins: []",
		"  env: []",
		"  config: []",
		"os: [linux]",
		"metadata:",
		"  generated_by: virmux-suggest-c5",
		"  source_skill: " + c.Skill,
		"  motif_key: " + c.MotifKey,
		"---",
		"# Steps",
		"",
		"Execute the motif-backed procedure and emit deterministic artifacts.",
		"",
	}, "\n")
	toolYAML := strings.Join([]string{
		"allowed_tools: [" + exemplarTool + "]",
		"budget:",
		"  tool_calls: 1",
		"  seconds: 20",
		"  tokens: 0",
		"",
	}, "\n")
	rubricYAML := strings.Join([]string{
		"criteria:",
		"  - id: format",
		"    w: 0.4",
		"    must: true",
		"  - id: completeness",
		"    w: 0.4",
		"  - id: actionability",
		"    w: 0.2",
		"pass: 0.8",
		"",
	}, "\n")
	fixtureJSON := fmt.Sprintf("{\"id\":\"case01\",\"tool\":%q,\"args\":%s,\"deterministic\":true}\n", exemplarTool, string(argsJSON))
	// Guard generated fixture schema before file write.
	var fixtureDoc map[string]any
	if err := json.Unmarshal([]byte(fixtureJSON), &fixtureDoc); err != nil {
		return SuggestionFiles{}, fmt.Errorf("generated fixture invalid: %w", err)
	}
	files := map[string][]byte{
		filepath.Join(dir, skillpkg.CanonicalSkillFile): []byte(skillDoc),
		filepath.Join(dir, skillpkg.ToolsConfigFile):    []byte(toolYAML),
		filepath.Join(dir, skillpkg.RubricConfigFile):   []byte(rubricYAML),
		filepath.Join(dir, "tests", "case01.json"):      []byte(fixtureJSON),
	}
	return SuggestionFiles{
		SkillName:  name,
		SkillDir:   dir,
		FixtureRel: filepath.ToSlash(filepath.Join("tests", "case01.json")),
		Files:      files,
	}, nil
}

func calcToolSeqHash(rows []ToolCallRow) (string, []string) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Seq == rows[j].Seq {
			if rows[i].Tool == rows[j].Tool {
				return rows[i].InputHash < rows[j].InputHash
			}
			return rows[i].Tool < rows[j].Tool
		}
		return rows[i].Seq < rows[j].Seq
	})
	lines := make([]string, 0, len(rows))
	seenTool := map[string]struct{}{}
	var tools []string
	for _, r := range rows {
		tool := strings.TrimSpace(r.Tool)
		in := strings.TrimSpace(r.InputHash)
		lines = append(lines, tool+"|"+in)
		if tool != "" {
			if _, ok := seenTool[tool]; !ok {
				seenTool[tool] = struct{}{}
				tools = append(tools, tool)
			}
		}
	}
	sort.Strings(tools)
	return hashLines(lines), tools
}

func calcArtifactSchemaHash(rows []ArtifactRow) string {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Path == rows[j].Path {
			return rows[i].SHA256 < rows[j].SHA256
		}
		return rows[i].Path < rows[j].Path
	})
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		p := filepath.ToSlash(strings.TrimSpace(r.Path))
		sha := strings.TrimSpace(r.SHA256)
		typ := "file"
		if strings.HasPrefix(sha, "meta:") {
			typ = sha
		}
		if p != "" {
			lines = append(lines, p+"|"+typ)
		}
	}
	return hashLines(lines)
}

func loadSkillFingerprints(skillsDir, skill string) (promptFingerprint, baseFingerprint string) {
	p := filepath.Join(skillsDir, skill, skillpkg.CanonicalSkillFile)
	b, err := os.ReadFile(p)
	if err != nil {
		fallback := hashLines([]string{"missing-skill", skill})
		return fallback, fallback
	}
	baseFingerprint = hashBytes(b)
	_, body, err := skillspec.SplitFrontmatter(b)
	if err != nil {
		return baseFingerprint, baseFingerprint
	}
	return hashLines([]string{strings.TrimSpace(body)}), baseFingerprint
}

func calcMotifKey(toolSeqHash, artifactSchemaHash, promptFingerprint, skillBaseFingerprint string) string {
	return hashLines([]string{toolSeqHash, artifactSchemaHash, promptFingerprint, skillBaseFingerprint})
}

func triggerVerdict(c Candidate, skillsRoot string, th Thresholds) (VerdictCode, string) {
	if c.Repeats < th.MinRepeats {
		return VerdictLowRepeats, fmt.Sprintf("repeats=%d < min=%d", c.Repeats, th.MinRepeats)
	}
	if c.PassRate < th.MinPassRate {
		return VerdictLowPassRate, fmt.Sprintf("pass_rate=%.4f < min=%.4f", c.PassRate, th.MinPassRate)
	}
	if c.ScoreP50 < th.MinScoreP50 {
		return VerdictLowScore, fmt.Sprintf("score_p50=%.4f < min=%.4f", c.ScoreP50, th.MinScoreP50)
	}
	if _, err := os.Stat(filepath.Join(skillsRoot, c.ProposedSkill)); err == nil {
		return VerdictNotNovel, "candidate skill already exists"
	}
	return VerdictTriggered, ""
}

func proposedSkillName(skill, motifKey string) string {
	prefix := strings.TrimSpace(skill)
	if prefix == "" {
		prefix = "skill"
	}
	prefix = sanitizeKebab(prefix)
	short := motifKey
	if len(short) > 12 {
		short = short[:12]
	}
	return sanitizeKebab("suggest-" + prefix + "-" + short)
}

func sanitizeKebab(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "x"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			r = '-'
		}
		if r == '-' {
			if lastDash {
				continue
			}
			lastDash = true
		} else {
			lastDash = false
		}
		b.WriteRune(r)
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "x"
	}
	return out
}

func hashLines(lines []string) string {
	sum := sha256.New()
	for _, ln := range lines {
		sum.Write([]byte(ln))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func hashBytes(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	cp := append([]float64(nil), v...)
	sort.Float64s(cp)
	m := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[m]
	}
	return (cp[m-1] + cp[m]) / 2
}

func average(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var sum float64
	for _, x := range v {
		sum += x
	}
	return sum / float64(len(v))
}
