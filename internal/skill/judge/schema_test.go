package judge

import (
	"testing"
)

func TestValidateOutput(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "valid",
			input:   `{"version":"v1", "score":1.0, "pass":true, "criterion":[{"id":"c1", "w":1.0, "value":1.0, "pass":true}]}`,
			wantErr: false,
		},
		{
			name:    "invalid version",
			input:   `{"version":"v2", "score":1.0, "pass":true, "criterion":[{"id":"c1", "w":1.0, "value":1.0, "pass":true}]}`,
			wantErr: true,
		},
		{
			name:    "missing criterion",
			input:   `{"version":"v1", "score":1.0, "pass":true, "criterion":[]}`,
			wantErr: true,
		},
		{
			name:    "malformed json",
			input:   `{"version":"v1", "score":1.0`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateOutput([]byte(tc.input))
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateOutput() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
