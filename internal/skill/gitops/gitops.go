package gitops

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	skillpkg "github.com/haris/virmux/internal/skill"
)

// Exec is the minimal subprocess seam used by git operations.
type Exec interface {
	Run(context.Context, skillpkg.Command) (skillpkg.CommandResult, error)
}

// BranchName returns the canonical refine branch name.
func BranchName(skill, runID string) string {
	return fmt.Sprintf("refine/%s/%s", sanitizeToken(skill), sanitizeToken(runID))
}

// PatchHash returns deterministic sha256 over unified patch bytes.
func PatchHash(patch []byte) string {
	sum := sha256.Sum256(patch)
	return fmt.Sprintf("%x", sum[:])
}

func sanitizeToken(in string) string {
	in = strings.TrimSpace(strings.ToLower(in))
	if in == "" {
		return "x"
	}
	var b strings.Builder
	b.Grow(len(in))
	lastDash := false
	for _, r := range in {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '/'
		if !ok {
			r = '-'
		}
		if r == '/' {
			r = '-'
		}
		if r == '_' {
			r = '-'
		}
		if r == '-' {
			if lastDash {
				continue
			}
			lastDash = true
		} else {
			lastDash = false
		}
		b.WriteRune(r)
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "x"
	}
	return out
}
