package eval

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	skillpkg "github.com/haris/virmux/internal/skill"
	skillspec "github.com/haris/virmux/internal/skill/spec"
)

// Exec runs host commands (git/promptfoo); injected for tests.
type Exec interface {
	Run(context.Context, skillpkg.Command) (skillpkg.CommandResult, error)
}

// OSExec is the default command runner.
type OSExec struct{}

func (OSExec) Run(ctx context.Context, c skillpkg.Command) (skillpkg.CommandResult, error) {
	if strings.TrimSpace(c.Name) == "" {
		return skillpkg.CommandResult{}, errors.New("empty command")
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	if strings.TrimSpace(c.Dir) != "" {
		cmd.Dir = c.Dir
	}
	if len(c.Env) > 0 {
		cmd.Env = append([]string{}, c.Env...)
	}
	stdout, err := cmd.Output()
	ended := time.Now().UTC()
	if err == nil {
		return skillpkg.CommandResult{ExitCode: 0, Stdout: stdout, EndedAt: ended}, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return skillpkg.CommandResult{ExitCode: ee.ExitCode(), Stderr: ee.Stderr, EndedAt: ended}, fmt.Errorf("command failed: %s %s: %w", c.Name, strings.Join(c.Args, " "), err)
	}
	return skillpkg.CommandResult{ExitCode: -1, EndedAt: ended}, err
}

// SkillSnapshot is SKILL body + fixtures frozen at one git ref.
type SkillSnapshot struct {
	Ref      string
	Body     string
	Fixtures []Fixture
}

// Fixture is one canonical test case.
type Fixture struct {
	ID   string
	Path string
	Raw  json.RawMessage
}

// Metrics are aggregated promptfoo results.
type Metrics struct {
	Cases     int     `json:"cases"`
	Passes    int     `json:"passes"`
	Fails     int     `json:"fails"`
	FailRate  float64 `json:"fail_rate"`
	ScoreP50  float64 `json:"score_p50"`
	ScoreP90  float64 `json:"score_p90"`
	CostTotal float64 `json:"cost_total"`
}

// CaseMetric keeps per-case side-by-side data for AB storage.
type CaseMetric struct {
	FixtureID string  `json:"fixture_id"`
	Score     float64 `json:"score"`
	Pass      bool    `json:"pass"`
	Cost      float64 `json:"cost"`
}

// ABThresholds define regression gates.
type ABThresholds struct {
	MinScoreDelta    float64
	MaxFailRateDelta float64
	MaxCostDelta     *float64
}

// ABVerdict is typed gate output persisted in sqlite/artifacts.
type ABVerdict struct {
	Pass          bool    `json:"pass"`
	Reason        string  `json:"reason"`
	ScoreDelta    float64 `json:"score_p50_delta"`
	FailRateDelta float64 `json:"fail_rate_delta"`
	CostDelta     float64 `json:"cost_delta"`
}

func LoadSkillSnapshot(ctx context.Context, ex Exec, repoDir, skillsDir, skill, ref string) (SkillSnapshot, error) {
	if ex == nil {
		ex = OSExec{}
	}
	base := filepath.ToSlash(filepath.Join(skillsDir, skill))
	skillPath := filepath.ToSlash(filepath.Join(base, skillpkg.CanonicalSkillFile))
	skillBytes, err := gitShow(ctx, ex, repoDir, ref, skillPath)
	if err != nil {
		return SkillSnapshot{}, err
	}
	_, body, err := skillspec.SplitFrontmatter(skillBytes)
	if err != nil {
		return SkillSnapshot{}, fmt.Errorf("parse %s@%s frontmatter: %w", skillPath, ref, err)
	}
	testsPrefix := filepath.ToSlash(filepath.Join(base, "tests"))
	paths, err := gitListJSONFiles(ctx, ex, repoDir, ref, testsPrefix)
	if err != nil {
		return SkillSnapshot{}, err
	}
	if len(paths) == 0 {
		return SkillSnapshot{}, fmt.Errorf("no fixtures at %s@%s", testsPrefix, ref)
	}
	fixtures := make([]Fixture, 0, len(paths))
	for _, p := range paths {
		raw, err := gitShow(ctx, ex, repoDir, ref, p)
		if err != nil {
			return SkillSnapshot{}, err
		}
		id, err := fixtureID(raw, p)
		if err != nil {
			return SkillSnapshot{}, err
		}
		fixtures = append(fixtures, Fixture{ID: id, Path: p, Raw: json.RawMessage(raw)})
	}
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].ID < fixtures[j].ID })
	return SkillSnapshot{Ref: ref, Body: body, Fixtures: fixtures}, nil
}

func BuildPromptfooConfig(snapshot SkillSnapshot, provider string) ([]byte, error) {
	if strings.TrimSpace(provider) == "" {
		return nil, errors.New("provider required")
	}
	if len(snapshot.Fixtures) == 0 {
		return nil, errors.New("fixtures required")
	}
	tests := make([]map[string]any, 0, len(snapshot.Fixtures))
	for _, fx := range snapshot.Fixtures {
		tests = append(tests, map[string]any{
			"vars": map[string]any{
				"fixture_id":   fx.ID,
				"fixture_json": string(fx.Raw),
			},
			"metadata": map[string]any{
				"fixture_id": fx.ID,
				"fixture":    fx.Path,
			},
		})
	}
	cfg := map[string]any{
		"description": fmt.Sprintf("virmux skill eval ref=%s", snapshot.Ref),
		"prompts":     []string{snapshot.Body},
		"providers":   []string{provider},
		"tests":       tests,
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func RunPromptfoo(ctx context.Context, ex Exec, repoDir, promptfooBin, cfgPath, outPath string, timeout time.Duration) error {
	if ex == nil {
		ex = OSExec{}
	}
	if strings.TrimSpace(promptfooBin) == "" {
		promptfooBin = "promptfoo"
	}

	// Try promptfooBin as is (e.g. from flag or default "promptfoo" in PATH)
	_, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: promptfooBin, Args: []string{"validate", "-c", cfgPath}, Timeout: timeout})
	if err == nil {
		_, err = ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: promptfooBin, Args: []string{"eval", "-c", cfgPath, "--output", outPath}, Timeout: timeout})
		if err != nil {
			return fmt.Errorf("promptfoo eval failed: %w", err)
		}
		return nil
	}

	// If failed, try local node_modules/.bin/promptfoo
	localPF := filepath.Join(repoDir, "node_modules", ".bin", "promptfoo")
	if _, serr := os.Stat(localPF); serr == nil {
		if _, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: localPF, Args: []string{"validate", "-c", cfgPath}, Timeout: timeout}); err == nil {
			if _, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: localPF, Args: []string{"eval", "-c", cfgPath, "--output", outPath}, Timeout: timeout}); err == nil {
				return nil
			}
		}
	}

	// Fallback to npx promptfoo@0.118.0
	if _, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: "npx", Args: []string{"promptfoo@0.118.0", "validate", "-c", cfgPath}, Timeout: timeout}); err != nil {
		return fmt.Errorf("npx promptfoo validate failed: %w", err)
	}
	if _, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: "npx", Args: []string{"promptfoo@0.118.0", "eval", "-c", cfgPath, "--output", outPath}, Timeout: timeout}); err != nil {
		return fmt.Errorf("npx promptfoo eval failed: %w", err)
	}
	return nil
}

func ParsePromptfooResults(raw []byte, fixtureIDs []string) (Metrics, map[string]CaseMetric, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return Metrics{}, nil, fmt.Errorf("parse promptfoo json: %w", err)
	}
	arr, _ := top["results"].([]any)
	if len(arr) == 0 {
		return Metrics{}, nil, errors.New("promptfoo results missing/empty")
	}
	caseMap := map[string]CaseMetric{}
	for i, row := range arr {
		rm, _ := row.(map[string]any)
		if rm == nil {
			continue
		}
		id := caseIDFromResult(rm)
		if id == "" && i < len(fixtureIDs) {
			id = fixtureIDs[i]
		}
		if id == "" {
			id = fmt.Sprintf("case-%03d", i+1)
		}
		caseMap[id] = CaseMetric{
			FixtureID: id,
			Score:     floatFromAny(firstNonNil(lookup(rm, "score"), lookup(rm, "grading", "score"), lookup(rm, "metrics", "score"))),
			Pass:      boolFromAny(firstNonNil(lookup(rm, "pass"), lookup(rm, "success"))),
			Cost:      floatFromAny(firstNonNil(lookup(rm, "cost"), lookup(rm, "metrics", "cost"), lookup(rm, "tokenUsage", "total"))),
		}
	}
	for _, id := range fixtureIDs {
		if _, ok := caseMap[id]; !ok {
			caseMap[id] = CaseMetric{FixtureID: id}
		}
	}
	orderedIDs := make([]string, 0, len(caseMap))
	for id := range caseMap {
		orderedIDs = append(orderedIDs, id)
	}
	sort.Strings(orderedIDs)
	scores := make([]float64, 0, len(orderedIDs))
	var passes int
	var cost float64
	for _, id := range orderedIDs {
		cm := caseMap[id]
		scores = append(scores, cm.Score)
		if cm.Pass {
			passes++
		}
		cost += cm.Cost
	}
	total := len(orderedIDs)
	fails := total - passes
	failRate := 0.0
	if total > 0 {
		failRate = float64(fails) / float64(total)
	}
	return Metrics{
		Cases:     total,
		Passes:    passes,
		Fails:     fails,
		FailRate:  failRate,
		ScoreP50:  median(scores),
		ScoreP90:  percentile(scores, 90),
		CostTotal: cost,
	}, caseMap, nil
}

func CompareAB(base, head Metrics, th ABThresholds) ABVerdict {
	v := ABVerdict{
		Pass:          true,
		Reason:        "ok",
		ScoreDelta:    head.ScoreP50 - base.ScoreP50,
		FailRateDelta: head.FailRate - base.FailRate,
		CostDelta:     head.CostTotal - base.CostTotal,
	}
	if v.ScoreDelta < th.MinScoreDelta {
		v.Pass = false
		v.Reason = fmt.Sprintf("score_p50_delta %.4f < min %.4f", v.ScoreDelta, th.MinScoreDelta)
		return v
	}
	if v.FailRateDelta > th.MaxFailRateDelta {
		v.Pass = false
		v.Reason = fmt.Sprintf("fail_rate_delta %.4f > max %.4f", v.FailRateDelta, th.MaxFailRateDelta)
		return v
	}
	if th.MaxCostDelta != nil && v.CostDelta > *th.MaxCostDelta {
		v.Pass = false
		v.Reason = fmt.Sprintf("cost_delta %.4f > max %.4f", v.CostDelta, *th.MaxCostDelta)
		return v
	}
	return v
}

func FixtureSetHash(fixtures []Fixture) string {
	h := sha256.New()
	cp := append([]Fixture(nil), fixtures...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].ID < cp[j].ID })
	for _, fx := range cp {
		h.Write([]byte(fx.ID))
		h.Write([]byte{0})
		h.Write([]byte(fx.Path))
		h.Write([]byte{0})
		h.Write(fx.Raw)
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func FixturesByID(fixtures []Fixture) map[string]Fixture {
	out := make(map[string]Fixture, len(fixtures))
	for _, fx := range fixtures {
		out[fx.ID] = fx
	}
	return out
}

func ValidateFrozenFixtureSet(head, base []Fixture) error {
	headSet := FixturesByID(head)
	baseSet := FixturesByID(base)
	for id := range headSet {
		if _, ok := baseSet[id]; !ok {
			return fmt.Errorf("base ref missing fixture id=%s", id)
		}
	}
	return nil
}

func gitShow(ctx context.Context, ex Exec, repoDir, ref, path string) ([]byte, error) {
	res, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: "git", Args: []string{"show", fmt.Sprintf("%s:%s", ref, path)}})
	if err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w", ref, path, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("git show %s:%s exit=%d", ref, path, res.ExitCode)
	}
	return res.Stdout, nil
}

func gitListJSONFiles(ctx context.Context, ex Exec, repoDir, ref, prefix string) ([]string, error) {
	res, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: "git", Args: []string{"ls-tree", "-r", "--name-only", ref, "--", prefix}})
	if err != nil {
		return nil, fmt.Errorf("git ls-tree %s %s: %w", ref, prefix, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("git ls-tree exit=%d", res.ExitCode)
	}
	lines := strings.Split(string(res.Stdout), "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || !strings.HasSuffix(ln, ".json") {
			continue
		}
		out = append(out, filepath.ToSlash(ln))
	}
	sort.Strings(out)
	return out, nil
}

func fixtureID(raw []byte, path string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("parse fixture %s: %w", path, err)
	}
	if id, _ := m["id"].(string); strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id), nil
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("fixture missing id: %s", path)
	}
	return base, nil
}

func caseIDFromResult(m map[string]any) string {
	for _, path := range [][]string{{"metadata", "fixture_id"}, {"vars", "fixture_id"}, {"testCase", "vars", "fixture_id"}, {"testCase", "metadata", "fixture_id"}} {
		if s, _ := lookup(m, path...).(string); strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func lookup(m map[string]any, path ...string) any {
	cur := any(m)
	for _, p := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = next[p]
	}
	return cur
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

func boolFromAny(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(strings.TrimSpace(x))
		return b
	case float64:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	default:
		return false
	}
}

func floatFromAny(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	default:
		return 0
	}
}

func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	if p <= 0 {
		return cp[0]
	}
	if p >= 100 {
		return cp[len(cp)-1]
	}
	idx := p / 100.0 * float64(len(cp)-1)
	i := int(idx)
	fraction := idx - float64(i)
	if i+1 < len(cp) {
		return cp[i] + fraction*(cp[i+1]-cp[i])
	}
	return cp[i]
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}
