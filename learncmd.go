package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tui"
)

func learnCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "learn", Short: "Capture, curate, and export scoped workspace lessons"}
	var project string
	cmd.PersistentFlags().StringVar(&project, "project", "", "configured project or stack")
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List candidates and active lessons", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return requestGoal(c, "knowledgeList", map[string]any{"scope": knowledge.Scope{Project: project}})
		}})
	cmd.AddCommand(&cobra.Command{Use: "maintain", Short: "Recheck watched code and expiry; return stale lessons to candidates", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if project == "" {
				return fmt.Errorf("--project required")
			}
			return requestGoal(c, "knowledgeMaintain", map[string]any{"scope": knowledge.Scope{Project: project}})
		}})
	var paths []string
	var query, goalID string
	lookup := &cobra.Command{Use: "lookup", Short: "Retrieve bounded active lessons for this scope", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if project == "" {
				return fmt.Errorf("--project required")
			}
			return requestGoal(c, "knowledgeLookup", map[string]any{"query": knowledge.Query{Project: project, GoalID: goalID, Paths: paths, Text: query}})
		}}
	lookup.Flags().StringArrayVar(&paths, "path", nil, "repository-relative affected path (repeatable)")
	lookup.Flags().StringVar(&query, "query", "", "task text for ranking")
	lookup.Flags().StringVar(&goalID, "goal", "", "include this goal's provisional lessons")
	cmd.AddCommand(lookup)
	for _, action := range []struct{ name, wire, help string }{
		{"capture", "knowledgeCapture", "Store an unverified lesson candidate from a JSON file"},
		{"correct", "knowledgeCorrect", "Record an explicit human correction; activate if scoped and unopposed"},
	} {
		cmd.AddCommand(&cobra.Command{Use: action.name + " <file.json>", Short: action.help, Args: cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, args []string) error {
				if action.name == "correct" && os.Getenv("LOLA_SESSION") != "" {
					return fmt.Errorf("workers submit candidates in .lola/learnings.json; corrections require an operator")
				}
				b, err := os.ReadFile(args[0])
				if err != nil {
					return err
				}
				var data struct {
					Scope    knowledge.Scope    `json:"scope"`
					Proposal knowledge.Proposal `json:"proposal"`
				}
				if err := json.Unmarshal(b, &data); err != nil {
					return err
				}
				if project != "" {
					data.Scope.Project = project
				}
				return requestGoal(c, action.wire, data)
			}})
	}
	for _, action := range []struct{ name, wire, help string }{
		{"accept", "knowledgeAccept", "Accept a reviewed candidate with explicit verification evidence"},
		{"dispute", "knowledgeDispute", "Remove a lesson from active guidance with a reason"},
	} {
		var version int
		var evidence, expires string
		var supersedes, watch []string
		edit := &cobra.Command{Use: action.name + " <lesson-id>", Short: action.help, Args: cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, args []string) error {
				if os.Getenv("LOLA_SESSION") != "" {
					return fmt.Errorf("lesson curation requires an operator")
				}
				if version < 1 || evidence == "" {
					return fmt.Errorf("--version and --evidence required")
				}
				data := map[string]any{"id": args[0], "version": version, "evidence": evidence, "supersedes": supersedes}
				if len(watch) > 0 {
					targets, err := learningCodeTargets(watch)
					if err != nil {
						return err
					}
					data["code_targets"] = targets
				}
				if expires != "" {
					stamp, err := time.Parse(time.RFC3339, expires)
					if err != nil {
						return err
					}
					data["expires"] = stamp
				}
				return requestGoal(c, action.wire, data)
			}}
		edit.Flags().IntVar(&version, "version", 0, "lesson version reviewed")
		edit.Flags().StringVar(&evidence, "evidence", "", "verification evidence or dispute reason")
		if action.name == "accept" {
			edit.Flags().StringArrayVar(&watch, "watch", nil, "committed code to revalidate, member:path (repeatable; . watches the whole repository)")
			edit.Flags().StringSliceVar(&supersedes, "supersedes", nil, "conflicting lesson IDs explicitly replaced")
			edit.Flags().StringVar(&expires, "expires", "", "RFC3339 expiry for environment-specific guidance")
		}
		cmd.AddCommand(edit)
	}
	var output string
	export := &cobra.Command{Use: "export", Short: "Write a reviewable LEARNINGS.md proposal to a new directory", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if project == "" || output == "" {
				return fmt.Errorf("--project and --output required")
			}
			args, _ := json.Marshal(map[string]any{"scope": knowledge.Scope{Project: project}})
			raw, err := tui.Request(protocol.Request{Cmd: "knowledgeExport", Args: args})
			if err != nil {
				return err
			}
			var files map[string]string
			if err := json.Unmarshal(raw, &files); err != nil {
				return err
			}
			if err := writeLearningExport(output, files); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Wrote %d reviewable files to %s\n", len(files), output)
			return nil
		}}
	export.Flags().StringVar(&output, "output", "", "new output directory; existing directories are refused")
	cmd.AddCommand(export)
	return cmd
}

func writeLearningExport(dir string, files map[string]string) error {
	// Refuse traversal even if a mismatched or compromised daemon returns it.
	for name := range files {
		clean := filepath.Clean(name)
		if clean != name || filepath.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe export path %q", name)
		}
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(body)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func learningCodeTargets(values []string) ([]knowledge.CodeTarget, error) {
	var targets []knowledge.CodeTarget
	for _, value := range values {
		member, path, ok := strings.Cut(value, ":")
		if !ok {
			return nil, fmt.Errorf("--watch needs member:path")
		}
		targets = append(targets, knowledge.CodeTarget{Member: member, Path: path})
	}
	return targets, knowledge.ValidateTargets(targets)
}
