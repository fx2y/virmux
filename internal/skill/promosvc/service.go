package promosvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	skillpkg "github.com/haris/virmux/internal/skill"
	"github.com/haris/virmux/internal/skill/eval"
	"github.com/haris/virmux/internal/skill/gates"
	"github.com/haris/virmux/internal/store"
)

type Store interface {
	GetEvalRun(context.Context, string) (store.EvalRun, error)
	InsertPromotion(context.Context, store.Promotion) error
}

type Service struct {
	Store Store
	Exec  eval.Exec
	Now   func() time.Time
	User  func(string) string
}

type Input struct {
	SkillName   string
	EvalRunID   string
	RepoDir     string
	Tag         string
	Actor       string
	MaxAgeHours int
	Rollback    bool
	ToRef       string // Target ref for rollback
	Reason      string
	DryRun      bool
}

type Result struct {
	ID        string
	Skill     string
	Tag       string
	EvalRunID string
	BaseRef   string
	HeadRef   string
	FromRef   string
	ToRef     string
	Actor     string
	Op        string
}

func (s Service) Run(ctx context.Context, in Input) (Result, error) {
	if s.Store == nil {
		return Result{}, errors.New("promote store required")
	}
	if strings.TrimSpace(in.SkillName) == "" {
		return Result{}, errors.New("skill is required")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	ex := s.Exec
	if ex == nil {
		ex = eval.OSExec{}
	}

	promoTag := strings.TrimSpace(in.Tag)
	if promoTag == "" {
		promoTag = fmt.Sprintf("skill/%s/prod", in.SkillName)
	}

	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		if s.User != nil {
			actor = strings.TrimSpace(s.User("USER"))
		}
	}

	op := "promote"
	var evalRun store.EvalRun
	var fromRef, toRef, commitSHA, baseRef, headRef string
	var metricsJSON string
	var verdictSHA string

	if in.Rollback {
		op = "rollback"
		if in.ToRef == "" {
			return Result{}, errors.New("rollback requires --to-ref")
		}
		toRef = in.ToRef

		// Optional: Link rollback to an eval run if provided
		if in.EvalRunID != "" {
			var err error
			evalRun, err = s.Store.GetEvalRun(ctx, in.EvalRunID)
			if err != nil {
				return Result{}, fmt.Errorf("MISSING_AB_VERDICT (rollback link): %w", err)
			}
		}

		// Get current ref for fromRef
		res, err := resolveRef(ctx, ex, in.RepoDir, promoTag)
		if err != nil {
			return Result{}, fmt.Errorf("resolve current promo tag %s: %w", promoTag, err)
		}
		fromRef = strings.TrimSpace(string(res.Stdout))
		if fromRef == "" {
			return Result{}, fmt.Errorf("resolve current promo tag %s: empty ref", promoTag)
		}
		baseRef = fromRef
		headRef = toRef

		// Resolve toRef to commit SHA
		res, err = resolveRef(ctx, ex, in.RepoDir, toRef)
		if err != nil {
			return Result{}, fmt.Errorf("resolve rollback target %s: %w", toRef, err)
		}
		commitSHA = strings.TrimSpace(string(res.Stdout))
		if commitSHA == "" {
			return Result{}, fmt.Errorf("resolve rollback target %s: empty commit", toRef)
		}
	} else {
		if strings.TrimSpace(in.EvalRunID) == "" {
			return Result{}, errors.New("promote requires --eval-run-id")
		}
		var err error
		evalRun, err = s.Store.GetEvalRun(ctx, in.EvalRunID)
		if err != nil {
			return Result{}, fmt.Errorf("MISSING_AB_VERDICT: %w", err)
		}
		if evalRun.Skill != in.SkillName {
			return Result{}, fmt.Errorf("MISSING_AB_VERDICT: eval run skill=%s does not match %s", evalRun.Skill, in.SkillName)
		}

		verdict := gates.GateEval(evalRun)
		if !verdict.Pass {
			return Result{}, fmt.Errorf("%s: %s", verdict.Refusal, verdict.Reason)
		}

		maxAge := time.Duration(in.MaxAgeHours) * time.Hour
		if maxAge > 0 && !evalRun.CreatedAt.IsZero() && now().Sub(evalRun.CreatedAt) > maxAge {
			return Result{}, fmt.Errorf("STALE_AB_VERDICT: eval run %s older than %s", in.EvalRunID, maxAge)
		}

		fromRef = evalRun.BaseRef
		toRef = evalRun.HeadRef
		baseRef = evalRun.BaseRef
		headRef = evalRun.HeadRef
		res, err := resolveRef(ctx, ex, in.RepoDir, toRef)
		if err != nil {
			return Result{}, fmt.Errorf("resolve promote target %s: %w", toRef, err)
		}
		commitSHA = strings.TrimSpace(string(res.Stdout))
		if commitSHA == "" {
			return Result{}, fmt.Errorf("resolve promote target %s: empty commit", toRef)
		}
		metricsJSON = evalRun.VerdictJSON
		verdictSHA = evalRun.VerdictSHA256
	}

	// Execute git tag move
	if !in.DryRun {
		if _, err := ex.Run(ctx, skillpkg.Command{
			Dir:  in.RepoDir,
			Name: "git",
			Args: []string{"tag", "-f", promoTag, commitSHA},
		}); err != nil {
			return Result{}, fmt.Errorf("failed to move tag %s to %s: %w", promoTag, commitSHA, err)
		}
	}

	promoID := fmt.Sprintf("%d-%s", now().UTC().UnixNano(), op)
	if in.DryRun {
		promoID += "-dryrun"
	}

	if !in.DryRun {
		if err := s.Store.InsertPromotion(ctx, store.Promotion{
			ID:            promoID,
			Skill:         in.SkillName,
			Tag:           promoTag,
			BaseRef:       baseRef,
			HeadRef:       headRef,
			FromRef:       fromRef,
			ToRef:         toRef,
			Reason:        in.Reason,
			MetricsJSON:   metricsJSON,
			CommitSHA:     commitSHA,
			Op:            op,
			EvalRunID:     evalRun.ID,
			VerdictSHA256: verdictSHA,
			Actor:         actor,
			CreatedAt:     now().UTC(),
		}); err != nil {
			return Result{}, err
		}
	}

	return Result{
		ID:        promoID,
		Skill:     in.SkillName,
		Tag:       promoTag,
		EvalRunID: evalRun.ID,
		BaseRef:   baseRef,
		HeadRef:   headRef,
		FromRef:   fromRef,
		ToRef:     toRef,
		Actor:     actor,
		Op:        op,
	}, nil
}

func resolveRef(ctx context.Context, ex eval.Exec, repoDir, ref string) (skillpkg.CommandResult, error) {
	cands := []string{ref}
	if !strings.HasPrefix(ref, "refs/") {
		cands = append(cands, "refs/tags/"+ref)
	}
	var lastErr error
	for _, cand := range cands {
		res, err := ex.Run(ctx, skillpkg.Command{
			Dir:  repoDir,
			Name: "git",
			Args: []string{"rev-parse", cand},
		})
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	return skillpkg.CommandResult{}, lastErr
}
