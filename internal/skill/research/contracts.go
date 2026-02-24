package research

import "context"

const (
	// CanonicalCommand freezes the C0 command surface mapping.
	CanonicalCommand = "virmux research <plan|map|reduce|replay|run>"
	// CanonicalTracePath is the run-truth trace filename.
	CanonicalTracePath = "trace.ndjson"
	// TraceCompatPath keeps compatibility tooling stable.
	TraceCompatPath = "trace.jsonl"
)

// FailureCode reserves typed failures for research services.
type FailureCode string

const (
	FailurePlanOrder      FailureCode = "PLAN_NOT_WRITTEN_FIRST"
	FailurePlanSchema     FailureCode = "PLAN_SCHEMA_INVALID"
	FailureWorkerContract FailureCode = "WORKER_CONTRACT_INVALID"
	FailureCoverageStop   FailureCode = "COVERAGE_STOP_MISSING"
	FailureUncitedOutput  FailureCode = "UNCITED_OUTPUT"
	FailureReducerImpure  FailureCode = "REDUCER_IMPURE"
	FailureRerunSelector  FailureCode = "RERUN_SELECTOR_INVALID"
)

// Failure is the typed error envelope crossing service seams.
type Failure struct {
	Code    FailureCode
	Message string
}

func (f Failure) Error() string {
	if f.Message == "" {
		return string(f.Code)
	}
	return string(f.Code) + ": " + f.Message
}

// Planner compiles a user request into deterministic plan artifacts.
type Planner interface {
	Compile(context.Context, PlanInput) (PlanOutput, error)
}

// Scheduler computes deterministic track execution ordering.
type Scheduler interface {
	Build(context.Context, ScheduleInput) (ScheduleOutput, error)
}

// Mapper executes track work and emits only schema rows/evidence references.
type Mapper interface {
	Run(context.Context, MapInput) (MapOutput, error)
}

// Reducer synthesizes map outputs into deterministic artifacts without tool I/O.
type Reducer interface {
	Run(context.Context, ReduceInput) (ReduceOutput, error)
}

// Replay compares/reruns selected tracks under the same plan contract.
type Replay interface {
	Run(context.Context, ReplayInput) (ReplayOutput, error)
}

// HintProvider provides advisory decomposition/retrieval hints based on motifs.
type HintProvider interface {
	GetHints(ctx context.Context, query string) ([]string, error)
}

// Services groups research seams for thin cmd handlers.
type Services struct {
	Planner      Planner
	Scheduler    Scheduler
	Mapper       Mapper
	Reducer      Reducer
	Replay       Replay
	HintProvider HintProvider
}

type PlanInput struct {
	Query string
}

type PlanOutput struct {
	PlanID string
	Plan   *Plan
}

type ScheduleInput struct {
	PlanID string
}

type ScheduleOutput struct {
	PlanID string
}

type MapInput struct {
	RunID   string
	TrackID string
}

type MapOutput struct {
	RunID         string
	TrackID       string
	Retryable     bool
	FailureReason string
}

type ReduceInput struct {
	RunID string
}

type ReduceOutput struct {
	RunID string
}

type ReplayInput struct {
	RunID string
	Only  []string
}

type ReplayOutput struct {
	RunID string
}

// PackageRoles captures C0 boundary ownership to control blast radius.
type PackageRoles struct {
	Planner   string
	Scheduler string
	Mapper    string
	Reducer   string
	Replay    string
}

// DefaultPackageRoles is the C0 boundary map agreed before C1+ features.
var DefaultPackageRoles = PackageRoles{
	Planner:   "query->plan DAG/schema/budget compile + deterministic plan_id",
	Scheduler: "topo batches + bounded concurrency + track state transitions",
	Mapper:    "track execution + schema rows/evidence refs + typed failures",
	Reducer:   "pure synthesis to table.csv/report.md/slides.md; no tools/network",
	Replay:    "failed-track subset rerun + parity diff artifacts under same plan_id",
}
