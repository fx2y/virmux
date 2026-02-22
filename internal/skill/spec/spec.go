package spec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	skillpkg "github.com/haris/virmux/internal/skill"
	yaml "gopkg.in/yaml.v2"
)

var kebabNameRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Requires struct {
	Bins   []string
	Env    []string
	Config []string
}

type Frontmatter struct {
	Name        string
	Description string
	Requires    Requires
	OS          []string
	Metadata    map[string]any
}

type Budget struct {
	ToolCalls int `json:"tool_calls"`
	Seconds   int `json:"seconds"`
	Tokens    int `json:"tokens"`
}

type ToolsConfig struct {
	AllowedTools []string `json:"allowed_tools"`
	Budget       Budget   `json:"budget"`
}

type Skill struct {
	Dir     string
	Path    string
	Body    string
	Meta    Frontmatter
	Tools   ToolsConfig
	Dormant bool
	Reasons []string
}

type LintResult struct {
	Dir      string   `json:"dir"`
	Name     string   `json:"name,omitempty"`
	Dormant  bool     `json:"dormant"`
	Reasons  []string `json:"reasons,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type EligibilityEnv struct {
	GOOS      string
	LookupEnv func(string) (string, bool)
	LookPath  func(string) error
	Config    map[string]string
}

func DefaultEligibilityEnv() EligibilityEnv {
	return EligibilityEnv{
		GOOS:      runtime.GOOS,
		LookupEnv: os.LookupEnv,
		LookPath: func(s string) error {
			_, err := exec.LookPath(s)
			return err
		},
		Config: map[string]string{},
	}
}

func SplitFrontmatter(src []byte) (map[string]any, string, error) {
	s := string(src)
	if !strings.HasPrefix(s, "---\n") {
		return nil, "", errors.New("missing frontmatter start sentinel ---")
	}
	rest := s[len("---\n"):]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return nil, "", errors.New("missing frontmatter end sentinel ---")
	}
	fmRaw := rest[:idx]
	body := rest[idx+len("\n---\n"):]
	var raw any
	if err := yaml.Unmarshal([]byte(fmRaw), &raw); err != nil {
		return nil, "", fmt.Errorf("parse frontmatter yaml: %w", err)
	}
	obj, ok := normalizeYAML(raw).(map[string]any)
	if !ok {
		return nil, "", errors.New("frontmatter must be mapping")
	}
	return obj, body, nil
}

func LoadDir(dir string, ee EligibilityEnv) (Skill, error) {
	if ee.LookupEnv == nil || ee.LookPath == nil {
		def := DefaultEligibilityEnv()
		if ee.LookupEnv == nil {
			ee.LookupEnv = def.LookupEnv
		}
		if ee.LookPath == nil {
			ee.LookPath = def.LookPath
		}
		if ee.GOOS == "" {
			ee.GOOS = def.GOOS
		}
		if ee.Config == nil {
			ee.Config = def.Config
		}
	}
	skillPath := filepath.Join(dir, skillpkg.CanonicalSkillFile)
	b, err := os.ReadFile(skillPath)
	if err != nil {
		return Skill{}, fmt.Errorf("read %s: %w", skillPath, err)
	}
	metaMap, body, err := SplitFrontmatter(b)
	if err != nil {
		return Skill{}, err
	}
	meta, err := parseFrontmatter(metaMap)
	if err != nil {
		return Skill{}, err
	}
	tools, err := LoadTools(filepath.Join(dir, skillpkg.ToolsConfigFile))
	if err != nil {
		return Skill{}, err
	}
	reasons := CheckEligibility(meta, ee)
	return Skill{
		Dir: dir, Path: skillPath, Body: body, Meta: meta, Tools: tools,
		Dormant: len(reasons) > 0, Reasons: reasons,
	}, nil
}

func LintDirs(dirs []string, ee EligibilityEnv) ([]LintResult, error) {
	uniq := map[string]struct{}{}
	for _, d := range dirs {
		if strings.TrimSpace(d) == "" {
			continue
		}
		info, err := os.Stat(d)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", d, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("expected skill dir, got file: %s", d)
		}
		uniq[d] = struct{}{}
	}
	sorted := make([]string, 0, len(uniq))
	for d := range uniq {
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)
	out := make([]LintResult, 0, len(sorted))
	for _, d := range sorted {
		s, err := LoadDir(d, ee)
		if err != nil {
			return nil, fmt.Errorf("lint %s: %w", d, err)
		}
		lr := LintResult{Dir: d, Name: s.Meta.Name, Dormant: s.Dormant}
		if s.Dormant {
			lr.Reasons = append([]string(nil), s.Reasons...)
		}
		if _, err := os.Stat(filepath.Join(d, skillpkg.RubricConfigFile)); err != nil {
			return nil, fmt.Errorf("lint %s: missing %s", d, skillpkg.RubricConfigFile)
		}
		testsPath := filepath.Join(d, "tests")
		if info, err := os.Stat(testsPath); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("lint %s: missing tests/ dir", d)
		}
		out = append(out, lr)
	}
	return out, nil
}

func LoadTools(path string) (ToolsConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ToolsConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var raw any
	if err := yaml.Unmarshal([]byte(b), &raw); err != nil {
		return ToolsConfig{}, fmt.Errorf("parse tools yaml: %w", err)
	}
	m, ok := normalizeYAML(raw).(map[string]any)
	if !ok {
		return ToolsConfig{}, errors.New("tools.yaml must be mapping")
	}
	allowedTop := map[string]struct{}{"allowed_tools": {}, "budget": {}}
	for k := range m {
		if _, ok := allowedTop[k]; !ok {
			return ToolsConfig{}, fmt.Errorf("unknown tools.yaml key: %s", k)
		}
	}
	tools := ToolsConfig{}
	tools.Budget = Budget{ToolCalls: -1, Seconds: -1, Tokens: -1}
	tools.AllowedTools, err = stringSliceField(m, "allowed_tools")
	if err != nil {
		return ToolsConfig{}, err
	}
	if len(tools.AllowedTools) == 0 {
		return ToolsConfig{}, errors.New("tools.yaml allowed_tools must be non-empty")
	}
	seen := map[string]struct{}{}
	for _, t := range tools.AllowedTools {
		if strings.TrimSpace(t) == "" {
			return ToolsConfig{}, errors.New("tools.yaml allowed_tools contains empty value")
		}
		if _, ok := seen[t]; ok {
			return ToolsConfig{}, fmt.Errorf("duplicate tool in allowed_tools: %s", t)
		}
		seen[t] = struct{}{}
	}
	if v, ok := m["budget"]; ok {
		bm, ok := v.(map[string]any)
		if !ok {
			return ToolsConfig{}, errors.New("tools.yaml budget must be mapping")
		}
		for k := range bm {
			switch k {
			case "tool_calls", "seconds", "tokens":
			default:
				return ToolsConfig{}, fmt.Errorf("unknown tools.yaml budget key: %s", k)
			}
		}
		tools.Budget.ToolCalls = int(int64FromAny(bm["tool_calls"]))
		tools.Budget.Seconds = int(int64FromAny(bm["seconds"]))
		tools.Budget.Tokens = int(int64FromAny(bm["tokens"]))
		if tools.Budget.ToolCalls < 0 || tools.Budget.Seconds < 0 || tools.Budget.Tokens < 0 {
			return ToolsConfig{}, errors.New("tools.yaml budget values must be >= 0")
		}
	} else {
		tools.Budget = Budget{}
	}
	return tools, nil
}

func CheckEligibility(meta Frontmatter, ee EligibilityEnv) []string {
	var reasons []string
	for _, osName := range meta.OS {
		if strings.EqualFold(osName, ee.GOOS) {
			goto bins
		}
	}
	if len(meta.OS) > 0 {
		reasons = append(reasons, "requires.os")
	}
bins:
	for _, k := range meta.Requires.Env {
		if _, ok := ee.LookupEnv(k); !ok {
			reasons = append(reasons, "requires.env:"+k)
		}
	}
	for _, b := range meta.Requires.Bins {
		if err := ee.LookPath(b); err != nil {
			reasons = append(reasons, "requires.bins:"+b)
		}
	}
	for _, c := range meta.Requires.Config {
		if ee.Config == nil {
			reasons = append(reasons, "requires.config:"+c)
			continue
		}
		if _, ok := ee.Config[c]; !ok {
			reasons = append(reasons, "requires.config:"+c)
		}
	}
	sort.Strings(reasons)
	return reasons
}

func parseFrontmatter(m map[string]any) (Frontmatter, error) {
	for k := range m {
		switch k {
		case "name", "description", "requires", "os", "metadata":
		default:
			return Frontmatter{}, fmt.Errorf("unknown frontmatter key: %s", k)
		}
	}
	var fm Frontmatter
	var err error
	fm.Name, err = stringField(m, "name")
	if err != nil {
		return Frontmatter{}, err
	}
	if !kebabNameRE.MatchString(fm.Name) {
		return Frontmatter{}, fmt.Errorf("invalid skill name (must be kebab-case): %s", fm.Name)
	}
	fm.Description, err = stringField(m, "description")
	if err != nil {
		return Frontmatter{}, err
	}
	fm.OS, err = stringSliceField(m, "os")
	if err != nil {
		return Frontmatter{}, err
	}
	if v, ok := m["metadata"]; ok {
		md, ok := v.(map[string]any)
		if !ok {
			return Frontmatter{}, errors.New("metadata must be mapping")
		}
		fm.Metadata = md
	} else {
		fm.Metadata = map[string]any{}
	}
	if v, ok := m["requires"]; ok {
		rm, ok := v.(map[string]any)
		if !ok {
			return Frontmatter{}, errors.New("requires must be mapping")
		}
		for k := range rm {
			switch k {
			case "bins", "env", "config":
			default:
				return Frontmatter{}, fmt.Errorf("unknown requires key: %s", k)
			}
		}
		fm.Requires.Bins, err = stringSliceField(rm, "bins")
		if err != nil {
			return Frontmatter{}, err
		}
		fm.Requires.Env, err = stringSliceField(rm, "env")
		if err != nil {
			return Frontmatter{}, err
		}
		fm.Requires.Config, err = stringSliceField(rm, "config")
		if err != nil {
			return Frontmatter{}, err
		}
	}
	return fm, nil
}

func stringField(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", fmt.Errorf("missing frontmatter key: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("frontmatter key %s must be string", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("frontmatter key %s cannot be empty", key)
	}
	return s, nil
}

func stringSliceField(m map[string]any, key string) ([]string, error) {
	v, ok := m[key]
	if !ok {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("key %s must be []string", key)
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("key %s must be []string", key)
		}
		out = append(out, strings.TrimSpace(s))
	}
	return out, nil
}

func normalizeYAML(v any) any {
	switch x := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, v := range x {
			m[fmt.Sprint(k)] = normalizeYAML(v)
		}
		return m
	case []interface{}:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalizeYAML(x[i])
		}
		return out
	default:
		return x
	}
}

func int64FromAny(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case uint64:
		return int64(x)
	case float64:
		return int64(x)
	default:
		return 0
	}
}
