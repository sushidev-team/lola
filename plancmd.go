package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tui"
)

// planCmd is the orchestrator pass (`lola plan <issue>`). Without --apply it
// asks the daemon for a decomposition, prints it and saves it as JSON for the
// human to review or edit; nothing is created. With --apply it sends that file
// back and the daemon creates the sub-issues with their blocked-by relations,
// which dispatch then runs in dependency order.
func planCmd() *cobra.Command {
	var apply bool
	var project, file string
	cmd := &cobra.Command{
		Use:   "plan <issue>",
		Short: "Decompose a large Linear issue into dependent sub-issues (review, then --apply)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			issue := strings.TrimSpace(a[0])
			path := file
			if path == "" {
				p, err := planFile(issue)
				if err != nil {
					return err
				}
				path = p
			}
			if apply {
				return applyPlan(c.OutOrStdout(), issue, project, path)
			}
			return proposePlan(c.OutOrStdout(), issue, project, path)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "create the saved (possibly edited) plan's sub-issues in Linear")
	cmd.Flags().StringVar(&project, "project", "", "the [[project]] whose poll dispatches the sub-issues (default: the one on the issue's team)")
	cmd.Flags().StringVar(&file, "file", "", "plan file to write / apply (default: ~/.lola/plans/<issue>.json)")
	return cmd
}

// planFile is the default plan location for issue under $LOLA_HOME.
func planFile(issue string) (string, error) {
	home, err := config.Home()
	if err != nil {
		return "", err
	}
	name := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == os.PathSeparator {
			return '_'
		}
		return r
	}, issue)
	return filepath.Join(home, "plans", name+".json"), nil
}

func proposePlan(w io.Writer, issue, project, path string) error {
	args, err := json.Marshal(protocol.PlanArgs{Issue: issue})
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "planning %s (one claude pass, may take a few minutes)…\n", issue)
	raw, err := tui.Request(protocol.Request{Cmd: "plan", Args: args})
	if err != nil {
		return err
	}
	var d protocol.PlanData
	if err := json.Unmarshal(raw, &d); err != nil {
		return fmt.Errorf("bad plan data: %w", err)
	}
	body, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Fprint(w, renderPlan(d))
	applyArgs := d.Issue
	if project != "" {
		applyArgs += " --project " + project
	}
	fmt.Fprintf(w, "\nNothing was created. Review or edit %s, then run:\n  lola plan %s --apply\n", path, applyArgs)
	return nil
}

func applyPlan(w io.Writer, issue, project, path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read plan (run `lola plan %s` first): %w", issue, err)
	}
	var d protocol.PlanData
	if err := json.Unmarshal(body, &d); err != nil {
		return fmt.Errorf("bad plan file %s: %w", path, err)
	}
	if d.Issue != "" && !strings.EqualFold(d.Issue, issue) {
		return fmt.Errorf("plan file %s is for %s, not %s", path, d.Issue, issue)
	}
	args, err := json.Marshal(protocol.PlanApplyArgs{Issue: issue, Project: project, Steps: d.Steps})
	if err != nil {
		return err
	}
	raw, err := tui.Request(protocol.Request{Cmd: "planApply", Args: args})
	if err != nil {
		return err
	}
	var res protocol.PlanApplyData
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("bad planApply data: %w", err)
	}
	fmt.Fprintf(w, "created %d sub-issue(s) of %s for project %s:\n", len(res.Created), res.Parent, res.Project)
	for i, id := range res.Created {
		title := ""
		if i < len(d.Steps) {
			title = d.Steps[i].Title
		}
		fmt.Fprintf(w, "  %d. %s  %s\n", i+1, id, title)
	}
	if res.Message != "" {
		fmt.Fprintln(w, "note:", res.Message)
	}
	return nil
}

// renderPlan prints a proposal as a numbered list with its dependencies.
func renderPlan(d protocol.PlanData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n", d.Issue, d.Title)
	for i, s := range d.Steps {
		fmt.Fprintf(&b, "\n%d. %s", i+1, s.Title)
		if len(s.BlockedBy) > 0 {
			deps := make([]string, len(s.BlockedBy))
			for j, x := range s.BlockedBy {
				deps[j] = fmt.Sprint(x)
			}
			fmt.Fprintf(&b, "  [after %s]", strings.Join(deps, ", "))
		}
		b.WriteString("\n")
		for _, line := range strings.Split(s.Description, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				fmt.Fprintf(&b, "   %s\n", line)
			}
		}
	}
	return b.String()
}
