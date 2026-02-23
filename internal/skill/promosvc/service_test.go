package promosvc

import (
	"context"
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
	return skillpkg.CommandResult{ExitCode: 0}, nil
}

func seedEvalRun(t *testing.T, dbPath, id string, created time.Time) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
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
		VerdictJSON:   `{"pass":true}`,
		Pass:          true,
		CreatedAt:     created,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestServiceRunRefusesStaleVerdict(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dbPath := tmp + "/runs/virmux.sqlite"
	seedEvalRun(t, dbPath, "ab-stale", time.Now().UTC().Add(-48*time.Hour))

	s := Service{Store: mustOpenStore(t, dbPath), Exec: &fakeExec{}, Now: time.Now}
	_, err := s.Run(context.Background(), Input{SkillName: "dd", EvalRunID: "ab-stale", MaxAgeHours: 24})
	if err == nil || !strings.Contains(err.Error(), "STALE_AB_VERDICT") {
		t.Fatalf("expected stale refusal, got %v", err)
	}
}

func TestServiceRunWritesPromotionAndMovesTag(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dbPath := tmp + "/runs/virmux.sqlite"
	seedEvalRun(t, dbPath, "ab-pass", time.Now().UTC())
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
}

func mustOpenStore(t *testing.T, dbPath string) *store.Store {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
