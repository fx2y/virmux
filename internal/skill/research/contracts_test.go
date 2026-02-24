package research

import "testing"

func TestC0CanonConstants(t *testing.T) {
	t.Parallel()
	if CanonicalCommand != "virmux research <plan|map|reduce|replay|run>" {
		t.Fatalf("unexpected canonical command: %q", CanonicalCommand)
	}
	if CanonicalTracePath != "trace.ndjson" {
		t.Fatalf("unexpected canonical trace path: %q", CanonicalTracePath)
	}
	if TraceCompatPath != "trace.jsonl" {
		t.Fatalf("unexpected trace compat path: %q", TraceCompatPath)
	}
}

func TestDefaultPackageRolesNonEmpty(t *testing.T) {
	t.Parallel()
	if DefaultPackageRoles.Planner == "" ||
		DefaultPackageRoles.Scheduler == "" ||
		DefaultPackageRoles.Mapper == "" ||
		DefaultPackageRoles.Reducer == "" ||
		DefaultPackageRoles.Replay == "" {
		t.Fatalf("all research package roles must be set: %#v", DefaultPackageRoles)
	}
}

func TestFailureErrorFormatting(t *testing.T) {
	t.Parallel()
	if got := (Failure{Code: FailurePlanSchema}).Error(); got != "PLAN_SCHEMA_INVALID" {
		t.Fatalf("unexpected no-message error: %q", got)
	}
	if got := (Failure{Code: FailurePlanOrder, Message: "plan.yaml missing"}).Error(); got != "PLAN_NOT_WRITTEN_FIRST: plan.yaml missing" {
		t.Fatalf("unexpected message error: %q", got)
	}
}
