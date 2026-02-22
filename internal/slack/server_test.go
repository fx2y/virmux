package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeRecorder struct {
	count int
}

func (f *fakeRecorder) InsertSlackEvent(_ context.Context, _ string, _ string, _ time.Time) error {
	f.count++
	return nil
}

func TestURLVerification(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	srv := httptest.NewServer(NewMux(rec))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/slack/events", "application/json", strings.NewReader(`{"type":"url_verification","challenge":"abc"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestEventCallbackRecords(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	srv := httptest.NewServer(NewMux(rec))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/slack/events", "application/json", strings.NewReader(`{"type":"event_callback","event":{"type":"message"}}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if rec.count != 1 {
		t.Fatalf("expected 1 callback, got %d", rec.count)
	}
}
