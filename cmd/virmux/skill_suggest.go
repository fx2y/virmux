package main

import (
	"context"
	"database/sql"
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
	skillmotif "github.com/haris/virmux/internal/skill/motif"
	skillrun "github.com/haris/virmux/internal/skill/run"
	skillspec "github.com/haris/virmux/internal/skill/spec"
	"github.com/haris/virmux/internal/store"
)

func cmdSkillSuggest(args []string) error {
	fs := flagSet("skill suggest")
	dbPath := fs.String("db", "runs/virmux.sqlite", "sqlite db path")
	runsDir := fs.String("runs-dir", "runs", "runs directory")
	skillsDir := fs.String("skills-dir", "skills", "skills root directory")
	repoDir := fs.String("repo-dir", ".", "git repository root")
	minRepeats := fs.Int("min-repeats", 3, "minimum repeats to trigger")
	minPassRate := fs.Float64("min-pass-rate", 0.66, "minimum pass rate to trigger")
	minScoreP50 := fs.Float64("min-score-p50", 0.8, "minimum p50 score to trigger")
	openPR := fs.Bool("open-pr", false, "open PR via gh when available")
	ghBin := fs.String("gh-bin", "gh", "gh binary path")
	maxCandidates := fs.Int("max-candidates", 1, "max triggered motifs to scaffold")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	features, err := loadMotifFeatures(st.DB(), *runsDir, *skillsDir)
	if err != nil {
		return err
	}
	cands := skillmotif.RankCandidates(features, *skillsDir, skillmotif.Thresholds{
		MinRepeats:  *minRepeats,
		MinPassRate: *minPassRate,
		MinScoreP50: *minScoreP50,
	})
	if len(cands) == 0 {
		return errors.New("no score/tool evidence rows available for motif mining")
	}
	triggered := make([]skillmotif.Candidate, 0, len(cands))
	for _, c := range cands {
		if c.Verdict == skillmotif.VerdictTriggered {
			triggered = append(triggered, c)
		}
	}
	if len(triggered) == 0 {
		out := map[string]any{"triggered": []any{}, "motifs": cands}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		return fmt.Errorf("SUGGEST_NOT_TRIGGERED: no motifs passed thresholds")
	}
	if *maxCandidates > 0 && len(triggered) > *maxCandidates {
		triggered = triggered[:*maxCandidates]
	}

	ex := skilleval.OSExec{}
	ctx := context.Background()
	results := make([]map[string]any, 0, len(triggered))
	for _, cand := range triggered {
		exemplarRun := cand.RunIDs[0]
		tool, args := exemplarToolArgs(*runsDir, exemplarRun)
		files, err := skillmotif.BuildSuggestionFiles(*skillsDir, cand, tool, args)
		if err != nil {
			return err
		}
		if err := ensureGitCleanForSuggest(ctx, ex, *repoDir, files.SkillDir); err != nil {
			return err
		}
		branch := "suggest/" + strings.TrimPrefix(files.SkillName, "suggest-")
		if _, err := ex.Run(ctx, skillpkg.Command{Dir: *repoDir, Name: "git", Args: []string{"checkout", "-b", branch}}); err != nil {
			return fmt.Errorf("create suggest branch %s: %w", branch, err)
		}
		for path, content := range files.Files {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				return fmt.Errorf("write scaffold %s: %w", path, err)
			}
		}
		if _, err := skillspec.LintDirs([]string{files.SkillDir}, skillspec.DefaultEligibilityEnv()); err != nil {
			return fmt.Errorf("generated skill lint failed: %w", err)
		}
		fxPath := filepath.Join(files.SkillDir, filepath.FromSlash(files.FixtureRel))
		fx, err := skillrun.LoadFixture(fxPath)
		if err != nil {
			return fmt.Errorf("generated fixture smoke failed: %w", err)
		}
		if !toolAllowed([]string{tool}, fx.Tool) {
			return fmt.Errorf("generated fixture tool mismatch: %s", fx.Tool)
		}

		relDir, err := filepath.Rel(*repoDir, files.SkillDir)
		if err != nil {
			return err
		}
		relDir = filepath.ToSlash(relDir)
		if _, err := ex.Run(ctx, skillpkg.Command{Dir: *repoDir, Name: "git", Args: []string{"add", relDir}}); err != nil {
			return fmt.Errorf("git add suggest files: %w", err)
		}
		msg := fmt.Sprintf("suggest: %s from motif %s", files.SkillName, cand.MotifKey[:12])
		prBodyPath, err := writeSuggestPRBody(*runsDir, files.SkillName, cand)
		if err != nil {
			return err
		}
		prBodyRaw, err := os.ReadFile(prBodyPath)
		if err != nil {
			return err
		}
		if _, err := ex.Run(ctx, skillpkg.Command{Dir: *repoDir, Name: "git", Args: []string{"commit", "-m", msg, "-m", strings.TrimSpace(string(prBodyRaw))}}); err != nil {
			return fmt.Errorf("git commit suggest files: %w", err)
		}
		headSHA, err := gitHeadSHA(ctx, ex, *repoDir)
		if err != nil {
			return err
		}
		prHint := fmt.Sprintf("%s pr create --title %q --body-file %q --head %q", *ghBin, msg, filepath.ToSlash(prBodyPath), branch)
		if *openPR {
			if has, _ := hasCommand(ctx, ex, *repoDir, *ghBin); has {
				if _, err := ex.Run(ctx, skillpkg.Command{Dir: *repoDir, Name: *ghBin, Args: []string{"pr", "create", "--title", msg, "--body-file", prBodyPath, "--head", branch}}); err != nil {
					return fmt.Errorf("open pr: %w", err)
				}
			}
		}
		results = append(results, map[string]any{
			"skill":          files.SkillName,
			"branch":         branch,
			"commit":         headSHA,
			"motif_key":      cand.MotifKey,
			"run_ids":        cand.RunIDs,
			"pr_body_path":   filepath.ToSlash(prBodyPath),
			"expected_value": cand.ExpectedReuseValue,
			"next": map[string]any{
				"pr_create": prHint,
			},
		})
	}
	out := map[string]any{
		"triggered": results,
		"motifs":    cands,
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
	return nil
}

func loadMotifFeatures(db *sql.DB, runsDir, skillsDir string) ([]skillmotif.RunFeature, error) {
	rows, err := db.Query(`
SELECT s.run_id,s.skill,s.score,s.pass,r.cost_est
FROM scores s
JOIN runs r ON r.id=s.run_id
WHERE r.task='skill:run'
ORDER BY s.created_at,s.run_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []skillmotif.RunFeature
	for rows.Next() {
		var (
			runID string
			skill string
			score float64
			pass  int
			cost  float64
		)
		if err := rows.Scan(&runID, &skill, &score, &pass, &cost); err != nil {
			return nil, err
		}
		tcs, err := loadToolCalls(db, runID)
		if err != nil {
			return nil, err
		}
		arts, err := loadArtifacts(db, runID)
		if err != nil {
			return nil, err
		}
		f, err := skillmotif.BuildFeature(skillmotif.BuildInput{
			RunID:     runID,
			Skill:     skill,
			Score:     score,
			Pass:      pass != 0,
			CostEst:   cost,
			ToolCalls: tcs,
			Artifacts: arts,
		}, skillsDir)
		if err != nil {
			return nil, err
		}
		// Blend declared expected files from run metadata into artifact schema hash.
		if expectFiles, err := readExpectFiles(filepath.Join(runsDir, runID, "skill-run.json")); err == nil && len(expectFiles) > 0 {
			extra := make([]skillmotif.ArtifactRow, 0, len(expectFiles))
			for _, p := range expectFiles {
				extra = append(extra, skillmotif.ArtifactRow{Path: "expect:" + filepath.ToSlash(p), SHA256: "meta:expect"})
			}
			ff, err := skillmotif.BuildFeature(skillmotif.BuildInput{
				RunID:     runID,
				Skill:     skill,
				Score:     score,
				Pass:      pass != 0,
				CostEst:   cost,
				ToolCalls: tcs,
				Artifacts: append(arts, extra...),
			}, skillsDir)
			if err == nil {
				f = ff
			}
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func loadToolCalls(db *sql.DB, runID string) ([]skillmotif.ToolCallRow, error) {
	rows, err := db.Query(`SELECT seq,tool,input_hash FROM tool_calls WHERE run_id=? ORDER BY seq,id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []skillmotif.ToolCallRow
	for rows.Next() {
		var row skillmotif.ToolCallRow
		if err := rows.Scan(&row.Seq, &row.Tool, &row.InputHash); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func loadArtifacts(db *sql.DB, runID string) ([]skillmotif.ArtifactRow, error) {
	rows, err := db.Query(`SELECT path,sha256 FROM artifacts WHERE run_id=? ORDER BY path,id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []skillmotif.ArtifactRow
	for rows.Next() {
		var row skillmotif.ArtifactRow
		if err := rows.Scan(&row.Path, &row.SHA256); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func readExpectFiles(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	v, ok := raw["expect_files"].([]any)
	if !ok {
		return nil, nil
	}
	out := make([]string, 0, len(v))
	for _, it := range v {
		if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	sort.Strings(out)
	return out, nil
}

func exemplarToolArgs(runsDir, runID string) (string, map[string]any) {
	reqs, _ := filepath.Glob(filepath.Join(runsDir, runID, "toolio", "*.req.json"))
	sort.Strings(reqs)
	if len(reqs) == 0 {
		return "shell.exec", map[string]any{"cmd": "echo ok"}
	}
	b, err := os.ReadFile(reqs[0])
	if err != nil {
		return "shell.exec", map[string]any{"cmd": "echo ok"}
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return "shell.exec", map[string]any{"cmd": "echo ok"}
	}
	tool := strings.TrimSpace(asString(raw["tool"]))
	if tool == "" {
		tool = "shell.exec"
	}
	args, _ := raw["args"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	if tool == "shell.exec" {
		if _, ok := args["cmd"]; !ok {
			args["cmd"] = "echo ok"
		}
	}
	return tool, args
}

func writeSuggestPRBody(runsDir, skill string, c skillmotif.Candidate) (string, error) {
	id := fmt.Sprintf("%d-suggest", time.Now().UTC().UnixNano())
	dir := filepath.Join(runsDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	body := strings.Join([]string{
		fmt.Sprintf("Suggested skill: %s", skill),
		fmt.Sprintf("Motif key: %s", c.MotifKey),
		fmt.Sprintf("Source skill: %s", c.Skill),
		fmt.Sprintf("Evidence rows (runs): %s", strings.Join(c.RunIDs, ",")),
		fmt.Sprintf("Repeats=%d PassRate=%.4f ScoreP50=%.4f ExpectedReuseValue=%.6f", c.Repeats, c.PassRate, c.ScoreP50, c.ExpectedReuseValue),
		fmt.Sprintf("ToolSeqHash=%s", c.ToolSeqHash),
		fmt.Sprintf("ArtifactSchemaHash=%s", c.ArtifactSchemaHash),
	}, "\n") + "\n"
	path := filepath.Join(dir, "suggest-pr.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func ensureGitCleanForSuggest(ctx context.Context, ex skilleval.OSExec, repoDir, skillDir string) error {
	rel, err := filepath.Rel(repoDir, skillDir)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	res, err := ex.Run(ctx, skillpkg.Command{Dir: repoDir, Name: "git", Args: []string{"status", "--porcelain", "--", rel}})
	if err != nil {
		return fmt.Errorf("git status suggest path: %w", err)
	}
	if strings.TrimSpace(string(res.Stdout)) != "" {
		return fmt.Errorf("target suggestion path already dirty; clean changes before suggest")
	}
	if _, err := os.Stat(skillDir); err == nil {
		return fmt.Errorf("target suggestion path already exists: %s", rel)
	}
	return nil
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprint(v)
	}
}
