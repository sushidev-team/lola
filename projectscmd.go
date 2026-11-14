package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tui"
)

// projectsCmd groups project-wide operations; today only `bulk`.
func projectsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "projects", Short: "Operate on several projects at once"}
	var (
		projects                                      []string
		agent, matchMode, onSent, blocked             string
		matchLabels                                   []string
		clearMatch, clearOnSent, clearBlocked, dryRun bool
	)
	bulk := &cobra.Command{
		Use:   "bulk",
		Short: "Change the same settings on all (or selected) projects",
		Long: `Change the same settings on all projects, or only those named with --project.

Label values may be Linear label ids, names, or "group/name". Names are looked
up in each project's own team, so one name works even when the same label has a
different id in every team. Nothing is saved unless every project resolves.
Use --dry-run to preview.`,
		Example: `  lola projects bulk --agent codex
  lola projects bulk --project api --project web --match-label bug --match-label "area/backend"
  lola projects bulk --blocked-label "Blocked" --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			a := protocol.BulkProjectsArgs{Projects: projects, DryRun: dryRun}
			f := c.Flags()
			if f.Changed("agent") {
				a.Agent = &agent
			}
			if f.Changed("match-mode") {
				a.MatchMode = &matchMode
			}
			switch {
			case clearMatch:
				none := []string{}
				a.MatchLabels = &none
			case f.Changed("match-label"):
				a.MatchLabels = &matchLabels
			}
			if clearOnSent {
				empty := ""
				a.OnSentSetLabel = &empty
			} else if f.Changed("on-sent-label") {
				a.OnSentSetLabel = &onSent
			}
			if clearBlocked {
				empty := ""
				a.BlockedLabel = &empty
			} else if f.Changed("blocked-label") {
				a.BlockedLabel = &blocked
			}
			raw, err := json.Marshal(a)
			if err != nil {
				return err
			}
			resp, err := tui.RequestResponse(protocol.Request{Cmd: "bulkProjects", Args: raw})
			if err != nil {
				return err
			}
			var d protocol.BulkProjectsData
			if len(resp.Data) > 0 {
				_ = json.Unmarshal(resp.Data, &d)
			}
			fmt.Print(renderBulk(&d, dryRun))
			if !resp.OK {
				return fmt.Errorf("%s", resp.Error)
			}
			return nil
		},
	}
	fl := bulk.Flags()
	fl.StringSliceVar(&projects, "project", nil, "limit to this project (repeatable; default all)")
	fl.StringVar(&agent, "agent", "", "coding agent: claude|codex|opencode")
	fl.StringSliceVar(&matchLabels, "match-label", nil, "replace match_labels with these labels (repeatable)")
	fl.BoolVar(&clearMatch, "clear-match-labels", false, "match no labels")
	fl.StringVar(&matchMode, "match-mode", "", "match_mode: any|all")
	fl.StringVar(&onSent, "on-sent-label", "", "label set once an issue is dispatched")
	fl.BoolVar(&clearOnSent, "clear-on-sent-label", false, "set no label on dispatch")
	fl.StringVar(&blocked, "blocked-label", "", "label set when a session is blocked")
	fl.BoolVar(&clearBlocked, "clear-blocked-label", false, "set no blocked label")
	fl.BoolVar(&dryRun, "dry-run", false, "show what would change without saving")
	cmd.AddCommand(bulk)
	return cmd
}

func renderBulk(d *protocol.BulkProjectsData, dry bool) string {
	var b strings.Builder
	for _, c := range d.Changes {
		switch {
		case c.Error != "":
			fmt.Fprintf(&b, "  %-24s ERROR %s\n", c.Project, c.Error)
		case len(c.Changed) == 0:
			fmt.Fprintf(&b, "  %-24s unchanged\n", c.Project)
		default:
			fmt.Fprintf(&b, "  %-24s %s\n", c.Project, strings.Join(c.Changed, ", "))
		}
	}
	switch {
	case dry:
		b.WriteString("dry run: nothing saved\n")
	case d.Applied:
		b.WriteString("saved and reloaded\n")
	}
	return b.String()
}
