package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tui"
)

// bestofCmd is best-of-N (`lola bestof …`): run several agents/models on ONE
// issue, compare their results side by side, keep one.
func bestofCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bestof",
		Short: "Best-of-N: run several agents/models on one issue, compare, keep one",
		Long: `Best-of-N runs one Linear issue as several independent attempts — different
coding agents, or one agent on different models — each on its own sibling
branch, opening a DRAFT pull request. Compare them (CI, review findings, diff
size) and keep one: the kept PR is marked ready, the others are closed and
their sessions torn down (a worktree with uncommitted changes is kept).

A mix is written <agent>[@<model>][:<count>], comma-separated:
  claude:2                      two claude attempts
  claude@opus,claude@sonnet     one agent on two models
  claude,codex@gpt-5-codex,opencode

Every attempt counts against the concurrency cap and the budget. A poll can
also start a group on its own: set [best_of].label and label the issue.`,
	}
	cmd.AddCommand(bestofStartCmd(), bestofCompareCmd(), bestofPickCmd())
	return cmd
}

func bestofStartCmd() *cobra.Command {
	var project, agents string
	cmd := &cobra.Command{
		Use:   "start <issue>",
		Short: "Start an issue as a best-of group (default mix: [best_of].agents)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			args, err := json.Marshal(protocol.BestOfArgs{Issue: strings.TrimSpace(a[0]), Project: project, Agents: agents})
			if err != nil {
				return err
			}
			raw, err := tui.Request(protocol.Request{Cmd: "bestOf", Args: args})
			if err != nil {
				return err
			}
			var d protocol.BestOfData
			if err := json.Unmarshal(raw, &d); err != nil {
				return fmt.Errorf("bad bestOf data: %w", err)
			}
			w := c.OutOrStdout()
			fmt.Fprintln(w, d.Message)
			for _, f := range d.Failed {
				fmt.Fprintln(w, "  failed:", f)
			}
			if n := len(d.Failed); n > 0 {
				// The group runs, but smaller than asked: a script must be able
				// to tell from the exit status.
				return fmt.Errorf("%d best-of attempt(s) failed to spawn", n)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agents, "agents", "", `the mix, e.g. "claude:2,codex" (default: [best_of].agents)`)
	cmd.Flags().StringVar(&project, "project", "", "the [[project]] to run it in (default: the one on the issue's team)")
	return cmd
}

func bestofCompareCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "compare <issue|group|session>",
		Short: "Compare a best-of group's attempts side by side",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			d, err := requestCompare(strings.TrimSpace(a[0]))
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(d)
			}
			renderCompare(c.OutOrStdout(), d)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the raw comparison")
	return cmd
}

// requestCompare resolves target as an issue identifier (letters-digits), a
// session id (lola-…), or else a group id.
func requestCompare(target string) (protocol.BestOfCompareData, error) {
	var a protocol.BestOfCompareArgs
	// Issue first: a team keyed "LOLA" makes "lola-123" an issue, and a session
	// id always has more dash-separated parts than an identifier.
	switch {
	case looksLikeIssue(target):
		a.Issue = target
	case strings.HasPrefix(target, "lola-"):
		a.Session = target
	default:
		a.Group = target
	}
	args, err := json.Marshal(a)
	if err != nil {
		return protocol.BestOfCompareData{}, err
	}
	raw, err := tui.Request(protocol.Request{Cmd: "bestOfCompare", Args: args})
	if err != nil {
		return protocol.BestOfCompareData{}, err
	}
	var d protocol.BestOfCompareData
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, fmt.Errorf("bad compare data: %w", err)
	}
	return d, nil
}

// looksLikeIssue reports whether s has a Linear identifier's shape: letters, a
// dash, digits (FE-231). A group id carries a second dash, so it never matches.
func looksLikeIssue(s string) bool {
	team, num, ok := strings.Cut(s, "-")
	if !ok || team == "" || num == "" {
		return false
	}
	for _, r := range team {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// renderCompare prints one row per attempt. The suggestion is marked as one —
// it is a ranking hint, the pick is the human's.
func renderCompare(w io.Writer, d protocol.BestOfCompareData) {
	fmt.Fprintf(w, "%s  %s  (group %s, project %s)\n\n", d.Issue, d.Title, d.Group, d.Project)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tAGENT\tSTATE\tPR\tCI\tREVIEW\tDIFF\tTOKENS\tSESSION")
	for _, a := range d.Attempts {
		who := a.Agent
		if a.Model != "" {
			who += "@" + a.Model
		}
		mark := " "
		switch {
		case a.Winner:
			mark = "✓"
		case a.Session == d.Suggested && d.Picked == "":
			mark = "★"
		}
		pr := "—"
		if a.PRNumber > 0 {
			pr = fmt.Sprintf("#%d", a.PRNumber)
			if a.Draft {
				pr += " draft"
			}
			if s := strings.ToLower(a.PRState); s != "" && s != "open" {
				pr += " " + s
			}
		}
		ci := a.Checks
		if ci == "" {
			ci = "—"
		}
		review := "—"
		if r := a.Review; r != nil {
			if r.Blocker+r.Major+r.Minor+r.Other == 0 {
				review = "clean"
			} else {
				review = fmt.Sprintf("%dB %dM %dm", r.Blocker, r.Major, r.Minor+r.Other)
			}
		}
		diff := "?"
		if a.DiffError == "" {
			diff = fmt.Sprintf("%d files +%d -%d", a.Files, a.Additions, a.Deletions)
		}
		tokens := "—"
		if a.Usage != nil {
			tokens = compactTokens(a.Usage.Tokens)
		}
		fmt.Fprintf(tw, "%s%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", mark, a.Attempt, who, a.Display, pr, ci, review, diff, tokens, a.Session)
	}
	tw.Flush()
	switch {
	case d.Picked != "":
		fmt.Fprintf(w, "\n✓ kept %s\n", d.Picked)
	case d.Suggested != "":
		fmt.Fprintf(w, "\n★ suggested (CI, then review findings, then diff size): %s\n  keep it with: lola bestof pick %s\n", d.Suggested, d.Suggested)
	default:
		fmt.Fprintln(w, "\nno attempt has an open PR yet")
	}
}

func compactTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

func bestofPickCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pick <session-id>",
		Short: "Keep one attempt: mark its PR ready, close the others' PRs and tear their sessions down",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			args, err := json.Marshal(protocol.BestOfPickArgs{Session: strings.TrimSpace(a[0])})
			if err != nil {
				return err
			}
			raw, err := tui.Request(protocol.Request{Cmd: "bestOfPick", Args: args})
			if err != nil {
				return err
			}
			var d protocol.BestOfPickData
			if err := json.Unmarshal(raw, &d); err != nil {
				return fmt.Errorf("bad pick data: %w", err)
			}
			w := c.OutOrStdout()
			fmt.Fprintln(w, d.Message)
			for _, p := range d.Problems {
				fmt.Fprintln(w, "  !", p)
			}
			if n := len(d.Problems); n > 0 {
				// The pick stands; these steps did not (a PR left draft or open).
				return fmt.Errorf("pick kept %s, but %d step(s) failed", d.Winner, n)
			}
			return nil
		},
	}
}
