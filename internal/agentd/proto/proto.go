package proto

import "encoding/json"

type Error struct {
	Code      string `json:"code"`
	Msg       string `json:"msg"`
	Retryable bool   `json:"retryable"`
	ReqID     int64  `json:"req"`
}

type Request struct {
	ReqID int64           `json:"req"`
	Tool  string          `json:"tool"`
	Args  json.RawMessage `json:"args,omitempty"`
	Allow []string        `json:"allow,omitempty"`
	RunID string          `json:"run_id,omitempty"`
}

type Response struct {
	ReqID        int64          `json:"req"`
	OK           bool           `json:"ok"`
	RC           int            `json:"rc,omitempty"`
	StdoutRef    string         `json:"stdout_ref,omitempty"`
	StderrRef    string         `json:"stderr_ref,omitempty"`
	OHHash       string         `json:"ohash,omitempty"`
	DurMS        int64          `json:"dur_ms,omitempty"`
	Error        *Error         `json:"error,omitempty"`
	Data         map[string]any `json:"data,omitempty"`
	ArtifactRefs []string       `json:"artifact_refs,omitempty"`
}

func (r Response) Marshal() ([]byte, error) { return json.Marshal(r) }
