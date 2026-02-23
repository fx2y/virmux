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
	"sort"
	"strings"
	"time"

	"github.com/haris/virmux/internal/agent"
	skilljudge "github.com/haris/virmux/internal/skill/judge"
	skillrun "github.com/haris/virmux/internal/skill/run"
	skillspec "github.com/haris/virmux/internal/skill/spec"
	"github.com/haris/virmux/internal/store"
	"github.com/haris/virmux/internal/trace"
	"github.com/haris/virmux/internal/vm"
)

func cmdSkill(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: virmux skill <lint|run|judge|replay>")
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
	dir := filepath.Join(*skillsRoot, name)
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
	task := "skill:run"
	startedPayload := map[string]any{
		"label":       cfg.label,
		"skill":       skillDef.Meta.Name,
		"skill_ref":   skillRef,
		"skill_sha":   localSkillSHA(dir),
		"skill_dir":   filepath.ToSlash(dir),
		"git_ref":     ref,
		"fixture":     filepath.ToSlash(fxPath),
		"fixture_id":  fx.ID,
		"dormant":     false,
		"allow_tools": skillDef.Tools.AllowedTools,
		"budget": map[string]any{
			"tool_calls": bgt.ToolCalls,
			"seconds":    bgt.Seconds,
			"tokens":     bgt.Tokens,
		},
	}
	inner := newVMRunRunner(cfg, fixtureCommandLabel(fx), nil)
	runner := wrapSkillRunner(inner, skillDef, skillRef, fxPath, fx)
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

func cmdSkillJudge(args []string) error {
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
	_ = emit(ctx, st, tw, runID, task, "skill.judge.started", map[string]any{
		"skill":       skillName,
		"rubric":      filepath.ToSlash(rubric),
		"rubric_hash": rubricHash,
	}, time.Now)
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
	if err := emit(ctx, st, tw, runID, task, "skill.judge.scored", map[string]any{
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

func wrapSkillRunner(inner vmRunner, skillDef skillspec.Skill, skillRef, fixturePath string, fx skillrun.Fixture) vmRunner {
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
		metaPath, err := writeSkillRunMeta(runDir, skillDef, skillRef, fixturePath, fx)
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

func writeSkillRunMeta(runDir string, skillDef skillspec.Skill, skillRef, fixturePath string, fx skillrun.Fixture) (string, error) {
	expectFiles := extractFixtureExpectFiles(fx.Expect)
	out := map[string]any{
		"skill":         skillDef.Meta.Name,
		"skill_ref":     skillRef,
		"skill_sha":     localSkillSHA(skillDef.Dir),
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
	// Reuse the existing evidence plane even for preflight policy failures.
	runner := func(ctx context.Context, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
		details := map[string]any{}
		if p, err := writeSkillRunMeta(runDir, skillDef, skillDef.Meta.Name, fixturePath, fx); err == nil {
			details["skill_meta_path"] = p
		}
		if p, err := skillrun.EnsureScorePlaceholder(runDir, map[string]any{"status": "blocked", "error_code": "BUDGET_EXCEEDED", "error": budgetErr.Error()}); err == nil {
			details["score_path"] = p
		}
		_ = emitVM("skill.run.selected", map[string]any{"skill": skillDef.Meta.Name, "fixture": filepath.ToSlash(fixturePath), "tool": fx.Tool})
		_ = emitVM("skill.budget.exceeded", map[string]any{"error_code": "BUDGET_EXCEEDED", "error": budgetErr.Error()})
		return vm.Outcome{}, details, budgetErr
	}
	startedPayload := map[string]any{
		"skill":      skillDef.Meta.Name,
		"fixture":    filepath.ToSlash(fixturePath),
		"fixture_id": fx.ID,
		"budget": map[string]any{
			"tool_calls": skillDef.Tools.Budget.ToolCalls,
			"seconds":    skillDef.Tools.Budget.Seconds,
			"tokens":     skillDef.Tools.Budget.Tokens,
		},
	}
	return runWithStore(cfg, "skill:run", startedPayload, runner, defaultRunRuntime)
}

func localSkillSHA(dir string) string {
	h := []byte(strings.TrimSpace(dir))
	for _, rel := range []string{"SKILL.md", "tools.yaml", "rubric.yaml"} {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		h = append(h, '\x00')
		h = append(h, []byte(rel)...)
		h = append(h, '\x00')
		h = append(h, b...)
	}
	sum := sha256.Sum256(h)
	return fmt.Sprintf("%x", sum[:])
}

type skillRunMeta struct {
	Skill         string         `json:"skill"`
	Fixture       string         `json:"fixture"`
	Tool          string         `json:"tool"`
	Deterministic bool           `json:"deterministic"`
	Expect        map[string]any `json:"expect,omitempty"`
	ToolCalls     int            `json:"tool_calls,omitempty"`
	ExpectFiles   []string       `json:"expect_files,omitempty"`
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
