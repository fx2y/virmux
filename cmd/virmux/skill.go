package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/haris/virmux/internal/agent"
	skillpkg "github.com/haris/virmux/internal/skill"
	skilleval "github.com/haris/virmux/internal/skill/eval"
	skilljudge "github.com/haris/virmux/internal/skill/judge"
	skillrun "github.com/haris/virmux/internal/skill/run"
	skillspec "github.com/haris/virmux/internal/skill/spec"
	"github.com/haris/virmux/internal/store"
	"github.com/haris/virmux/internal/trace"
	"github.com/haris/virmux/internal/vm"
)

var skillNameArgRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func cmdSkill(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: virmux skill <lint|run|judge|replay|ab|promote|refine|suggest>")
	}
	switch args[0] {
	case "lint":
		return cmdSkillLint(args[1:])
	case "run":
		return cmdSkillRun(args[1:])
	case "judge":
		return cmdSkillJudge(args[1:])
	case "replay":
		return cmdSkillReplay(args[1:])
	case "ab":
		return cmdSkillAB(args[1:])
	case "promote":
		return cmdSkillPromote(args[1:])
	case "refine":
		return cmdSkillRefine(args[1:])
	case "suggest":
		return cmdSkillSuggest(args[1:])
	default:
		return fmt.Errorf("unknown skill subcommand: %s", args[0])
	}
}

func cmdSkillLint(args []string) error {
	fs := flag.NewFlagSet("skill lint", flag.ContinueOnError)
	skillsRoot := fs.String("skills-dir", "skills", "skills root directory")
	jsonOut := fs.Bool("json", true, "emit json summary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dirs := fs.Args()
	if len(dirs) == 0 {
		var err error
		dirs, err = discoverSkillDirs(*skillsRoot)
		if err != nil {
			return err
		}
	}
	results, err := skillspec.LintDirs(dirs, skillspec.DefaultEligibilityEnv())
	if err != nil {
		return err
	}
	if *jsonOut {
		b, _ := json.Marshal(results)
		fmt.Println(string(b))
		return nil
	}
	for _, r := range results {
		fmt.Printf("%s name=%s dormant=%v", r.Dir, r.Name, r.Dormant)
		if len(r.Reasons) > 0 {
			fmt.Printf(" reasons=%s", strings.Join(r.Reasons, ","))
		}
		fmt.Println()
	}
	return nil
}

func cmdSkillRun(args []string) error {
	preSkill := ""
	if len(args) > 0 && !strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		preSkill = strings.TrimSpace(args[0])
		args = append([]string{}, args[1:]...)
	}
	fs, cfg, timeoutSec := commonFlags("skill run")
	skillsRoot := fs.String("skills-dir", "skills", "skills root directory")
	fixturePath := fs.String("fixture", "", "fixture path (absolute or skill-relative)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	skillArgs := fs.Args()
	if preSkill != "" {
		skillArgs = append([]string{preSkill}, skillArgs...)
	}
	if len(skillArgs) != 1 {
		return errors.New("usage: virmux skill run <skill> --fixture <path>")
	}
	skillRef := strings.TrimSpace(skillArgs[0])
	if skillRef == "" {
		return errors.New("skill name cannot be empty")
	}
	if strings.TrimSpace(*fixturePath) == "" {
		return errors.New("--fixture is required")
	}
	cfg.timeout = time.Duration(*timeoutSec) * time.Second
	if cfg.vsockCID == 0 {
		cfg.vsockCID = 52
	}

	name, ref := splitSkillRef(skillRef)
	if !skillNameArgRE.MatchString(name) {
		return fmt.Errorf("SKILL_PATH_ESCAPE: invalid skill name %q (expected kebab-case token)", name)
	}
	dir, err := safeJoin(*skillsRoot, name)
	if err != nil {
		return fmt.Errorf("SKILL_PATH_ESCAPE: %w", err)
	}
	ee := skillspec.DefaultEligibilityEnv()
	skillDef, err := skillspec.LoadDir(dir, ee)
	if err != nil {
		return err
	}
	if skillDef.Dormant {
		return fmt.Errorf("skill %s dormant (excluded): %s", skillDef.Meta.Name, strings.Join(skillDef.Reasons, ","))
	}
	fxPath := *fixturePath
	if !filepath.IsAbs(fxPath) {
		if strings.HasPrefix(fxPath, "tests/") {
			fxPath = filepath.Join(dir, fxPath)
		} else {
			fxPath = filepath.Join(dir, "tests", fxPath)
		}
	}
	fx, err := skillrun.LoadFixture(fxPath)
	if err != nil {
		return err
	}
	if !toolAllowed(skillDef.Tools.AllowedTools, fx.Tool) {
		return fmt.Errorf("TOOL_DENIED: tool %s not allowlisted by %s", fx.Tool, filepath.Join(dir, "tools.yaml"))
	}
	bgt := skillrun.Budget{ToolCalls: skillDef.Tools.Budget.ToolCalls, Seconds: skillDef.Tools.Budget.Seconds, Tokens: skillDef.Tools.Budget.Tokens}
	bt := skillrun.NewBudgetTracker(bgt, time.Now().UTC())
	if err := bt.BeforeToolCall(fx.Tool); err != nil {
		return runSkillBudgetFailure(cfg, skillDef, fxPath, fx, err)
	}
	if bgt.Seconds > 0 && (cfg.timeout <= 0 || time.Duration(bgt.Seconds)*time.Second < cfg.timeout) {
		cfg.timeout = time.Duration(bgt.Seconds) * time.Second
	}
	cfg.tool = fx.Tool
	cfg.allowCSV = strings.Join(skillDef.Tools.AllowedTools, ",")
	if err := applyFixtureToolArgs(cfg, fx, bgt); err != nil {
		return err
	}
	fp, err := buildSkillFingerprints(skillDef)
	if err != nil {
		return err
	}
	task := "skill:run"
	startedPayload := map[string]any{
		"label":         cfg.label,
		"skill":         skillDef.Meta.Name,
		"skill_ref":     skillRef,
		"skill_sha":     fp.SkillSHA,
		"prompt_fp":     fp.PromptFingerprint,
		"skill_base_fp": fp.SkillBaseFingerprint,
		"skill_dir":     filepath.ToSlash(dir),
		"git_ref":       ref,
		"fixture":       filepath.ToSlash(fxPath),
		"fixture_id":    fx.ID,
		"dormant":       false,
		"allow_tools":   skillDef.Tools.AllowedTools,
		"budget": map[string]any{
			"tool_calls": bgt.ToolCalls,
			"seconds":    bgt.Seconds,
			"tokens":     bgt.Tokens,
		},
	}
	inner := newVMRunRunner(cfg, fixtureCommandLabel(fx), nil)
	runner := wrapSkillRunner(inner, skillDef, fp, skillRef, fxPath, fx)
	return runWithStore(cfg, task, startedPayload, runner, defaultRunRuntime)
}

func cmdSkillReplay(args []string) error {
	fs := flag.NewFlagSet("skill replay", flag.ContinueOnError)
	runsDir := fs.String("runs-dir", "runs", "runs directory")
	dbPath := fs.String("db", "runs/virmux.sqlite", "sqlite db path")
	againstRunID := fs.String("against", "", "optional run id to compare against")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return errors.New("usage: virmux skill replay <run-id> [--against <run-id>]")
	}
	runID := strings.TrimSpace(fs.Args()[0])
	if runID == "" {
		return errors.New("run id cannot be empty")
	}
	var (
		rep skillrun.ReplayReport
		err error
	)
	if strings.TrimSpace(*againstRunID) != "" {
		rep, err = skillrun.CompareReplayHashes(*dbPath, *runsDir, runID, strings.TrimSpace(*againstRunID))
	} else {
		rep, err = skillrun.VerifyReplayHashes(*dbPath, filepath.Join(*runsDir, runID), runID)
	}
	if err != nil {
		return err
	}
	b, _ := json.Marshal(rep)
	fmt.Println(string(b))
	if !rep.Verified && !rep.Exempt {
		return fmt.Errorf("REPLAY_MISMATCH: %s", rep.Mismatch)
	}
	return nil
}

type skillEventEmitter func(ctx context.Context, st *store.Store, tw *trace.Writer, runID, task, event string, payload map[string]any, now func() time.Time) error

func cmdSkillJudge(args []string) error {
	return cmdSkillJudgeWithEmitter(args, emit)
}

func cmdSkillJudgeWithEmitter(args []string, emitEvent skillEventEmitter) error {
	fs := flag.NewFlagSet("skill judge", flag.ContinueOnError)
	runsDir := fs.String("runs-dir", "runs", "runs directory")
	dbPath := fs.String("db", "runs/virmux.sqlite", "sqlite db path")
	skillsDir := fs.String("skills-dir", "skills", "skills root directory")
	rubricPath := fs.String("rubric", "", "optional rubric path override")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return errors.New("usage: virmux skill judge <run-id>")
	}
	runID := strings.TrimSpace(fs.Args()[0])
	if runID == "" {
		return errors.New("run id cannot be empty")
	}
	runDir := filepath.Join(*runsDir, runID)
	meta, err := readSkillRunMeta(runDir)
	if err != nil {
		return err
	}
	skillName := strings.TrimSpace(meta.Skill)
	if skillName == "" {
		return errors.New("skill-run.json missing skill")
	}
	rubric := strings.TrimSpace(*rubricPath)
	if rubric == "" {
		rubric = filepath.Join(*skillsDir, skillName, "rubric.yaml")
	}
	r, rubricHash, err := skilljudge.LoadRubric(rubric)
	if err != nil {
		return err
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	task, status, err := lookupRunTaskStatus(st, runID)
	if err != nil {
		return err
	}
	tw, err := trace.NewWriter(filepath.Join(runDir, "trace.ndjson"))
	if err != nil {
		return err
	}
	defer tw.Close()

	ctx := context.Background()
	if err := emitEvent(ctx, st, tw, runID, task, "skill.judge.started", map[string]any{
		"skill":       skillName,
		"rubric":      filepath.ToSlash(rubric),
		"rubric_hash": rubricHash,
	}, time.Now); err != nil {
		return err
	}
	res, err := skilljudge.Evaluate(r, rubricHash, skilljudge.Evidence{
		RunID:       runID,
		Skill:       skillName,
		RunDir:      runDir,
		RunStatus:   status,
		ToolCalls:   meta.ToolCalls,
		ExpectFiles: meta.ExpectFiles,
	})
	if err != nil {
		return err
	}
	scorePath, err := skillrun.EnsureScorePlaceholder(runDir, map[string]any{
		"run_id":         res.RunID,
		"skill":          res.Skill,
		"score":          res.Score,
		"pass":           res.Pass,
		"critique":       res.Critique,
		"criterion":      res.Criterion,
		"rubric_hash":    res.RubricHash,
		"judge_cfg_hash": res.JudgeCfgHash,
		"artifact_hash":  res.ArtifactHash,
	})
	if err != nil {
		return err
	}
	metricsJSON, _ := json.Marshal(criteriaMap(res.Criterion))
	critiqueJSON, _ := json.Marshal(res.Critique)
	now := time.Now().UTC()
	if err := st.InsertScore(ctx, store.Score{
		RunID:        runID,
		Skill:        skillName,
		Score:        res.Score,
		Pass:         res.Pass,
		Critique:     string(critiqueJSON),
		JudgeCfgHash: res.JudgeCfgHash,
		ArtifactHash: res.ArtifactHash,
		CreatedAt:    now,
	}); err != nil {
		return err
	}
	if err := st.InsertJudgeRun(ctx, store.JudgeRun{
		RunID:        runID,
		Skill:        skillName,
		RubricHash:   res.RubricHash,
		JudgeCfgHash: res.JudgeCfgHash,
		ArtifactHash: res.ArtifactHash,
		MetricsJSON:  string(metricsJSON),
		Critique:     string(critiqueJSON),
		Score:        res.Score,
		Pass:         res.Pass,
		CreatedAt:    now,
	}); err != nil {
		return err
	}
	if err := emitEvent(ctx, st, tw, runID, task, "skill.judge.scored", map[string]any{
		"skill":          skillName,
		"score":          res.Score,
		"pass":           res.Pass,
		"critique":       res.Critique,
		"criterion":      res.Criterion,
		"score_path":     filepath.ToSlash(filepath.Base(scorePath)),
		"rubric_hash":    res.RubricHash,
		"judge_cfg_hash": res.JudgeCfgHash,
		"artifact_hash":  res.ArtifactHash,
	}, time.Now); err != nil {
		return err
	}
	if err := persistRunArtifacts(ctx, st, runID, []string{scorePath}); err != nil {
		return err
	}
	b, _ := json.Marshal(res)
	fmt.Println(string(b))
	return nil
}

func discoverSkillDirs(root string) ([]string, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read skills dir %s: %w", root, err)
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(p, "SKILL.md")); err == nil {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func splitSkillRef(ref string) (name, gitRef string) {
	name = ref
	if i := strings.Index(ref, "@"); i > 0 {
		name = ref[:i]
		gitRef = ref[i+1:]
	}
	return strings.TrimSpace(name), strings.TrimSpace(gitRef)
}

func toolAllowed(allow []string, tool string) bool {
	for _, a := range allow {
		if a == tool {
			return true
		}
	}
	return false
}

func applyFixtureToolArgs(cfg *runCommon, fx skillrun.Fixture, b skillrun.Budget) error {
	args := map[string]any{}
	for k, v := range fx.Args {
		args[k] = v
	}
	if fx.Tool == "shell.exec" {
		if _, ok := args["cmd"]; !ok && strings.TrimSpace(fx.Cmd) != "" {
			args["cmd"] = fx.Cmd
		}
		args["cwd"] = "/dev/virmux-data"
		if b.Seconds > 0 {
			args["timeout_ms"] = b.Seconds * 1000
		}
	}
	bb, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("marshal fixture tool args: %w", err)
	}
	cfg.toolArgsJSON = string(bb)
	return nil
}

func fixtureCommandLabel(fx skillrun.Fixture) string {
	if fx.Tool == "shell.exec" {
		if cmd, _ := fx.Args["cmd"].(string); strings.TrimSpace(cmd) != "" {
			return cmd
		}
		if strings.TrimSpace(fx.Cmd) != "" {
			return fx.Cmd
		}
	}
	return fmt.Sprintf("skill fixture tool=%s", fx.Tool)
}

type skillFingerprints struct {
	SkillSHA             string
	PromptFingerprint    string
	SkillBaseFingerprint string
}

func wrapSkillRunner(inner vmRunner, skillDef skillspec.Skill, fp skillFingerprints, skillRef, fixturePath string, fx skillrun.Fixture) vmRunner {
	return func(ctx context.Context, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
		details := map[string]any{}
		if err := emitVM("skill.run.selected", map[string]any{
			"skill":      skillDef.Meta.Name,
			"skill_ref":  skillRef,
			"fixture":    filepath.ToSlash(fixturePath),
			"fixture_id": fx.ID,
			"tool":       fx.Tool,
		}); err != nil {
			return vm.Outcome{}, details, err
		}
		metaPath, err := writeSkillRunMeta(runDir, skillDef, fp, skillRef, fixturePath, fx)
		if err == nil {
			details["skill_meta_path"] = metaPath
		}
		scorePath, scoreErr := skillrun.EnsureScorePlaceholder(runDir, map[string]any{
			"status": "pending",
			"phase":  "c1-placeholder",
			"skill":  skillDef.Meta.Name,
		})
		if scoreErr == nil {
			details["score_path"] = scorePath
		}
		outcome, innerDetails, runErr := inner(ctx, art, meta, runDir, emitVM)
		for k, v := range innerDetails {
			details[k] = v
		}
		scoreStatus := "pending"
		if runErr != nil {
			scoreStatus = "blocked"
		}
		if scorePath != "" {
			_, _ = skillrun.EnsureScorePlaceholder(runDir, map[string]any{
				"status": scoreStatus,
				"phase":  "c1-placeholder",
				"skill":  skillDef.Meta.Name,
			})
		}
		scoreRef := ""
		if scorePath != "" {
			scoreRef = filepath.ToSlash(filepath.Base(scorePath))
		}
		if err := emitVM("skill.score.placeholder", map[string]any{"status": scoreStatus, "score_path": scoreRef}); err != nil && runErr == nil {
			runErr = err
		}
		if runErr != nil && strings.Contains(strings.ToUpper(runErr.Error()), "CODE=DENIED") {
			runErr = fmt.Errorf("TOOL_DENIED: %w", runErr)
		}
		if err != nil && runErr == nil {
			runErr = err
		}
		if scoreErr != nil && runErr == nil {
			runErr = scoreErr
		}
		return outcome, details, runErr
	}
}

func writeSkillRunMeta(runDir string, skillDef skillspec.Skill, fp skillFingerprints, skillRef, fixturePath string, fx skillrun.Fixture) (string, error) {
	expectFiles := extractFixtureExpectFiles(fx.Expect)
	if fp.SkillSHA == "" || fp.PromptFingerprint == "" || fp.SkillBaseFingerprint == "" {
		next, err := buildSkillFingerprints(skillDef)
		if err != nil {
			return "", err
		}
		fp = next
	}
	out := map[string]any{
		"skill":         skillDef.Meta.Name,
		"skill_ref":     skillRef,
		"skill_sha":     fp.SkillSHA,
		"prompt_fp":     fp.PromptFingerprint,
		"skill_base_fp": fp.SkillBaseFingerprint,
		"fixture":       filepath.ToSlash(fixturePath),
		"fixture_id":    fx.ID,
		"tool":          fx.Tool,
		"allowed":       skillDef.Tools.AllowedTools,
		"budget":        skillDef.Tools.Budget,
		"deterministic": fx.Deterministic,
		"expect":        fx.Expect,
		"expect_files":  expectFiles,
		"tool_calls":    1,
	}
	path := filepath.Join(runDir, "skill-run.json")
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func extractFixtureExpectFiles(expect map[string]any) []string {
	if expect == nil {
		return nil
	}
	raw, ok := expect["files"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	sort.Strings(out)
	return out
}

func runSkillBudgetFailure(cfg *runCommon, skillDef skillspec.Skill, fixturePath string, fx skillrun.Fixture, budgetErr error) error {
	if cfg == nil {
		return budgetErr
	}
	st, err := store.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	now := time.Now().UTC()
	runID := defaultRunRuntime.runID("skill:run", now)
	runDir := filepath.Join(cfg.runsDir, runID)
	tracePath, traceCompatPath, metaPath, err := prepareRunFiles(runDir, runID, "skill:run", now)
	if err != nil {
		return err
	}
	tw, err := trace.NewWriter(tracePath)
	if err != nil {
		return err
	}
	defer tw.Close()
	ctx := context.Background()
	if err := st.StartRun(ctx, store.Run{
		ID:        runID,
		Task:      "skill:run",
		Label:     cfg.label,
		AgentID:   cfg.agentID,
		ImageSHA:  "preflight:budget",
		KernelSHA: "",
		RootfsSHA: "",
		StartedAt: now,
	}); err != nil {
		return err
	}
	startedPayload := map[string]any{
		"label":      cfg.label,
		"agent_id":   cfg.agentID,
		"skill":      skillDef.Meta.Name,
		"fixture":    filepath.ToSlash(fixturePath),
		"fixture_id": fx.ID,
	}
	if err := emit(ctx, st, tw, runID, "skill:run", "run.started", startedPayload, time.Now); err != nil {
		_ = st.FinishRun(ctx, runID, "failed", 0, 0, tracePath, "", 0, time.Now().UTC())
		return err
	}
	fp, err := buildSkillFingerprints(skillDef)
	if err != nil {
		return err
	}
	details := map[string]any{}
	if p, err := writeSkillRunMeta(runDir, skillDef, fp, skillDef.Meta.Name, fixturePath, fx); err != nil {
		return err
	} else {
		details["skill_meta_path"] = p
	}
	if p, err := skillrun.EnsureScorePlaceholder(runDir, map[string]any{"status": "blocked", "error_code": "BUDGET_EXCEEDED", "error": budgetErr.Error()}); err != nil {
		return err
	} else {
		details["score_path"] = p
	}
	if err := emit(ctx, st, tw, runID, "skill:run", "skill.run.selected", map[string]any{"skill": skillDef.Meta.Name, "fixture": filepath.ToSlash(fixturePath), "tool": fx.Tool}, time.Now); err != nil {
		return err
	}
	if err := emit(ctx, st, tw, runID, "skill:run", "skill.budget.exceeded", map[string]any{"error_code": "BUDGET_EXCEEDED", "error": budgetErr.Error()}, time.Now); err != nil {
		return err
	}
	payload := map[string]any{
		"status": "failed",
	}
	for k, v := range details {
		payload[k] = v
	}
	addFailurePayload(payload, budgetErr)
	if err := emit(ctx, st, tw, runID, "skill:run", "run.finished", payload, time.Now); err != nil {
		return err
	}
	if err := st.FinishRun(ctx, runID, "failed", 0, 0, tracePath, "", 0, time.Now().UTC()); err != nil {
		return err
	}
	if err := persistRunArtifacts(ctx, st, runID, []string{
		tracePath,
		traceCompatPath,
		metaPath,
		stringDetail(details, "score_path"),
		stringDetail(details, "skill_meta_path"),
	}); err != nil {
		return err
	}
	if exportErr := maybeAutoExportFailure(ctx, cfg, st, tw, time.Now, runID, "skill:run", "failed"); exportErr != nil {
		return exportErr
	}
	return budgetErr
}

func localSkillSHA(dir string) (string, error) {
	h := make([]byte, 0, 1024)
	for _, rel := range []string{"SKILL.md", "tools.yaml", "rubric.yaml"} {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return "", fmt.Errorf("read required skill file %s: %w", rel, err)
		}
		h = append(h, '\x00')
		h = append(h, []byte(rel)...)
		h = append(h, '\x00')
		h = append(h, b...)
	}
	sum := sha256.Sum256(h)
	return fmt.Sprintf("%x", sum[:]), nil
}

func buildSkillFingerprints(skillDef skillspec.Skill) (skillFingerprints, error) {
	skillSHA, err := localSkillSHA(skillDef.Dir)
	if err != nil {
		return skillFingerprints{}, err
	}
	promptSum := sha256.Sum256([]byte(strings.TrimSpace(skillDef.Body) + "\x00"))
	skillBytes, err := os.ReadFile(skillDef.Path)
	if err != nil {
		return skillFingerprints{}, fmt.Errorf("read %s: %w", skillDef.Path, err)
	}
	baseSum := sha256.Sum256(skillBytes)
	return skillFingerprints{
		SkillSHA:             skillSHA,
		PromptFingerprint:    fmt.Sprintf("%x", promptSum[:]),
		SkillBaseFingerprint: fmt.Sprintf("%x", baseSum[:]),
	}, nil
}

type skillRunMeta struct {
	Skill                string         `json:"skill"`
	SkillSHA             string         `json:"skill_sha,omitempty"`
	PromptFingerprint    string         `json:"prompt_fp,omitempty"`
	SkillBaseFingerprint string         `json:"skill_base_fp,omitempty"`
	Fixture              string         `json:"fixture"`
	Tool                 string         `json:"tool"`
	Deterministic        bool           `json:"deterministic"`
	Expect               map[string]any `json:"expect,omitempty"`
	ToolCalls            int            `json:"tool_calls,omitempty"`
	ExpectFiles          []string       `json:"expect_files,omitempty"`
}

func readSkillRunMeta(runDir string) (skillRunMeta, error) {
	b, err := os.ReadFile(filepath.Join(runDir, "skill-run.json"))
	if err != nil {
		return skillRunMeta{}, fmt.Errorf("read skill-run.json: %w", err)
	}
	var meta skillRunMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return skillRunMeta{}, fmt.Errorf("parse skill-run.json: %w", err)
	}
	if len(meta.ExpectFiles) == 0 && meta.Expect != nil {
		if raw, ok := meta.Expect["files"].([]any); ok {
			for _, v := range raw {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					meta.ExpectFiles = append(meta.ExpectFiles, strings.TrimSpace(s))
				}
			}
		}
	}
	return meta, nil
}

func lookupRunTaskStatus(db *store.Store, runID string) (string, string, error) {
	var task, status string
	if err := db.DB().QueryRow(`SELECT task,status FROM runs WHERE id=?`, runID).Scan(&task, &status); err != nil {
		return "", "", fmt.Errorf("query run %s: %w", runID, err)
	}
	return task, status, nil
}

func criteriaMap(in []skilljudge.CriterionScore) map[string]float64 {
	out := make(map[string]float64, len(in))
	for _, c := range in {
		out[c.ID] = c.Value
	}
	return out
}

func cmdSkillAB(args []string) error {
	fs := flag.NewFlagSet("skill ab", flag.ContinueOnError)
	repoDir := fs.String("repo-dir", ".", "git repository root")
	skillsDir := fs.String("skills-dir", "skills", "skills directory (repo-relative)")
	runsDir := fs.String("runs-dir", "runs", "runs directory")
	dbPath := fs.String("db", "runs/virmux.sqlite", "sqlite db path")
	provider := fs.String("provider", "openai:gpt-4.1-mini", "promptfoo provider string")
	promptfooBin := fs.String("promptfoo-bin", "promptfoo", "promptfoo binary path")
	cohort := fs.String("cohort", "", "cohort label for SQL cert scoping")
	scoreMin := fs.Float64("score-delta-min", 0.0, "minimum allowed p50 score delta (head-base)")
	failMax := fs.Float64("fail-rate-delta-max", 0.0, "maximum allowed fail-rate delta (head-base)")
	costMax := fs.Float64("cost-delta-max", 0.0, "maximum allowed cost delta (head-base)")
	costGate := fs.Bool("cost-gate", false, "enforce cost delta threshold")
	timeoutSec := fs.Int("timeout-sec", 120, "promptfoo validate/eval timeout per side")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pos := fs.Args()
	if len(pos) < 2 || len(pos) > 3 {
		return errors.New("usage: virmux skill ab <skill> <base..head>|<base> <head>")
	}
	skillName := strings.TrimSpace(pos[0])
	if skillName == "" {
		return errors.New("skill name cannot be empty")
	}
	if !skillNameArgRE.MatchString(skillName) {
		return fmt.Errorf("SKILL_PATH_ESCAPE: invalid skill name %q (expected kebab-case token)", skillName)
	}
	baseRef := ""
	headRef := ""
	if len(pos) == 2 {
		parts := strings.SplitN(pos[1], "..", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return errors.New("second arg must be <base..head> when only two positionals are provided")
		}
		baseRef = strings.TrimSpace(parts[0])
		headRef = strings.TrimSpace(parts[1])
	} else {
		baseRef = strings.TrimSpace(pos[1])
		headRef = strings.TrimSpace(pos[2])
	}
	if baseRef == headRef {
		return errors.New("base and head refs must differ")
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	cohortLabel := strings.TrimSpace(*cohort)
	if cohortLabel == "" {
		cohortLabel = "qa-skill-c3-" + time.Now().UTC().Format("20060102")
	}
	ex := skilleval.OSExec{}
	ctx := context.Background()
	headSnap, err := skilleval.LoadSkillSnapshot(ctx, ex, *repoDir, *skillsDir, skillName, headRef)
	if err != nil {
		return err
	}
	baseSnap, err := skilleval.LoadSkillSnapshot(ctx, ex, *repoDir, *skillsDir, skillName, baseRef)
	if err != nil {
		return err
	}
	if err := skilleval.ValidateFrozenFixtureSet(headSnap.Fixtures, baseSnap.Fixtures); err != nil {
		return fmt.Errorf("frozen fixture set violation: %w", err)
	}
	baseSnapFrozen := baseSnap
	baseSnapFrozen.Fixtures = append([]skilleval.Fixture(nil), headSnap.Fixtures...)
	evalID := fmt.Sprintf("%d-skillab", time.Now().UTC().UnixNano())
	evalDir := filepath.Join(*runsDir, evalID)
	if err := os.MkdirAll(evalDir, 0o755); err != nil {
		return err
	}
	baseCfgPath := filepath.Join(evalDir, "promptfoo.base.json")
	headCfgPath := filepath.Join(evalDir, "promptfoo.head.json")
	baseCfg, err := skilleval.BuildPromptfooConfig(baseSnapFrozen, *provider)
	if err != nil {
		return err
	}
	if err := os.WriteFile(baseCfgPath, baseCfg, 0o644); err != nil {
		return err
	}
	headCfg, err := skilleval.BuildPromptfooConfig(headSnap, *provider)
	if err != nil {
		return err
	}
	if err := os.WriteFile(headCfgPath, headCfg, 0o644); err != nil {
		return err
	}
	baseResPath := filepath.Join(evalDir, "promptfoo.base.results.json")
	headResPath := filepath.Join(evalDir, "promptfoo.head.results.json")
	timeout := time.Duration(*timeoutSec) * time.Second
	if err := skilleval.RunPromptfoo(ctx, ex, *repoDir, *promptfooBin, baseCfgPath, baseResPath, timeout); err != nil {
		return err
	}
	if err := skilleval.RunPromptfoo(ctx, ex, *repoDir, *promptfooBin, headCfgPath, headResPath, timeout); err != nil {
		return err
	}
	baseRaw, err := os.ReadFile(baseResPath)
	if err != nil {
		return err
	}
	headRaw, err := os.ReadFile(headResPath)
	if err != nil {
		return err
	}
	cfgBundlePath := filepath.Join(evalDir, "promptfoo.cfg.bundle.json")
	cfgBundle, _ := json.MarshalIndent(map[string]any{
		"base_cfg_path": filepath.ToSlash(filepath.Join(evalID, "promptfoo.base.json")),
		"head_cfg_path": filepath.ToSlash(filepath.Join(evalID, "promptfoo.head.json")),
		"base":          json.RawMessage(baseCfg),
		"head":          json.RawMessage(headCfg),
	}, "", "  ")
	cfgBundleBytes := append(cfgBundle, '\n')
	if err := os.WriteFile(cfgBundlePath, cfgBundleBytes, 0o644); err != nil {
		return err
	}
	resultsBundlePath := filepath.Join(evalDir, "promptfoo.results.bundle.json")
	resultsBundle, _ := json.MarshalIndent(map[string]any{
		"base_results_path": filepath.ToSlash(filepath.Join(evalID, "promptfoo.base.results.json")),
		"head_results_path": filepath.ToSlash(filepath.Join(evalID, "promptfoo.head.results.json")),
		"base":              json.RawMessage(baseRaw),
		"head":              json.RawMessage(headRaw),
	}, "", "  ")
	resultsBundleBytes := append(resultsBundle, '\n')
	if err := os.WriteFile(resultsBundlePath, resultsBundleBytes, 0o644); err != nil {
		return err
	}
	ids := make([]string, 0, len(headSnap.Fixtures))
	for _, fx := range headSnap.Fixtures {
		ids = append(ids, fx.ID)
	}
	baseMetrics, baseCases, err := skilleval.ParsePromptfooResults(baseRaw, ids)
	if err != nil {
		return err
	}
	headMetrics, headCases, err := skilleval.ParsePromptfooResults(headRaw, ids)
	if err != nil {
		return err
	}
	var maxCost *float64
	if *costGate {
		maxCost = costMax
	}
	verdict := skilleval.CompareAB(baseMetrics, headMetrics, skilleval.ABThresholds{
		MinScoreDelta:    *scoreMin,
		MaxFailRateDelta: *failMax,
		MaxCostDelta:     maxCost,
	})
	verdictPath := filepath.Join(evalDir, "ab-verdict.json")
	verdictDoc := map[string]any{
		"id":      evalID,
		"skill":   skillName,
		"cohort":  cohortLabel,
		"base":    map[string]any{"ref": baseRef, "metrics": baseMetrics},
		"head":    map[string]any{"ref": headRef, "metrics": headMetrics},
		"verdict": verdict,
		"gates": map[string]any{
			"score_delta_min":     *scoreMin,
			"fail_rate_delta_max": *failMax,
			"cost_delta_max":      maxCost,
		},
	}
	vb, _ := json.MarshalIndent(verdictDoc, "", "  ")
	verdictBytes := append(vb, '\n')
	if err := os.WriteFile(verdictPath, verdictBytes, 0o644); err != nil {
		return err
	}
	cfgSum := sha256.Sum256(cfgBundleBytes)
	resSum := sha256.Sum256(resultsBundleBytes)
	verdictSum := sha256.Sum256(verdictBytes)
	ctx = context.Background()
	if err := st.InsertEvalRun(ctx, store.EvalRun{
		ID:            evalID,
		Skill:         skillName,
		Cohort:        cohortLabel,
		BaseRef:       baseRef,
		HeadRef:       headRef,
		Provider:      *provider,
		FixturesHash:  skilleval.FixtureSetHash(headSnap.Fixtures),
		CfgSHA256:     fmt.Sprintf("%x", cfgSum[:]),
		CfgPath:       filepath.ToSlash(filepath.Join(evalID, "promptfoo.cfg.bundle.json")),
		ResultsSHA256: fmt.Sprintf("%x", resSum[:]),
		ResultsPath:   filepath.ToSlash(filepath.Join(evalID, "promptfoo.results.bundle.json")),
		VerdictSHA256: fmt.Sprintf("%x", verdictSum[:]),
		VerdictPath:   filepath.ToSlash(filepath.Join(evalID, "ab-verdict.json")),
		ScoreP50Base:  baseMetrics.ScoreP50,
		ScoreP50Head:  headMetrics.ScoreP50,
		FailRateBase:  baseMetrics.FailRate,
		FailRateHead:  headMetrics.FailRate,
		CostTotalBase: baseMetrics.CostTotal,
		CostTotalHead: headMetrics.CostTotal,
		ScoreP50Delta: verdict.ScoreDelta,
		FailRateDelta: verdict.FailRateDelta,
		CostDelta:     verdict.CostDelta,
		Pass:          verdict.Pass,
		VerdictJSON:   string(vb),
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		return err
	}
	for _, id := range ids {
		bc := baseCases[id]
		hc := headCases[id]
		if err := st.InsertEvalCase(ctx, store.EvalCase{
			EvalRunID: evalID,
			FixtureID: id,
			BaseScore: bc.Score,
			HeadScore: hc.Score,
			BasePass:  bc.Pass,
			HeadPass:  hc.Pass,
			BaseCost:  bc.Cost,
			HeadCost:  hc.Cost,
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
	}
	out := map[string]any{
		"id":     evalID,
		"skill":  skillName,
		"cohort": cohortLabel,
		"pass":   verdict.Pass,
		"reason": verdict.Reason,
		"artifacts": map[string]any{
			"eval_dir":     filepath.ToSlash(evalDir),
			"verdict_path": filepath.ToSlash(verdictPath),
		},
		"deltas": map[string]any{
			"score_p50_delta": verdict.ScoreDelta,
			"fail_rate_delta": verdict.FailRateDelta,
			"cost_delta":      verdict.CostDelta,
			"base_ref":        baseRef,
			"head_ref":        headRef,
			"base_score_p50":  baseMetrics.ScoreP50,
			"head_score_p50":  headMetrics.ScoreP50,
			"base_fail_rate":  baseMetrics.FailRate,
			"head_fail_rate":  headMetrics.FailRate,
		},
	}
	ob, _ := json.Marshal(out)
	fmt.Println(string(ob))
	if !verdict.Pass {
		return fmt.Errorf("AB_REGRESSION: %s", verdict.Reason)
	}
	return nil
}

func cmdSkillPromote(args []string) error {
	fs := flag.NewFlagSet("skill promote", flag.ContinueOnError)
	dbPath := fs.String("db", "runs/virmux.sqlite", "sqlite db path")
	repoDir := fs.String("repo-dir", ".", "git repository root")
	tag := fs.String("tag", "", "promotion tag (default: skill/<name>/prod)")
	actor := fs.String("actor", "", "actor id (default: $USER)")
	maxAgeH := fs.Int("max-age-hours", 24, "max AB verdict age before stale refusal")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 2 {
		return errors.New("usage: virmux skill promote <skill> <eval-run-id>")
	}
	skillName := strings.TrimSpace(fs.Args()[0])
	evalID := strings.TrimSpace(fs.Args()[1])
	if skillName == "" || evalID == "" {
		return errors.New("skill and eval-run-id are required")
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	row, err := st.GetEvalRun(context.Background(), evalID)
	if err != nil {
		return fmt.Errorf("MISSING_AB_VERDICT: %w", err)
	}
	if row.Skill != skillName {
		return fmt.Errorf("MISSING_AB_VERDICT: eval run skill=%s does not match %s", row.Skill, skillName)
	}
	if !row.Pass {
		return fmt.Errorf("MISSING_AB_VERDICT: eval run %s is not passing", evalID)
	}
	maxAge := time.Duration(*maxAgeH) * time.Hour
	if maxAge > 0 && !row.CreatedAt.IsZero() && time.Since(row.CreatedAt) > maxAge {
		return fmt.Errorf("STALE_AB_VERDICT: eval run %s older than %s", evalID, maxAge)
	}
	promoTag := strings.TrimSpace(*tag)
	if promoTag == "" {
		promoTag = fmt.Sprintf("skill/%s/prod", skillName)
	}
	promoActor := strings.TrimSpace(*actor)
	if promoActor == "" {
		promoActor = strings.TrimSpace(os.Getenv("USER"))
	}
	ex := skilleval.OSExec{}
	if _, err := ex.Run(context.Background(), skillpkg.Command{
		Dir:  *repoDir,
		Name: "git",
		Args: []string{"tag", "-f", promoTag, row.HeadRef},
	}); err != nil {
		return fmt.Errorf("move promotion tag: %w", err)
	}
	promoID := fmt.Sprintf("%d-promote", time.Now().UTC().UnixNano())
	if err := st.InsertPromotion(context.Background(), store.Promotion{
		ID:            promoID,
		Skill:         skillName,
		Tag:           promoTag,
		BaseRef:       row.BaseRef,
		HeadRef:       row.HeadRef,
		EvalRunID:     row.ID,
		VerdictSHA256: row.VerdictSHA256,
		Actor:         promoActor,
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		return err
	}
	out := map[string]any{
		"id":          promoID,
		"skill":       skillName,
		"tag":         promoTag,
		"eval_run_id": row.ID,
		"base_ref":    row.BaseRef,
		"head_ref":    row.HeadRef,
		"actor":       promoActor,
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
	return nil
}
