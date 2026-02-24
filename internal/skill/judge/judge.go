package judge

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/haris/virmux/internal/trace"
	yaml "gopkg.in/yaml.v2"
)

const engineVersion = "judge.v1"

var defaultCriterionIDs = []string{"actionability", "completeness", "format"}

type Criterion struct {
	ID        string  `json:"id"`
	W         float64 `json:"w"`
	Must      bool    `json:"must,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
}

type Rubric struct {
	Criteria             []Criterion `json:"criteria"`
	Pass                 float64     `json:"pass"`
	OverrideDefaultCheck bool        `json:"override_default_check,omitempty"`
}

type Evidence struct {
	RunID       string   `json:"run_id"`
	Skill       string   `json:"skill"`
	RunDir      string   `json:"run_dir"`
	RunStatus   string   `json:"run_status"`
	ToolCalls   int      `json:"tool_calls"`
	ExpectFiles []string `json:"expect_files,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	ModelID     string   `json:"model_id,omitempty"`
}

type CriterionScore struct {
	ID     string  `json:"id"`
	Weight float64 `json:"w"`
	Value  float64 `json:"value"`
	Pass   bool    `json:"pass"`
	Must   bool    `json:"must,omitempty"`
	Reason string  `json:"reason,omitempty"`
}

type Result struct {
	RunID        string           `json:"run_id"`
	Skill        string           `json:"skill"`
	Score        float64          `json:"score"`
	Pass         bool             `json:"pass"`
	Critique     []string         `json:"critique"`
	Criterion    []CriterionScore `json:"criterion"`
	RubricHash   string           `json:"rubric_hash"`
	JudgeCfgHash string           `json:"judge_cfg_hash"`
	ArtifactHash string           `json:"artifact_hash"`
	ModelID      string           `json:"model_id,omitempty"`
	PromptHash   string           `json:"prompt_hash,omitempty"`
	SchemaVer    string           `json:"schema_ver,omitempty"`
	Mode         string           `json:"mode,omitempty"`
}

func LoadRubric(path string) (Rubric, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Rubric{}, "", fmt.Errorf("read rubric: %w", err)
	}
	var raw any
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return Rubric{}, "", fmt.Errorf("parse rubric yaml: %w", err)
	}
	m, ok := normalizeYAML(raw).(map[string]any)
	if !ok {
		return Rubric{}, "", errors.New("rubric.yaml must be mapping")
	}
	for k := range m {
		switch k {
		case "criteria", "pass", "override_default_check":
		default:
			return Rubric{}, "", fmt.Errorf("unknown rubric.yaml key: %s", k)
		}
	}
	criteriaRaw, ok := m["criteria"]
	if !ok {
		return Rubric{}, "", errors.New("rubric.yaml criteria required")
	}
	criteriaList, ok := criteriaRaw.([]any)
	if !ok || len(criteriaList) == 0 {
		return Rubric{}, "", errors.New("rubric.yaml criteria must be non-empty list")
	}
	r := Rubric{Pass: float64FromAny(m["pass"])}
	if r.Pass <= 0 || r.Pass > 1 {
		return Rubric{}, "", errors.New("rubric.yaml pass must be in (0,1]")
	}
	if v, ok := m["override_default_check"]; ok {
		bv, ok := v.(bool)
		if !ok {
			return Rubric{}, "", errors.New("rubric.yaml override_default_check must be bool")
		}
		r.OverrideDefaultCheck = bv
	}
	seen := map[string]struct{}{}
	sumW := 0.0
	for _, entry := range criteriaList {
		cm, ok := entry.(map[string]any)
		if !ok {
			return Rubric{}, "", errors.New("rubric.yaml criteria entries must be mappings")
		}
		for k := range cm {
			switch k {
			case "id", "w", "must", "threshold":
			default:
				return Rubric{}, "", fmt.Errorf("unknown rubric criterion key: %s", k)
			}
		}
		id := strings.TrimSpace(stringFromAny(cm["id"]))
		if id == "" {
			return Rubric{}, "", errors.New("rubric criterion id required")
		}
		if _, dup := seen[id]; dup {
			return Rubric{}, "", fmt.Errorf("duplicate rubric criterion id: %s", id)
		}
		seen[id] = struct{}{}
		w := float64FromAny(cm["w"])
		if w <= 0 || w > 1 {
			return Rubric{}, "", fmt.Errorf("rubric criterion %s weight must be in (0,1]", id)
		}
		sumW += w
		c := Criterion{ID: id, W: w, Threshold: 0.5}
		if v, ok := cm["must"]; ok {
			bv, ok := v.(bool)
			if !ok {
				return Rubric{}, "", fmt.Errorf("rubric criterion %s must must be bool", id)
			}
			c.Must = bv
		}
		if v, ok := cm["threshold"]; ok {
			c.Threshold = float64FromAny(v)
		}
		if c.Threshold < 0 || c.Threshold > 1 {
			return Rubric{}, "", fmt.Errorf("rubric criterion %s threshold must be in [0,1]", id)
		}
		r.Criteria = append(r.Criteria, c)
	}
	if sumW > 1.0000001 {
		return Rubric{}, "", fmt.Errorf("rubric criterion weight sum exceeds 1: %.6f", sumW)
	}
	if !r.OverrideDefaultCheck {
		for _, id := range defaultCriterionIDs {
			if _, ok := seen[id]; !ok {
				return Rubric{}, "", fmt.Errorf("rubric missing default criterion: %s", id)
			}
		}
	}
	j, _ := json.Marshal(canonicalRubric(r))
	return r, trace.SHA256Hex(j), nil
}

func Evaluate(r Rubric, rubricHash string, ev Evidence) (Result, error) {
	metric, artHash, reasons, err := collectMetrics(ev)
	if err != nil {
		return Result{}, err
	}
	judgeCfg := map[string]any{
		"engine":   engineVersion,
		"rubric":   canonicalRubric(r),
		"mode":     ev.Mode,
		"model_id": ev.ModelID,
	}
	cfgBytes, _ := json.Marshal(judgeCfg)
	res := Result{
		RunID:        ev.RunID,
		Skill:        ev.Skill,
		Critique:     append([]string(nil), reasons...),
		RubricHash:   rubricHash,
		JudgeCfgHash: trace.SHA256Hex(cfgBytes),
		ArtifactHash: artHash,
		Mode:         ev.Mode,
		ModelID:      ev.ModelID,
	}
	mustOK := true
	for _, c := range r.Criteria {
		v := metric[c.ID]
		ok := v >= c.Threshold
		if c.Must && !ok {
			mustOK = false
		}
		cs := CriterionScore{ID: c.ID, Weight: c.W, Value: v, Pass: ok, Must: c.Must}
		if !ok {
			cs.Reason = fmt.Sprintf("%s below threshold %.2f", c.ID, c.Threshold)
			res.Critique = append(res.Critique, cs.Reason)
		}
		res.Criterion = append(res.Criterion, cs)
		res.Score += c.W * v
	}
	res.Pass = res.Score >= r.Pass && mustOK
	sort.Strings(res.Critique)
	res.Critique = dedupe(res.Critique)
	return res, nil
}

func canonicalRubric(r Rubric) Rubric {
	out := Rubric{
		Pass:                 r.Pass,
		OverrideDefaultCheck: r.OverrideDefaultCheck,
		Criteria:             append([]Criterion(nil), r.Criteria...),
	}
	sort.Slice(out.Criteria, func(i, j int) bool { return out.Criteria[i].ID < out.Criteria[j].ID })
	return out
}

func collectMetrics(ev Evidence) (map[string]float64, string, []string, error) {
	metrics := map[string]float64{
		"format":        0,
		"completeness":  0,
		"actionability": 0,
	}
	var critique []string
	tracePath := filepath.Join(ev.RunDir, "trace.ndjson")
	tf, err := os.Open(tracePath)
	if err != nil {
		return nil, "", nil, fmt.Errorf("open trace for judge: %w", err)
	}
	defer tf.Close()
	validLines := 0
	toolLines := 0
	sc := bufio.NewScanner(tf)
	for sc.Scan() {
		validLines++
		if err := trace.ValidateLine(sc.Bytes()); err != nil {
			return nil, "", nil, fmt.Errorf("invalid trace line for judge: %w", err)
		}
		var ent trace.Entry
		if err := json.Unmarshal(sc.Bytes(), &ent); err != nil {
			return nil, "", nil, fmt.Errorf("parse trace line for judge: %w", err)
		}
		if ent.Type == "tool" {
			toolLines++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, "", nil, fmt.Errorf("scan trace for judge: %w", err)
	}
	if validLines > 0 && strings.EqualFold(strings.TrimSpace(ev.RunStatus), "ok") {
		metrics["format"] = 1
	}

	artifactCount, actionableCount, artHash, err := walkEvidenceFiles(ev.RunDir)
	if err != nil {
		return nil, "", nil, err
	}
	if artifactCount > 0 && toolLines > 0 {
		metrics["completeness"] = 1
	}
	if len(ev.ExpectFiles) > 0 {
		matched := 0
		for _, f := range ev.ExpectFiles {
			if expectedFileExists(ev.RunDir, f) {
				matched++
			}
		}
		ratio := float64(matched) / float64(len(ev.ExpectFiles))
		if ratio > 0 {
			metrics["completeness"] = (metrics["completeness"] + ratio) / 2
		}
		if matched == 0 {
			critique = append(critique, "expected files not found under run dir/artifacts")
		}
	}
	if actionableCount > 0 && strings.EqualFold(strings.TrimSpace(ev.RunStatus), "ok") {
		metrics["actionability"] = 1
	}
	if artifactCount == 0 {
		critique = append(critique, "empty artifact set")
	}
	return metrics, artHash, critique, nil
}

func walkEvidenceFiles(runDir string) (artifactCount int, actionableCount int, artHash string, err error) {
	var rels []string
	for _, root := range []string{"artifacts", "toolio"} {
		base := filepath.Join(runDir, root)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		if err := filepath.Walk(base, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			artifactCount++
			rel, relErr := filepath.Rel(runDir, path)
			if relErr != nil {
				return relErr
			}
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			trim := strings.TrimSpace(string(b))
			if trim != "" {
				actionableCount++
			}
			rels = append(rels, fmt.Sprintf("%s:%s", filepath.ToSlash(rel), trace.SHA256Hex(b)))
			return nil
		}); err != nil {
			return 0, 0, "", fmt.Errorf("walk %s: %w", base, err)
		}
	}
	sort.Strings(rels)
	hInput := strings.Join(rels, "\n")
	return artifactCount, actionableCount, trace.SHA256Hex([]byte(hInput)), nil
}

func expectedFileExists(runDir, name string) bool {
	name = filepath.ToSlash(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	cands := []string{
		filepath.Join(runDir, filepath.FromSlash(name)),
		filepath.Join(runDir, "artifacts", filepath.Base(name)),
	}
	for _, p := range cands {
		info, err := os.Stat(p)
		if err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func normalizeYAML(v any) any {
	switch x := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			m[fmt.Sprint(k)] = normalizeYAML(val)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalizeYAML(x[i])
		}
		return out
	default:
		return v
	}
}

func float64FromAny(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	default:
		return 0
	}
}

func stringFromAny(v any) string {
	s, _ := v.(string)
	return s
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:0]
	var prev string
	for i, s := range in {
		if i == 0 || s != prev {
			out = append(out, s)
			prev = s
		}
	}
	return out
}
