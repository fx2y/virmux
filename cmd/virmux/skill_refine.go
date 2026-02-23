package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	skillpkg "github.com/haris/virmux/internal/skill"
	skilleval "github.com/haris/virmux/internal/skill/eval"
	skillgit "github.com/haris/virmux/internal/skill/gitops"
	skillrefine "github.com/haris/virmux/internal/skill/refine"
	skillspec "github.com/haris/virmux/internal/skill/spec"
	"github.com/haris/virmux/internal/store"
)

func cmdSkillRefine(args []string) error {
	fs := flagSet("skill refine")
	dbPath := fs.String("db", "runs/virmux.sqlite", "sqlite db path")
	runsDir := fs.String("runs-dir", "runs", "runs directory")
	skillsDir := fs.String("skills-dir", "skills", "skills root directory")
	repoDir := fs.String("repo-dir", ".", "git repository root")
	evalRunID := fs.String("eval-run-id", "", "optional eval run id (defaults to latest for skill)")
	maxAgeH := fs.Int("max-age-hours", 24, "max AB verdict age before stale refusal")
	allowToolsEdit := fs.Bool("allow-tools-edit", false, "allow tools.yaml modifications")
	maxHunks := fs.Int("max-hunks", 3, "max allowed patch hunks")
	precheck := fs.Bool("precheck", true, "run skill lint + fixture parse before commit")
	openPR := fs.Bool("open-pr", false, "open PR via gh when available")
	ghBin := fs.String("gh-bin", "gh", "gh binary path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return errors.New("usage: virmux skill refine <run-id>")
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
	skillDir := filepath.Join(*skillsDir, skillName)

	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	evalRow, err := resolveRefineEval(ctx, st, skillName, strings.TrimSpace(*evalRunID))
	if err != nil {
		return fmt.Errorf("MISSING_AB_VERDICT: %w", err)
	}
	if !evalRow.Pass {
		return fmt.Errorf("MISSING_AB_VERDICT: eval run %s is not passing", evalRow.ID)
	}
	maxAge := time.Duration(*maxAgeH) * time.Hour
	if maxAge > 0 && !evalRow.CreatedAt.IsZero() && time.Since(evalRow.CreatedAt) > maxAge {
		return fmt.Errorf("STALE_AB_VERDICT: eval run %s older than %s", evalRow.ID, maxAge)
	}

	score, err := skillrefine.LoadScore(filepath.Join(runDir, "score.json"))
	if err != nil {
		return err
	}
	suggestions, err := skillrefine.BuildSuggestions(skillDir, runID, score, *allowToolsEdit)
	if err != nil {
		return err
	}
	if len(suggestions) == 0 {
		return errors.New("refine produced zero file changes")
	}

	ex := skilleval.OSExec{}
	if err := ensureGitCleanForRefine(ctx, ex, *repoDir, suggestions); err != nil {
		return err
	}
	branch := skillgit.BranchName(skillName, runID)
	if _, err := ex.Run(ctx, skillpkg.Command{Dir: *repoDir, Name: "git", Args: []string{"checkout", "-b", branch}}); err != nil {
		return fmt.Errorf("create refine branch %s: %w", branch, err)
	}

	for _, s := range suggestions {
		if err := os.WriteFile(s.Path, s.Content, 0o644); err != nil {
			return fmt.Errorf("write suggestion %s: %w", s.Path, err)
		}
	}
	if !*allowToolsEdit {
		toolsPath := filepath.ToSlash(filepath.Join(skillDir, "tools.yaml"))
		changed, err := gitChangedPaths(ctx, ex, *repoDir, toolsPath)
		if err != nil {
			return err
		}
		if len(changed) > 0 {
			return fmt.Errorf("tools.yaml changed without opt-in (--allow-tools-edit): %s", toolsPath)
		}
	}
	if *precheck {
		if _, err := skillspec.LintDirs([]string{skillDir}, skillspec.DefaultEligibilityEnv()); err != nil {
			return fmt.Errorf("skill lint precheck failed: %w", err)
		}
		if err := skillrefine.ValidateFixtures(skillDir); err != nil {
			return fmt.Errorf("fixture precheck failed: %w", err)
		}
	}

	patchScope := filepath.ToSlash(skillDir)
	patch, err := gitDiff(ctx, ex, *repoDir, patchScope)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(patch))) == 0 {
		return errors.New("refine produced empty git diff")
	}
	hunkCount := strings.Count(string(patch), "@@")
	if hunkCount <= 0 {
		hunkCount = 1
	}
	if *maxHunks > 0 && hunkCount > *maxHunks {
		return fmt.Errorf("REFINE_PATCH_TOO_LARGE: %d hunks > max %d; split into smaller focused edit", hunkCount, *maxHunks)
	}

	patchPath := filepath.Join(runDir, "refine.patch")
	if err := os.WriteFile(patchPath, patch, 0o644); err != nil {
		return fmt.Errorf("write patch artifact: %w", err)
	}
	rationalePath := filepath.Join(runDir, "refine-rationale.json")
	rationaleRows := make([]map[string]any, 0, len(suggestions))
	for _, s := range suggestions {
		rel, err := filepath.Rel(*repoDir, s.Path)
		if err != nil {
			rel = s.Path
		}
		rationaleRows = append(rationaleRows, map[string]any{"path": filepath.ToSlash(rel), "rationale": s.Rationale})
	}
	rb, _ := json.MarshalIndent(rationaleRows, "", "  ")
	if err := os.WriteFile(rationalePath, append(rb, '\n'), 0o644); err != nil {
		return fmt.Errorf("write rationale artifact: %w", err)
	}
	patchRef := filepath.ToSlash(filepath.Join("runs", runID, "refine.patch"))
	rationaleRef := filepath.ToSlash(filepath.Join("runs", runID, "refine-rationale.json"))
	prBody := renderRefinePRBody(runID, score, evalRow, patchRef, rationaleRef)
	prBodyPath := filepath.Join(runDir, "refine-pr.md")
	if err := os.WriteFile(prBodyPath, []byte(prBody), 0o644); err != nil {
		return fmt.Errorf("write pr body artifact: %w", err)
	}
	prBodyRef := filepath.ToSlash(filepath.Join("runs", runID, "refine-pr.md"))
	if err := persistRunArtifacts(ctx, st, runID, []string{patchPath, rationalePath, prBodyPath}); err != nil {
		return fmt.Errorf("register refine artifacts: %w", err)
	}

	pathsToAdd := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		rel, err := filepath.Rel(*repoDir, s.Path)
		if err != nil {
			return fmt.Errorf("relative path for git add: %w", err)
		}
		pathsToAdd = append(pathsToAdd, filepath.ToSlash(rel))
	}
	sort.Strings(pathsToAdd)
	if _, err := ex.Run(ctx, skillpkg.Command{Dir: *repoDir, Name: "git", Args: append([]string{"add"}, pathsToAdd...)}); err != nil {
		return fmt.Errorf("git add refine changes: %w", err)
	}
	msg := fmt.Sprintf("refine(%s): run %s", skillName, runID)
	if _, err := ex.Run(ctx, skillpkg.Command{Dir: *repoDir, Name: "git", Args: []string{"commit", "-m", msg, "-m", strings.TrimSpace(prBody)}}); err != nil {
		return fmt.Errorf("git commit refine changes: %w", err)
	}
	headSHA, err := gitHeadSHA(ctx, ex, *repoDir)
	if err != nil {
		return err
	}

	prHint := fmt.Sprintf("%s pr create --title %q --body-file %q --head %q", *ghBin, msg, prBodyRef, branch)
	if *openPR {
		if has, _ := hasCommand(ctx, ex, *repoDir, *ghBin); has {
			if _, err := ex.Run(ctx, skillpkg.Command{Dir: *repoDir, Name: *ghBin, Args: []string{"pr", "create", "--title", msg, "--body-file", prBodyPath, "--head", branch}}); err != nil {
				return fmt.Errorf("open pr: %w", err)
			}
		}
	}

	refineID := fmt.Sprintf("%d-refine", time.Now().UTC().UnixNano())
	if err := st.InsertRefineRun(ctx, store.RefineRun{
		ID:         refineID,
		RunID:      runID,
		Skill:      skillName,
		EvalRunID:  evalRow.ID,
		Branch:     branch,
		CommitSHA:  headSHA,
		PatchHash:  skillgit.PatchHash(patch),
		PatchPath:  filepath.ToSlash(filepath.Join(runID, "refine.patch")),
		PRBodyPath: filepath.ToSlash(filepath.Join(runID, "refine-pr.md")),
		HunkCount:  hunkCount,
		ToolsEdit:  *allowToolsEdit,
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		return err
	}

	out := map[string]any{
		"id":          refineID,
		"run_id":      runID,
		"skill":       skillName,
		"eval_run_id": evalRow.ID,
		"branch":      branch,
		"commit":      headSHA,
		"patch_hash":  skillgit.PatchHash(patch),
		"hunks":       hunkCount,
		"artifacts": map[string]any{
			"patch":     patchRef,
			"pr_body":   prBodyRef,
			"rationale": rationaleRef,
		},
		"next": map[string]any{
			"pr_create": prHint,
		},
	}
	ob, _ := json.Marshal(out)
	fmt.Println(string(ob))
	return nil
}

func resolveRefineEval(ctx context.Context, st *store.Store, skillName, evalRunID string) (store.EvalRun, error) {
	if strings.TrimSpace(evalRunID) != "" {
		row, err := st.GetEvalRun(ctx, evalRunID)
		if err != nil {
			return store.EvalRun{}, err
		}
		if row.Skill != skillName {
			return store.EvalRun{}, fmt.Errorf("eval run skill=%s does not match %s", row.Skill, skillName)
		}
		return row, nil
	}
	return st.LatestPassingEvalRunBySkill(ctx, skillName)
}

func ensureGitCleanForRefine(ctx context.Context, ex skilleval.OSExec, repoDir string, suggestions []skillrefine.Suggestion) error {
	paths := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		rel, err := filepath.Rel(repoDir, s.Path)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	res, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: "git", Args: append([]string{"status", "--porcelain", "--"}, paths...)})
	if err != nil {
		return fmt.Errorf("git status refine paths: %w", err)
	}
	if strings.TrimSpace(string(res.Stdout)) != "" {
		return fmt.Errorf("target files already dirty; clean staged/unstaged changes before refine")
	}
	return nil
}

func gitChangedPaths(ctx context.Context, ex skilleval.OSExec, repoDir, path string) ([]string, error) {
	res, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: "git", Args: []string{"diff", "--name-only", "--", path}})
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
	var out []string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	return out, nil
}

func gitDiff(ctx context.Context, ex skilleval.OSExec, repoDir, scope string) ([]byte, error) {
	res, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: "git", Args: []string{"diff", "--", scope}})
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	return res.Stdout, nil
}

func gitHeadSHA(ctx context.Context, ex skilleval.OSExec, repoDir string) (string, error) {
	res, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: "git", Args: []string{"rev-parse", "HEAD"}})
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func hasCommand(ctx context.Context, ex skilleval.OSExec, repoDir, bin string) (bool, error) {
	res, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: "bash", Args: []string{"-lc", "command -v " + shellQuote(bin)}})
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(res.Stdout)) != "", nil
}

func shellQuote(s string) string {
	s = strings.ReplaceAll(s, "'", "'\\''")
	return "'" + s + "'"
}

func renderRefinePRBody(runID string, score skillrefine.Score, eval store.EvalRun, patchPath, rationalePath string) string {
	return strings.Join([]string{
		fmt.Sprintf("Run: %s", runID),
		fmt.Sprintf("Trace: runs/%s/trace.ndjson", runID),
		fmt.Sprintf("Score: %.4f pass=%t", score.Score, score.Pass),
		fmt.Sprintf("Rubric/Judge Hashes: rubric=%s judge_cfg=%s", score.RubricHash, score.JudgeCfgHash),
		fmt.Sprintf("AB: eval=%s score_p50_delta=%.4f fail_rate_delta=%.4f cost_delta=%.4f", eval.ID, eval.ScoreP50Delta, eval.FailRateDelta, eval.CostDelta),
		fmt.Sprintf("Patch: %s", filepath.ToSlash(patchPath)),
		fmt.Sprintf("Rationale: %s", filepath.ToSlash(rationalePath)),
	}, "\n") + "\n"
}
