package promosvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	skillpkg "github.com/haris/virmux/internal/skill"
	"github.com/haris/virmux/internal/skill/eval"
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
}

type Result struct {
	ID        string
	Skill     string
	Tag       string
	EvalRunID string
	BaseRef   string
	HeadRef   string
	Actor     string
}

func (s Service) Run(ctx context.Context, in Input) (Result, error) {
	if s.Store == nil {
		return Result{}, errors.New("promote store required")
	}
	if strings.TrimSpace(in.SkillName) == "" || strings.TrimSpace(in.EvalRunID) == "" {
		return Result{}, errors.New("skill and eval-run-id are required")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	ex := s.Exec
	if ex == nil {
		ex = eval.OSExec{}
	}
	row, err := s.Store.GetEvalRun(ctx, in.EvalRunID)
	if err != nil {
		return Result{}, fmt.Errorf("MISSING_AB_VERDICT: %w", err)
	}
	if row.Skill != in.SkillName {
		return Result{}, fmt.Errorf("MISSING_AB_VERDICT: eval run skill=%s does not match %s", row.Skill, in.SkillName)
	}
	if !row.Pass {
		return Result{}, fmt.Errorf("MISSING_AB_VERDICT: eval run %s is not passing", in.EvalRunID)
	}
	maxAge := time.Duration(in.MaxAgeHours) * time.Hour
	if maxAge > 0 && !row.CreatedAt.IsZero() && now().Sub(row.CreatedAt) > maxAge {
		return Result{}, fmt.Errorf("STALE_AB_VERDICT: eval run %s older than %s", in.EvalRunID, maxAge)
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
	if _, err := ex.Run(ctx, skillpkg.Command{
		Dir:  in.RepoDir,
		Name: "git",
		Args: []string{"tag", "-f", promoTag, row.HeadRef},
	}); err != nil {
		return Result{}, fmt.Errorf("move promotion tag: %w", err)
	}
	promoID := fmt.Sprintf("%d-promote", now().UTC().UnixNano())
	if err := s.Store.InsertPromotion(ctx, store.Promotion{
		ID:            promoID,
		Skill:         in.SkillName,
		Tag:           promoTag,
		BaseRef:       row.BaseRef,
		HeadRef:       row.HeadRef,
		EvalRunID:     row.ID,
		VerdictSHA256: row.VerdictSHA256,
		Actor:         actor,
		CreatedAt:     now().UTC(),
	}); err != nil {
		return Result{}, err
	}
	return Result{
		ID:        promoID,
		Skill:     in.SkillName,
		Tag:       promoTag,
		EvalRunID: row.ID,
		BaseRef:   row.BaseRef,
		HeadRef:   row.HeadRef,
		Actor:     actor,
	}, nil
}
