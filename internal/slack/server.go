package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type Recorder interface {
	InsertSlackEvent(ctx context.Context, eventType, payload string, ts time.Time) error
}

func NewMux(rec Recorder) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/slack/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		t, _ := payload["type"].(string)
		switch t {
		case "url_verification":
			challenge, _ := payload["challenge"].(string)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(challenge))
			return
		case "event_callback":
			if rec != nil {
				_ = rec.InsertSlackEvent(r.Context(), "slack.event_callback", string(body), time.Now())
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ignored"))
		}
	})
	return mux
}
