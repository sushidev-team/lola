package knowledge

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// CodeTarget names operator-selected committed evidence. Workers cannot supply
// the trusted digest; the daemon resolves it from configured repositories.
type CodeTarget struct {
	Member string `json:"member"`
	Path   string `json:"path"`
}
type CodeReference struct {
	CodeTarget
	Source string `json:"source"`
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Digest string `json:"digest"`
}
type Maintenance struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	State   string `json:"state"` // current, changed, expired, unavailable, untracked, inactive
	Reason  string `json:"reason,omitempty"`
}

var codeCommit = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)
var codeDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidateTargets(targets []CodeTarget) error {
	if len(targets) > 20 {
		return fmt.Errorf("at most 20 code references per lesson")
	}
	seen := map[CodeTarget]bool{}
	for _, t := range targets {
		if t.Member == "" || len(t.Member) > 240 || strings.ContainsAny(t.Member, "\x00\r\n\t") || t.Path == "" || len(t.Path) > 2000 || path.IsAbs(t.Path) || path.Clean(t.Path) != t.Path || t.Path == ".." || strings.HasPrefix(t.Path, "../") || strings.ContainsAny(t.Path, "\\\x00\r\n\t") || seen[t] {
			return fmt.Errorf("code references need unique members and normalized repository-relative paths")
		}
		seen[t] = true
	}
	return nil
}
func validateCode(refs []CodeReference) error {
	targets := make([]CodeTarget, 0, len(refs))
	for _, r := range refs {
		targets = append(targets, r.CodeTarget)
		if !filepath.IsAbs(r.Source) || r.Branch == "" || len(r.Branch) > 1000 || strings.ContainsAny(r.Source+r.Branch, "\x00\r\n") || !codeCommit.MatchString(r.Commit) || !codeDigest.MatchString(r.Digest) {
			return fmt.Errorf("code references require a configured checkout, branch and verified commit/digest")
		}
	}
	return ValidateTargets(targets)
}

// ReviewCode preserves the former proof and delivery history, but removes the
// changed lesson from active guidance. Repeat maintenance is a no-op; a stale
// read cannot demote a newly accepted version.
func (s *Store) ReviewCode(id string, version int, reason string) error {
	if reason != "referenced code changed" && reason != "lesson expired" {
		return fmt.Errorf("unknown maintenance reason")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copy()
	l, ok := next.Lessons[id]
	if !ok || l.Version != version {
		return fmt.Errorf("lesson changed during maintenance")
	}
	if l.Status != Active {
		return nil
	}
	next.History[id] = append(next.History[id], l)
	l.Status = Candidate
	l.Version++
	l.ReviewReason = reason
	next.Lessons[id] = l
	return s.commit(next)
}
