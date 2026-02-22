package trace

import "testing"

func TestValidateLine(t *testing.T) {
	t.Parallel()
	good := []byte(`{"ts":"2026-02-22T00:00:00Z","run_id":"r1","task":"vm:smoke","event":"run.started","payload":{}}`)
	if err := ValidateLine(good); err != nil {
		t.Fatalf("expected valid line: %v", err)
	}

	bad := []byte(`{"run_id":"r1"}`)
	if err := ValidateLine(bad); err == nil {
		t.Fatalf("expected invalid line")
	}
}
