package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tui"
)

func addImprovementCommands(parent *cobra.Command, project *string) {
	for _, action := range []struct{ use, wire, help string }{
		{"lesson <id>", "knowledgeVersion", "Inspect an exact saved lesson version, including its evidence"},
		{"improvements", "knowledgeImprovements", "List reviewable proposals from recurring reports, prevention ideas, and policy requests"},
		{"improvement-history <id>", "knowledgeImprovementHistory", "Inspect triage history and its complete source-version receipts"},
		{"improvement <id>", "knowledgeImprovement", "Inspect an improvement and its source revision"},
		{"decide-improvement <id>", "knowledgeDecideImprovement", "Record human triage without authorizing implementation"},
		{"export-improvement <id>", "knowledgeExportImprovement", "Export a reviewed task brief to a new directory"},
	} {
		var goal, after, revision, decision, evidence, output string
		var version int
		cmd := &cobra.Command{Use: action.use, Short: action.help, Args: cobra.ExactArgs(1)}
		if action.wire == "knowledgeImprovements" {
			cmd.Args = cobra.NoArgs
			cmd.Flags().StringVar(&after, "after", "", "next_after cursor from a previous page")
		}
		cmd.Flags().StringVar(&goal, "goal", "", "exact goal scope; default shows project-wide proposals only")
		if action.wire == "knowledgeVersion" {
			cmd.Flags().IntVar(&version, "version", 0, "exact lesson version cited by a proposal or session")
		}
		if action.wire == "knowledgeDecideImprovement" || action.wire == "knowledgeExportImprovement" {
			cmd.Flags().StringVar(&revision, "revision", "", "complete source revision reviewed")
		}
		if action.wire == "knowledgeDecideImprovement" {
			cmd.Flags().StringVar(&decision, "decision", "", "dismissed, tracked, or reopened")
			cmd.Flags().StringVar(&evidence, "evidence", "", "reason or normal task/PR reference")
		}
		if action.wire == "knowledgeExportImprovement" {
			cmd.Flags().StringVar(&output, "output", "", "new output directory; existing directories are refused")
		}
		cmd.RunE = func(c *cobra.Command, args []string) error {
			if *project == "" {
				return fmt.Errorf("--project required")
			}
			data := map[string]any{"scope": knowledge.Scope{Project: *project, GoalID: goal}, "after": after, "revision": revision, "decision": decision, "evidence": evidence}
			if action.wire == "knowledgeVersion" {
				if version < 1 {
					return fmt.Errorf("--version required")
				}
				data["version"] = version
			}
			if len(args) > 0 {
				data["id"] = args[0]
			}
			if action.wire == "knowledgeDecideImprovement" {
				if os.Getenv("LOLA_SESSION") != "" {
					return fmt.Errorf("improvement decisions require an operator")
				}
				if revision == "" || decision == "" || evidence == "" {
					return fmt.Errorf("--revision, --decision, and --evidence required")
				}
			}
			if action.wire != "knowledgeExportImprovement" {
				return requestGoal(c, action.wire, data)
			}
			if revision == "" || output == "" {
				return fmt.Errorf("--revision and --output required")
			}
			body, _ := json.Marshal(data)
			raw, err := tui.Request(protocol.Request{Cmd: action.wire, Args: body})
			if err != nil {
				return err
			}
			var files map[string]string
			if err = json.Unmarshal(raw, &files); err != nil {
				return err
			}
			if err = writeLearningExport(output, files); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Wrote a reviewable improvement proposal to %s\n", output)
			return nil
		}
		parent.AddCommand(cmd)
	}
}
