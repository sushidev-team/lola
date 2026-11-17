// Package goalpolicy defines accepted autonomous execution permissions and
// finite limits. Validation is static; it performs no filesystem or network IO.
package goalpolicy

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
var imageDigest = regexp.MustCompile(`^(?:sha256:|[^\s@]+@sha256:)[a-f0-9]{64}$`)

// Check comes from the accepted operator policy, never a worker report. Image
// uses a content digest; mutable tags cannot silently change verification.
type Check struct {
	ID             string         `json:"id" toml:"id"`
	Command        string         `json:"command" toml:"command"`
	Member         string         `json:"member,omitempty" toml:"member,omitempty"` // empty = stack root
	Image          string         `json:"image" toml:"image"`
	TimeoutSeconds int            `json:"timeout_seconds" toml:"timeout_seconds"`
	MemoryMiB      int            `json:"memory_mib,omitempty" toml:"memory_mib,omitempty"` // zero preserves the original 2 GiB limit
	Artifacts      []ArtifactSpec `json:"artifacts,omitempty" toml:"artifacts,omitempty"`
}

// MemoryLimitBytes resolves the accepted limit without changing legacy policy digests.
func (s Check) MemoryLimitBytes() int64 {
	if s.MemoryMiB == 0 {
		return 2 << 30
	}
	return int64(s.MemoryMiB) << 20
}

type ArtifactSpec struct {
	ID   string `json:"id" toml:"id"`
	Kind string `json:"kind" toml:"kind"`
	Path string `json:"path" toml:"path"` // relative to the stack root, includes member
}

func (s Check) Validate() error {
	if !identifier.MatchString(s.ID) || strings.TrimSpace(s.Command) == "" || len(s.Command) > 16000 || strings.ContainsRune(s.Command, 0) {
		return errors.New("check requires an identity and a bounded command")
	}
	if s.Member != "" && !identifier.MatchString(s.Member) {
		return errors.New("invalid check member")
	}
	if !imageDigest.MatchString(s.Image) || strings.HasPrefix(s.Image, "-") {
		return errors.New("check image must be pinned by sha256 digest")
	}
	if s.TimeoutSeconds < 1 || s.TimeoutSeconds > 3600 {
		return errors.New("check timeout must be 1–3600 seconds")
	}
	if s.MemoryMiB != 0 && (s.MemoryMiB < 256 || s.MemoryMiB > 8192) {
		return errors.New("check memory_mib must be 256–8192, or zero for the 2048 MiB default")
	}
	if len(s.Artifacts) > 20 {
		return errors.New("at most 20 artifacts per check")
	}
	seen := map[string]bool{}
	for _, a := range s.Artifacts {
		if !identifier.MatchString(a.ID) || !identifier.MatchString(a.Kind) || seen[a.ID] || !filepath.IsLocal(a.Path) || filepath.Clean(a.Path) != a.Path || a.Path == "." {
			return errors.New("artifact needs unique identity, kind, and a normalized relative path")
		}
		seen[a.ID] = true
	}
	return nil
}
