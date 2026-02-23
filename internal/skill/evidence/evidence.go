package evidence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
)

// WriteJSONFile writes an indented JSON document with trailing newline and returns exact bytes written.
func WriteJSONFile(path string, v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return nil, err
	}
	return b, nil
}

func SHA256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return fmt.Sprintf("%x", s[:])
}
