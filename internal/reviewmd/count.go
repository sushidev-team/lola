package reviewmd

import "strings"

// Counts is a review's findings tallied by severity — the same buckets the
// rendered tally line uses.
type Counts struct {
	Blocker, Major, Minor, Other int
}

// Count tallies findings by severity with the renderer's own parser. A body it
// cannot split into findings but that is not empty (a plain-text pass) counts
// as one Other: something was reported, and an unknown must never read as a
// clean review. An empty body is a clean pass (all zeros).
func Count(findings string) Counts {
	var c Counts
	if strings.TrimSpace(findings) == "" {
		return c
	}
	fs, _ := parse(findings)
	if len(fs) == 0 {
		c.Other = 1
		return c
	}
	for _, f := range fs {
		switch f.sev {
		case sevBlocker:
			c.Blocker++
		case sevMajor:
			c.Major++
		case sevMinor:
			c.Minor++
		default:
			c.Other++
		}
	}
	return c
}
