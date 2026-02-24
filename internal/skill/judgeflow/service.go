package judgeflow

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

	skilljudge "github.com/haris/virmux/internal/skill/judge"
	"github.com/haris/virmux/internal/skill/rules"
	skillrun "github.com/haris/virmux/internal/skill/run"
	"github.com/haris/virmux/internal/store"
)

type EmitFn func(context.Context, string, map[string]any) error

type PersistArtifactsFn func(context.Context, string, []string) error

type Service struct {
	Emit             EmitFn
	PersistArtifacts PersistArtifactsFn
	InsertScore      func(context.Context, store.Score) error
	InsertJudgeRun   func(context.Context, store.JudgeRun) error
	Now              func() time.Time
	DBPath           string
	RunsDir          string
}

type Input struct {
	RunID      string
	RunDir     string
	RunTask    string
	RunStatus  string
	Skill      string
	RubricPath string
	RubricHash string
	Rubric     skilljudge.Rubric
	ToolCalls  int
	ExpectFile []string
	Mode       string // rules, llm_abs, llm_probe
	ModelID    string
	ReadOnly   bool
}

type Result struct {
	Judge     skilljudge.Result
	ScorePath string
}

func (s Service) Run(ctx context.Context, in Input) (Result, error) {
	mode := strings.TrimSpace(in.Mode)
	if mode == "" {
		mode = "rules"
		in.Mode = mode
	}
	if mode != "rules" {
		return Result{}, fmt.Errorf("JUDGE_INVALID: unsupported judge mode %q (only rules mode is implemented)", mode)
	}
	if !in.ReadOnly && (s.Emit == nil || s.InsertScore == nil || s.InsertJudgeRun == nil || s.PersistArtifacts == nil) {
		return Result{}, errors.New("judgeflow missing required ports")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	if !in.ReadOnly {
		if err := s.Emit(ctx, "skill.judge.started", map[string]any{
			"skill":       in.Skill,
			"rubric":      filepath.ToSlash(in.RubricPath),
			"rubric_hash": in.RubricHash,
		}); err != nil {
			return Result{}, err
		}
	}

	ev := skilljudge.Evidence{
		RunID:       in.RunID,
		Skill:       in.Skill,
		RunDir:      in.RunDir,
		RunStatus:   in.RunStatus,
		ToolCalls:   in.ToolCalls,
		ExpectFiles: in.ExpectFile,
		Mode:        in.Mode,
		ModelID:     in.ModelID,
	}

	ruleEngine := &rules.Engine{
		DBPath:  s.DBPath,
		RunsDir: s.RunsDir,
	}
	ruleResults, err := ruleEngine.Evaluate(ctx, ev)
	if err != nil {
		return Result{}, fmt.Errorf("rule evaluation failed: %w", err)
	}

	res, err := skilljudge.Evaluate(in.Rubric, in.RubricHash, ev)
	if err != nil {
		return Result{}, err
	}

	for _, rr := range ruleResults {
		matched := false
		for i, c := range res.Criterion {
			if c.ID != rr.ID {
				continue
			}
			res.Criterion[i].Value = rr.Value
			res.Criterion[i].Pass = rr.Pass
			res.Criterion[i].Reason = rr.Reason
			matched = true
			break
		}
		if !matched {
			res.Criterion = append(res.Criterion, skilljudge.CriterionScore{
				ID:     rr.ID,
				Weight: 0,
				Value:  rr.Value,
				Pass:   rr.Pass,
				Must:   true,
				Reason: rr.Reason,
			})
		}
		if !rr.Pass {
			if strings.TrimSpace(rr.Reason) != "" {
				res.Critique = append(res.Critique, rr.Reason)
			} else {
				res.Critique = append(res.Critique, rr.ID+" failed")
			}
		}
	}
	// Recalculate terminal score/pass after rule overrides.
	res.Score = 0
	mustOK := true
	for _, c := range res.Criterion {
		res.Score += c.Weight * c.Value
		if c.Must && !c.Pass {
			mustOK = false
		}
	}
	res.Pass = res.Score >= in.Rubric.Pass && mustOK
	sort.Strings(res.Critique)
	res.Critique = dedupe(res.Critique)

	scoreDoc := map[string]any{
		"version":        skilljudge.SchemaVersion,
		"run_id":         res.RunID,
		"skill":          res.Skill,
		"score":          res.Score,
		"pass":           res.Pass,
		"critique":       res.Critique,
		"criterion":      res.Criterion,
		"rubric_hash":    res.RubricHash,
		"judge_cfg_hash": res.JudgeCfgHash,
		"artifact_hash":  res.ArtifactHash,
		"mode":           res.Mode,
		"model_id":       res.ModelID,
	}
	scoreJSON, err := json.Marshal(scoreDoc)
	if err != nil {
		return Result{}, err
	}
	if _, err := skilljudge.ValidateOutput(scoreJSON); err != nil {
		return Result{}, fmt.Errorf("JUDGE_INVALID: %w", err)
	}
	if in.ReadOnly {
		return Result{Judge: res}, nil
	}

	scorePath, err := skillrun.EnsureScorePlaceholder(in.RunDir, scoreDoc)
	if err != nil {
		return Result{}, err
	}
	metricsJSON, _ := json.Marshal(CriteriaMap(res.Criterion))
	critiqueJSON, _ := json.Marshal(res.Critique)
	ts := now().UTC()
	if err := s.InsertScore(ctx, store.Score{
		RunID:        in.RunID,
		Skill:        in.Skill,
		Score:        res.Score,
		Pass:         res.Pass,
		Critique:     string(critiqueJSON),
		JudgeCfgHash: res.JudgeCfgHash,
		ArtifactHash: res.ArtifactHash,
		CreatedAt:    ts,
	}); err != nil {
		return Result{}, err
	}
	if err := s.InsertJudgeRun(ctx, store.JudgeRun{
		RunID:        in.RunID,
		Skill:        in.Skill,
		RubricHash:   res.RubricHash,
		JudgeCfgHash: res.JudgeCfgHash,
		ArtifactHash: res.ArtifactHash,
		MetricsJSON:  string(metricsJSON),
		Critique:     string(critiqueJSON),
		Score:        res.Score,
		Pass:         res.Pass,
		CreatedAt:    ts,
		Mode:         res.Mode,
		ModelID:      res.ModelID,
		PromptHash:   res.PromptHash,
		SchemaVer:    res.SchemaVer,
	}); err != nil {
		return Result{}, err
	}
	if err := s.Emit(ctx, "skill.judge.scored", map[string]any{
		"skill":          in.Skill,
		"score":          res.Score,
		"pass":           res.Pass,
		"critique":       res.Critique,
		"criterion":      res.Criterion,
		"score_path":     filepath.ToSlash(filepath.Base(scorePath)),
		"rubric_hash":    res.RubricHash,
		"judge_cfg_hash": res.JudgeCfgHash,
		"artifact_hash":  res.ArtifactHash,
	}); err != nil {
		return Result{}, err
	}
	if err := s.PersistArtifacts(ctx, in.RunID, []string{scorePath}); err != nil {
		return Result{}, err
	}
	return Result{Judge: res, ScorePath: scorePath}, nil
}

type RunMeta struct {
	Skill         string         `json:"skill"`
	Fixture       string         `json:"fixture"`
	Tool          string         `json:"tool"`
	Deterministic bool           `json:"deterministic"`
	Expect        map[string]any `json:"expect,omitempty"`
	ToolCalls     int            `json:"tool_calls,omitempty"`
	ExpectFiles   []string       `json:"expect_files,omitempty"`
}

func ReadRunMeta(runDir string) (RunMeta, error) {
	b, err := os.ReadFile(filepath.Join(runDir, "skill-run.json"))
	if err != nil {
		return RunMeta{}, fmt.Errorf("read skill-run.json: %w", err)
	}
	var meta RunMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return RunMeta{}, fmt.Errorf("parse skill-run.json: %w", err)
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

func CriteriaMap(in []skilljudge.CriterionScore) map[string]float64 {
	out := make(map[string]float64, len(in))
	for _, c := range in {
		out[c.ID] = c.Value
	}
	return out
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
