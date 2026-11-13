// Package knowledge stores attributable lessons, not executable policy. Worker
// submissions remain candidates until a human or a trusted verifier accepts them.
package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

type Status string

const (
	Candidate  Status = "candidate"
	Active     Status = "active"
	Disputed   Status = "disputed"
	Superseded Status = "superseded"
)

type Scope struct {
	Project string   `json:"project"`
	GoalID  string   `json:"goal_id,omitempty"`
	Paths   []string `json:"paths,omitempty"`
}

type Proposal struct {
	Prevention *PreventionIdea `json:"prevention,omitempty"`
	Topic      string          `json:"topic"`
	Summary    string          `json:"summary"`
	Rationale  string          `json:"rationale"`
	Paths      []string        `json:"paths,omitempty"`
	Tags       []string        `json:"tags,omitempty"`
	Evidence   []string        `json:"evidence"`
	Policy     bool            `json:"policy,omitempty"` // mandatory policy changes stay proposals
}

type Source struct {
	Kind    string `json:"kind"` // worker, review, correction, verification
	ID      string `json:"id"`
	Session string `json:"session,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

type Lesson struct {
	ReviewProof  *ReviewProof    `json:"review_proof,omitempty"`
	Code         []CodeReference `json:"code,omitempty"`
	ReviewReason string          `json:"review_reason,omitempty"`
	ID           string          `json:"id"`
	Version      int             `json:"version"`
	Scope        Scope           `json:"scope"`
	Proposal
	Source       Source    `json:"source"`
	Status       Status    `json:"status"`
	Conflicts    []string  `json:"conflicts,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	VerifiedAt   time.Time `json:"verified_at,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Verification string    `json:"verification,omitempty"`
}

type Reference struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type Query struct {
	Project  string   `json:"project"`
	GoalID   string   `json:"goal_id,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Text     string   `json:"text,omitempty"`
	MaxBytes int      `json:"max_bytes,omitempty"`
}

type Bundle struct {
	Lessons    []Lesson    `json:"lessons"`
	References []Reference `json:"references"`
}

// Reject likely credentials before they enter durable knowledge. This is a
// conservative guard, not a promise that arbitrary prose contains no secrets.
var credential = regexp.MustCompile(`(?i)(-----BEGIN [A-Z ]*PRIVATE KEY-----|(?:gh[pousr]_|github_pat_|sk-(?:ant-|proj-)?|lin_api_)[A-Za-z0-9_\-]{12,}|(?:api[_-]?key|password|secret|access[_-]?token)\s*[:=]\s*["']?[A-Za-z0-9_\-+/=]{8,})`)

func Validate(scope Scope, p Proposal, source Source) error {
	if err := validatePrevention(p.Prevention); err != nil {
		return err
	}
	if scope.Project == "" || source.ID == "" || source.Kind == "" {
		return fmt.Errorf("lesson requires a project and attributable source")
	}
	if len(p.Summary) < 8 || len(p.Summary) > 2000 || len(p.Topic) == 0 || len(p.Topic) > 160 || len(p.Rationale) > 4000 || len(p.Evidence) > 20 || len(p.Tags) > 20 || len(p.Paths) > 20 {
		return fmt.Errorf("lesson requires a short topic, 8–2000 byte summary, bounded rationale, paths, tags, and evidence")
	}
	all := []string{p.Topic, p.Summary, p.Rationale, source.ID, source.Commit}
	all = append(all, p.Evidence...)
	all = append(all, p.Tags...)
	all = append(all, p.Paths...)
	for _, text := range all {
		if len(text) > 4000 || credential.MatchString(text) || strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
			return fmt.Errorf("lesson contains a suspected credential, control character, or oversized field")
		}
	}
	for _, p := range p.Paths {
		if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, `\`) {
			return fmt.Errorf("lesson paths must be normalized repository-relative paths")
		}
	}
	return nil
}

func identity(scope Scope, p Proposal, source Source) string {
	scope.Paths = p.Paths
	body, _ := json.Marshal(struct {
		Scope    Scope
		Proposal Proposal
		Source   Source
	}{scope, p, source})
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:16])
}

func scopeMatch(l Lesson, q Query) bool {
	if l.Scope.Project != q.Project || (l.Scope.GoalID != "" && l.Scope.GoalID != q.GoalID) {
		return false
	}
	if len(l.Paths) == 0 {
		return true
	}
	for _, prefix := range l.Paths {
		for _, p := range q.Paths {
			if p == prefix || strings.HasPrefix(p, prefix+"/") {
				return true
			}
		}
	}
	return false
}

func Select(lessons []Lesson, q Query, now time.Time) Bundle {
	limit := q.MaxBytes
	if limit <= 0 || limit > 12000 {
		limit = 12000
	}
	var selected []Lesson
	for _, l := range lessons {
		if l.Status != Active || l.Policy || (!l.ExpiresAt.IsZero() && !l.ExpiresAt.After(now)) || !scopeMatch(l, q) {
			continue
		}
		selected = append(selected, l)
	}
	score := func(l Lesson) int {
		n := len(l.Paths) * 2
		text := strings.ToLower(q.Text)
		for _, tag := range l.Tags {
			if strings.Contains(text, strings.ToLower(tag)) {
				n++
			}
		}
		return n
	}
	slices.SortFunc(selected, func(a, b Lesson) int {
		if n := score(b) - score(a); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	out := Bundle{Lessons: []Lesson{}, References: []Reference{}}
	used := 0
	for _, l := range selected {
		// Budget the actual rendered bundle, not just the summary field.
		entry := renderLesson(l)
		if used+len(entry) > limit {
			continue
		}
		out.Lessons = append(out.Lessons, l)
		out.References = append(out.References, Reference{ID: l.ID, Version: l.Version})
		used += len(entry)
		if len(out.Lessons) == 12 {
			break
		}
	}
	return out
}

func renderLesson(l Lesson) string {
	return fmt.Sprintf("\n### %s (%s v%d)\n%s\nReason: %s\nEvidence: %s\n", l.Topic, l.ID, l.Version, l.Summary, l.Rationale, strings.Join(l.Evidence, "; "))
}

func (b Bundle) Markdown() string {
	var out strings.Builder
	for _, l := range b.Lessons {
		out.WriteString(renderLesson(l))
	}
	return out.String()
}

// Export is a reviewable documentation proposal. The caller chooses where to
// save it; this package never edits AGENTS.md or a repository checkout.
func Export(lessons []Lesson, project string, now time.Time) map[string]string {
	files := map[string]string{}
	var index strings.Builder
	index.WriteString("# Workspace learnings\n\nReviewable knowledge export. These lessons do not override project instructions, permissions, or acceptance checks.\n\n")
	for _, l := range lessons {
		if l.Scope.Project != project || l.Scope.GoalID != "" || l.Status != Active || l.Policy || (!l.ExpiresAt.IsZero() && !l.ExpiresAt.After(now)) {
			continue
		}
		name := "docs/learnings/" + l.ID + ".md"
		fmt.Fprintf(&index, "- [%s](%s)\n", strings.NewReplacer("[", "\\[", "]", "\\]").Replace(l.Topic), name)
		files[name] = renderLesson(l) + fmt.Sprintf("\nScope: %s\nSource: %s %s\nVerified: %s\n", strings.Join(l.Paths, ", "), l.Source.Kind, l.Source.ID, l.VerifiedAt.Format(time.RFC3339))
	}
	files["LEARNINGS.md"] = index.String()
	return files
}
