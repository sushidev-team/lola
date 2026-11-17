package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
)

// A branch-name match is not evidence that a previous round's completed PR
// belongs to this one. Keep this before facts reach either observer's delivery
// derivation, write-back, review or cleanup. Explicit PR resumes are exempt.
func prForAttempt(s session.Session, pr *scm.PR) *scm.PR {
	if pr == nil || !s.OwnsBranch() || s.FirstSeen.IsZero() {
		return pr
	}
	var ended time.Time
	switch pr.State {
	case "MERGED":
		ended = pr.MergedAt
		if ended.IsZero() {
			ended = pr.ClosedAt
		}
	case "CLOSED":
		ended = pr.ClosedAt
	}
	if !ended.IsZero() && ended.Before(s.FirstSeen) {
		return nil
	}
	return pr
}

// Conversation directories also cover launches made before durable reservations
// existed. An unreadable history must not be overwritten.
func sessionPreviouslyUsed(home, id string) (bool, error) {
	_, err := os.Lstat(filepath.Join(sshstore.Root(home), id))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// The prompt gets a bounded history snapshot. Comments remain a live Linear
// read by the agent, as with the original issue briefing.
func previousIssueWork(home, project, issue string) string {
	entries, err := os.ReadDir(sshstore.Root(home))
	if err != nil {
		return ""
	}
	var history []*sshstore.EndedInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info := sshstore.ReadEnded(home, e.Name())
		if info != nil && info.Project == project && strings.EqualFold(info.Issue, issue) {
			history = append(history, info)
		}
	}
	slices.SortFunc(history, func(a, b *sshstore.EndedInfo) int { return b.EndedAt.Compare(a.EndedAt) })
	if len(history) > 3 {
		history = history[:3]
	}
	var b strings.Builder
	for _, h := range history {
		fmt.Fprintf(&b, "- %s ended %s: %s\n", h.ID, h.EndedAt.UTC().Format(time.RFC3339), h.Reason)
		urls := slices.Clone(h.PRURLs)
		if h.PRURL != "" && !slices.Contains(urls, h.PRURL) {
			urls = append(urls, h.PRURL)
		}
		for _, url := range urls {
			fmt.Fprintf(&b, "  - Previous PR: %s\n", url)
		}
	}
	return b.String()
}
