package promosvc

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	skillpkg "github.com/haris/virmux/internal/skill"
	"github.com/haris/virmux/internal/store"
)

type fakeExec struct {
	last skillpkg.Command
}

func (f *fakeExec) Run(_ context.Context, c skillpkg.Command) (skillpkg.CommandResult, error) {
	f.last = c
	if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "rev-parse" {
		return skillpkg.CommandResult{ExitCode: 0, Stdout: []byte("sha-abc\n")}, nil
	}
	return skillpkg.CommandResult{ExitCode: 0}, nil
}

type failingExec struct {
	failRef string
}

func (f failingExec) Run(_ context.Context, c skillpkg.Command) (skillpkg.CommandResult, error) {
	if c.Name == "git" && len(c.Args) > 1 && c.Args[0] == "rev-parse" &&
		(c.Args[1] == f.failRef || c.Args[1] == "refs/tags/"+f.failRef) {
		return skillpkg.CommandResult{ExitCode: 1}, fmt.Errorf("forced rev-parse failure for %s", f.failRef)
	}
	return (&fakeExec{}).Run(context.Background(), c)
}

func seedEvalRun(t *testing.T, dbPath, id string, created time.Time, pass bool, verdictJSON string) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if verdictJSON == "" {
		verdictJSON = `{"pass":true}`
	}
	if err := st.InsertEvalRun(context.Background(), store.EvalRun{
		ID:            id,
		Skill:         "dd",
		Cohort:        "qa",
		BaseRef:       "base",
		HeadRef:       "head",
		Provider:      "openai:gpt-4.1-mini",
		FixturesHash:  "sha256:fx",
		CfgSHA256:     "sha256:cfg",
		ResultsSHA256: "sha256:res",
		VerdictSHA256: "sha256:verdict",
		VerdictJSON:   verdictJSON,
		Pass:          pass,
		CreatedAt:     created,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestServiceRunRefusesStaleVerdict(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dbPath := tmp + "/runs/virmux.sqlite"
	seedEvalRun(t, dbPath, "ab-stale", time.Now().UTC().Add(-48*time.Hour), true, "")

	s := Service{Store: mustOpenStore(t, dbPath), Exec: &fakeExec{}, Now: time.Now}
	_, err := s.Run(context.Background(), Input{SkillName: "dd", EvalRunID: "ab-stale", MaxAgeHours: 24})
	if err == nil || !strings.Contains(err.Error(), "STALE_AB_VERDICT") {
		t.Fatalf("expected stale refusal, got %v", err)
	}
}

func TestServiceRunRefusesRegression(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dbPath := tmp + "/runs/virmux.sqlite"
	seedEvalRun(t, dbPath, "ab-fail", time.Now().UTC(), false, `{"pass":false,"hard":{"fail_rate":false}}`)

	st := mustOpenStore(t, dbPath)
	defer st.Close()
	s := Service{Store: st, Exec: &fakeExec{}, Now: time.Now}
	_, err := s.Run(context.Background(), Input{SkillName: "dd", EvalRunID: "ab-fail"})
	if err == nil || !strings.Contains(err.Error(), "AB_REGRESSION") {
		t.Fatalf("expected regression refusal, got %v", err)
	}
}

func TestServiceRunRollback(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dbPath := tmp + "/runs/virmux.sqlite"
	st := mustOpenStore(t, dbPath)
	defer st.Close()
	ex := &fakeExec{}
	s := Service{Store: st, Exec: ex, Now: func() time.Time { return time.Unix(1700000001, 0).UTC() }}
	res, err := s.Run(context.Background(), Input{
		SkillName: "dd",
		Rollback:  true,
		ToRef:     "tag-v1",
		RepoDir:   tmp,
		Reason:    "bug in head",
	})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if res.Op != "rollback" {
		t.Fatalf("expected op=rollback, got %s", res.Op)
	}
	if res.ToRef != "tag-v1" {
		t.Fatalf("target mismatch: %s", res.ToRef)
	}

	var row store.Promotion
	if err := st.DB().QueryRow(`SELECT id, op, from_ref, to_ref, reason FROM promotions WHERE id=?`, res.ID).Scan(&row.ID, &row.Op, &row.FromRef, &row.ToRef, &row.Reason); err != nil {
		t.Fatal(err)
	}
	if row.Op != "rollback" || row.ToRef != "tag-v1" || row.Reason != "bug in head" {
		t.Fatalf("db row mismatch: %+v", row)
	}
}

func TestServiceRunRollbackFailsWhenCurrentTagResolveFails(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dbPath := tmp + "/runs/virmux.sqlite"
	st := mustOpenStore(t, dbPath)
	defer st.Close()
	s := Service{
		Store: st,
		Exec:  failingExec{failRef: "skill/dd/prod"},
		Now:   func() time.Time { return time.Unix(1700000001, 0).UTC() },
	}
	_, err := s.Run(context.Background(), Input{
		SkillName: "dd",
		Rollback:  true,
		ToRef:     "tag-v1",
		RepoDir:   tmp,
		Reason:    "rollback",
	})
	if err == nil || !strings.Contains(err.Error(), "resolve current promo tag") {
		t.Fatalf("expected resolve current promo tag failure, got %v", err)
	}
}

func TestServiceRunWritesPromotionAndMovesTag(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dbPath := tmp + "/runs/virmux.sqlite"
	seedEvalRun(t, dbPath, "ab-pass", time.Now().UTC(), true, "")
	st := mustOpenStore(t, dbPath)
	defer st.Close()
	ex := &fakeExec{}
	s := Service{Store: st, Exec: ex, Now: func() time.Time { return time.Unix(1700000001, 0).UTC() }}
	res, err := s.Run(context.Background(), Input{SkillName: "dd", EvalRunID: "ab-pass", RepoDir: tmp, MaxAgeHours: 24})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Tag != "skill/dd/prod" {
		t.Fatalf("tag mismatch: %s", res.Tag)
	}
	if ex.last.Name != "git" || len(ex.last.Args) < 4 || ex.last.Args[0] != "tag" {
		t.Fatalf("unexpected git command: %+v", ex.last)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM promotions WHERE id=?`, res.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected one promotion row, got %d", n)
	}
	var commitSHA string
	if err := st.DB().QueryRow(`SELECT commit_sha FROM promotions WHERE id=?`, res.ID).Scan(&commitSHA); err != nil {
		t.Fatal(err)
	}
	if commitSHA != "sha-abc" {
		t.Fatalf("expected resolved commit_sha, got %s", commitSHA)
	}
}

func mustOpenStore(t *testing.T, dbPath string) *store.Store {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
