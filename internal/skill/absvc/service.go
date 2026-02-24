package absvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/haris/virmux/internal/skill/eval"
	"github.com/haris/virmux/internal/skill/evidence"
	"github.com/haris/virmux/internal/store"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type EvalStore interface {
	InsertEvalRun(context.Context, store.EvalRun) error
	InsertEvalCase(context.Context, store.EvalCase) error
	InsertExperiment(context.Context, store.Experiment) error
	InsertComparison(context.Context, store.Comparison) error
}

type Service struct {
	Store EvalStore
	Exec  eval.Exec
	Now   func() time.Time
}

type Input struct {
	RepoDir      string
	SkillsDir    string
	RunsDir      string
	SkillName    string
	BaseRef      string
	HeadRef      string
	Provider     string
	PromptfooBin string
	Cohort       string

	ScoreMin   float64
	FailMax    float64
	CostMax    float64
	CostGate   bool
	TimeoutSec int

	JudgeMode string // "pairwise", "independent"
	AntiTie   bool
}

type Result struct {
	EvalID      string
	Skill       string
	Cohort      string
	Pass        bool
	Reason      string
	BaseRef     string
	HeadRef     string
	EvalDir     string
	VerdictPath string
	ScoreDelta  float64
	FailDelta   float64
	CostDelta   float64
	BaseScore   float64
	HeadScore   float64
	BaseFail    float64
	HeadFail    float64

	ExperimentID string  `json:"experiment_id,omitempty"`
	Winner       string  `json:"winner,omitempty"` // "A", "B", "tie"
	WinRate      float64 `json:"win_rate,omitempty"`
}

func (s Service) Run(ctx context.Context, in Input) (Result, error) {
	if s.Store == nil {
		return Result{}, errors.New("ab store required")
	}
	if strings.TrimSpace(in.SkillName) == "" {
		return Result{}, errors.New("skill name cannot be empty")
	}
	if strings.TrimSpace(in.BaseRef) == "" || strings.TrimSpace(in.HeadRef) == "" {
		return Result{}, errors.New("base/head refs required")
	}
	if in.BaseRef == in.HeadRef {
		return Result{}, errors.New("base and head refs must differ")
	}
	if in.TimeoutSec <= 0 {
		in.TimeoutSec = 120
	}
	switch strings.TrimSpace(in.JudgeMode) {
	case "", "independent":
		in.JudgeMode = "independent"
	case "pairwise":
	default:
		return Result{}, fmt.Errorf("invalid --judge mode %q (allowed: independent|pairwise)", in.JudgeMode)
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	ex := s.Exec
	if ex == nil {
		ex = eval.OSExec{}
	}
	cohort := strings.TrimSpace(in.Cohort)
	if cohort == "" {
		cohort = "qa-skill-c3-" + now().UTC().Format("20060102")
	}
	evalID := fmt.Sprintf("%d-skillab", now().UTC().UnixNano())
	evalDir := filepath.Join(in.RunsDir, evalID)
	if err := os.MkdirAll(evalDir, 0o755); err != nil {
		return Result{}, err
	}

	headSnap, err := eval.LoadSkillSnapshot(ctx, ex, in.RepoDir, in.SkillsDir, in.SkillName, in.HeadRef)
	if err != nil {
		return Result{}, err
	}
	baseSnap, err := eval.LoadSkillSnapshot(ctx, ex, in.RepoDir, in.SkillsDir, in.SkillName, in.BaseRef)
	if err != nil {
		return Result{}, err
	}
	if err := eval.ValidateFrozenFixtureSet(headSnap.Fixtures, baseSnap.Fixtures); err != nil {
		return Result{}, fmt.Errorf("frozen fixture set violation: %w", err)
	}
	baseSnapFrozen := baseSnap
	baseSnapFrozen.Fixtures = append([]eval.Fixture(nil), headSnap.Fixtures...)

	baseCfgPath := filepath.Join(evalDir, "promptfoo.base.json")
	headCfgPath := filepath.Join(evalDir, "promptfoo.head.json")
	baseCfg, err := eval.BuildPromptfooConfig(baseSnapFrozen, in.Provider)
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(baseCfgPath, baseCfg, 0o644); err != nil {
		return Result{}, err
	}
	headCfg, err := eval.BuildPromptfooConfig(headSnap, in.Provider)
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(headCfgPath, headCfg, 0o644); err != nil {
		return Result{}, err
	}

	baseResPath := filepath.Join(evalDir, "promptfoo.base.results.json")
	headResPath := filepath.Join(evalDir, "promptfoo.head.results.json")
	timeout := time.Duration(in.TimeoutSec) * time.Second
	if err := eval.RunPromptfoo(ctx, ex, in.RepoDir, in.PromptfooBin, baseCfgPath, baseResPath, timeout); err != nil {
		return Result{}, err
	}
	if err := eval.RunPromptfoo(ctx, ex, in.RepoDir, in.PromptfooBin, headCfgPath, headResPath, timeout); err != nil {
		return Result{}, err
	}
	baseRaw, err := os.ReadFile(baseResPath)
	if err != nil {
		return Result{}, err
	}
	headRaw, err := os.ReadFile(headResPath)
	if err != nil {
		return Result{}, err
	}

	cfgBundlePath := filepath.Join(evalDir, "promptfoo.cfg.bundle.json")
	cfgBundleBytes, err := evidence.WriteJSONFile(cfgBundlePath, map[string]any{
		"base_cfg_path": filepath.ToSlash(filepath.Join(evalID, "promptfoo.base.json")),
		"head_cfg_path": filepath.ToSlash(filepath.Join(evalID, "promptfoo.head.json")),
		"base":          baseCfg,
		"head":          headCfg,
	})
	if err != nil {
		return Result{}, err
	}
	resultsBundlePath := filepath.Join(evalDir, "promptfoo.results.bundle.json")
	resultsBundleBytes, err := evidence.WriteJSONFile(resultsBundlePath, map[string]any{
		"base_results_path": filepath.ToSlash(filepath.Join(evalID, "promptfoo.base.results.json")),
		"head_results_path": filepath.ToSlash(filepath.Join(evalID, "promptfoo.head.results.json")),
		"base":              headOrRaw(baseRaw),
		"head":              headOrRaw(headRaw),
	})
	if err != nil {
		return Result{}, err
	}

	ids := make([]string, 0, len(headSnap.Fixtures))
	for _, fx := range headSnap.Fixtures {
		ids = append(ids, fx.ID)
	}
	baseMetrics, baseCases, err := eval.ParsePromptfooResults(baseRaw, ids)
	if err != nil {
		return Result{}, err
	}
	headMetrics, headCases, err := eval.ParsePromptfooResults(headRaw, ids)
	if err != nil {
		return Result{}, err
	}

	var maxCost *float64
	if in.CostGate {
		maxCost = &in.CostMax
	}
	verdict := eval.CompareAB(baseMetrics, headMetrics, eval.ABThresholds{
		MinScoreDelta:    in.ScoreMin,
		MaxFailRateDelta: in.FailMax,
		MaxCostDelta:     maxCost,
	})
	verdictPath := filepath.Join(evalDir, "ab-verdict.json")
	verdictDoc := map[string]any{
		"id":      evalID,
		"skill":   in.SkillName,
		"cohort":  cohort,
		"base":    map[string]any{"ref": in.BaseRef, "metrics": baseMetrics},
		"head":    map[string]any{"ref": in.HeadRef, "metrics": headMetrics},
		"verdict": verdict,
		"gates": map[string]any{
			"score_delta_min":     in.ScoreMin,
			"fail_rate_delta_max": in.FailMax,
			"cost_delta_max":      maxCost,
		},
	}
	verdictBytes, err := evidence.WriteJSONFile(verdictPath, verdictDoc)
	if err != nil {
		return Result{}, err
	}

	if err := s.Store.InsertEvalRun(ctx, store.EvalRun{
		ID:            evalID,
		Skill:         in.SkillName,
		Cohort:        cohort,
		BaseRef:       in.BaseRef,
		HeadRef:       in.HeadRef,
		Provider:      in.Provider,
		FixturesHash:  eval.FixtureSetHash(headSnap.Fixtures),
		CfgSHA256:     evidence.SHA256Hex(cfgBundleBytes),
		CfgPath:       filepath.ToSlash(filepath.Join(evalID, "promptfoo.cfg.bundle.json")),
		ResultsSHA256: evidence.SHA256Hex(resultsBundleBytes),
		ResultsPath:   filepath.ToSlash(filepath.Join(evalID, "promptfoo.results.bundle.json")),
		VerdictSHA256: evidence.SHA256Hex(verdictBytes),
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
		VerdictJSON:   string(verdictBytes),
		CreatedAt:     now().UTC(),
	}); err != nil {
		return Result{}, err
	}
	for _, id := range ids {
		bc := baseCases[id]
		hc := headCases[id]
		if err := s.Store.InsertEvalCase(ctx, store.EvalCase{
			EvalRunID: evalID,
			FixtureID: id,
			BaseScore: bc.Score,
			HeadScore: hc.Score,
			BasePass:  bc.Pass,
			HeadPass:  hc.Pass,
			BaseCost:  bc.Cost,
			HeadCost:  hc.Cost,
			CreatedAt: now().UTC(),
		}); err != nil {
			return Result{}, err
		}
	}

	res := Result{
		EvalID:      evalID,
		Skill:       in.SkillName,
		Cohort:      cohort,
		Pass:        verdict.Pass,
		Reason:      verdict.Reason,
		BaseRef:     in.BaseRef,
		HeadRef:     in.HeadRef,
		EvalDir:     filepath.ToSlash(evalDir),
		VerdictPath: filepath.ToSlash(verdictPath),
		ScoreDelta:  verdict.ScoreDelta,
		FailDelta:   verdict.FailRateDelta,
		CostDelta:   verdict.CostDelta,
		BaseScore:   baseMetrics.ScoreP50,
		HeadScore:   headMetrics.ScoreP50,
		BaseFail:    baseMetrics.FailRate,
		HeadFail:    headMetrics.FailRate,
	}

	if in.JudgeMode == "pairwise" {
		expID := fmt.Sprintf("%d-exp", now().UTC().UnixNano())
		if err := s.Store.InsertExperiment(ctx, store.Experiment{
			ID:        expID,
			EvalRunID: evalID,
			Skill:     in.SkillName,
			Cohort:    cohort,
			BaseRef:   in.BaseRef,
			HeadRef:   in.HeadRef,
			JudgeMode: in.JudgeMode,
			CreatedAt: now().UTC(),
		}); err != nil {
			return Result{}, err
		}

		var winsA, winsB, ties int
		for _, id := range ids {
			bc := baseCases[id]
			hc := headCases[id]
			winner := "tie"
			rationale := "both hard-fail"

			if hc.Score > bc.Score {
				winner = "B"
				rationale = "head score higher"
			} else if bc.Score > hc.Score {
				winner = "A"
				rationale = "base score higher"
			} else {
				// Scores equal
				if hc.Pass && !bc.Pass {
					winner = "B"
					rationale = "head passed, base failed"
				} else if bc.Pass && !hc.Pass {
					winner = "A"
					rationale = "base passed, head failed"
				} else if hc.Pass && bc.Pass {
					if in.AntiTie {
						winner = "B"
						rationale = "anti-tie favor head"
					} else {
						winner = "A"
						rationale = "tie-break favor base"
					}
				}
			}

			if winner == "A" {
				winsA++
			} else if winner == "B" {
				winsB++
			} else {
				ties++
			}

			if err := s.Store.InsertComparison(ctx, store.Comparison{
				ExperimentID: expID,
				FixtureID:    id,
				Winner:       winner,
				Rationale:    rationale,
				CreatedAt:    now().UTC(),
			}); err != nil {
				return Result{}, err
			}
		}
		res.ExperimentID = expID
		if len(ids) > 0 {
			res.WinRate = float64(winsB) / float64(len(ids))
		}
		if winsB > winsA {
			res.Winner = "B"
		} else if winsA > winsB {
			res.Winner = "A"
		} else {
			res.Winner = "tie"
		}
	}

	return res, nil
}

func headOrRaw(raw []byte) any {
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err == nil {
		return parsed
	}
	return string(raw)
}
