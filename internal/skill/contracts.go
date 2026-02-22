package skill

import (
	"context"
	"time"
)

// Canonical file names for the spec-04 skill artifact contract.
const (
	CanonicalSkillFile = "SKILL.md"
	PromptCompatFile   = "prompt.md"
	ToolsConfigFile    = "tools.yaml"
	RubricConfigFile   = "rubric.yaml"
)

// Clock injects time for deterministic IDs/timestamps in tests.
type Clock interface {
	Now() time.Time
}

// IDGen injects stable ID generation (run IDs, eval IDs, refine IDs).
type IDGen interface {
	New(kind string, started time.Time) string
}

// Command captures a host-side subprocess invocation for git/promptfoo/helpers.
type Command struct {
	Dir       string
	Env       []string
	Name      string
	Args      []string
	Timeout   time.Duration
	StartedAt time.Time
}

// CommandResult is transport-agnostic subprocess evidence.
type CommandResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	EndedAt  time.Time
}

// ExecRunner is the shared seam for host subprocesses (git, promptfoo, helpers).
type ExecRunner interface {
	Run(context.Context, Command) (CommandResult, error)
}

// GitRunner narrows git operations behind an injectable seam.
type GitRunner interface {
	RunGit(ctx context.Context, dir string, args []string) (CommandResult, error)
}

// PromptfooRunner isolates promptfoo integration from CLI/parsing layers.
type PromptfooRunner interface {
	RunPromptfoo(ctx context.Context, dir string, args []string) (CommandResult, error)
}

// PackageRoles freezes C0 responsibility boundaries to keep cmd/virmux thin.
type PackageRoles struct {
	Spec   string
	Run    string
	Judge  string
	Eval   string
	GitOps string
	Motif  string
	Runlog string
}

// DefaultPackageRoles is the C0 package map agreed before C1 feature work.
var DefaultPackageRoles = PackageRoles{
	Spec:   "load/lint/index skill artifacts (SKILL/tools/rubric/tests)",
	Run:    "skill envelope, budget gates, VM-backed exec, replay",
	Judge:  "rubric parse + deterministic scoring adapters",
	Eval:   "promptfoo cfggen/runner + AB orchestration",
	GitOps: "branch/commit/tag/PR-body operations",
	Motif:  "trace+score motif mining + scaffold generation",
	Runlog: "optional generic trace/sqlite/artifact helper extraction target",
}
