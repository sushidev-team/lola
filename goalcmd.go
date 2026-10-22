package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tui"
)

func goalCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "goal", Short: "Plan and track bounded goals in an existing Linear project"}
	cmd.AddCommand(&cobra.Command{Use: "propose <brief.json>", Short: "Run one bounded coordinator to propose tasks from committed repository context", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		b, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		a, err := goalplan.DecodeRequest(b)
		if err != nil {
			return err
		}
		if a.ID == "" {
			a.ID = goal.NewID()
		}
		if err = a.Normalize(); err != nil {
			return err
		}
		// Print the identity BEFORE the socket call so a dropped response can
		// be inspected with proposal, without paying for a second call.
		fmt.Fprintf(c.ErrOrStderr(), "Proposal ID: %s (inspect with lola goal proposal %s)\n", a.ID, a.ID)
		return requestGoal(c, "goalPropose", a)
	}})
	cmd.AddCommand(&cobra.Command{Use: "proposal <proposal-id>", Short: "Inspect a durable coordinator proposal and its source evidence", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		return requestGoal(c, "goalProposal", protocol.GoalArgs{ID: args[0]})
	}})
	cmd.AddCommand(&cobra.Command{Use: "edit-plan <edit.json>", Short: "Revise a reviewed task proposal without a model call or Linear writes", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		b, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		a, err := goalplan.DecodeEdit(b)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.ErrOrStderr(), "Edit ID: %s (inspect with lola goal proposal %s before retrying)\n", a.EditID, a.ID)
		return requestGoal(c, "goalEditPlan", a)
	}})
	var progressRevision uint64
	var progressReason, progressUpdate string
	dismiss := &cobra.Command{Use: "dismiss-progress <goal-id>", Short: "Retain an uncertain Linear update in history and allow later progress updates", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		return requestGoal(c, "goalDismissProgress", goal.ProgressResolution{ID: args[0], Revision: progressRevision, UpdateID: progressUpdate, Reason: progressReason})
	}}
	dismiss.Flags().Uint64Var(&progressRevision, "revision", 0, "Reviewed goal revision (required)")
	dismiss.Flags().StringVar(&progressUpdate, "update", "", "Pending progress update UUID (required)")
	dismiss.Flags().StringVar(&progressReason, "reason", "", "Why the uncertain update can be left unresolved in Linear (required)")
	for _, key := range []string{"revision", "update", "reason"} {
		_ = dismiss.MarkFlagRequired(key)
	}
	cmd.AddCommand(dismiss)
	var proposalRevision uint64
	accept := &cobra.Command{Use: "accept-plan <proposal-id>", Short: "Accept the reviewed proposal into a local goal; does not publish or start work", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		return requestGoal(c, "goalAcceptPlan", struct {
			ID       string `json:"id"`
			Revision uint64 `json:"revision"`
		}{args[0], proposalRevision})
	}}
	accept.Flags().Uint64Var(&proposalRevision, "revision", 0, "Reviewed proposal revision (required)")
	_ = accept.MarkFlagRequired("revision")
	cmd.AddCommand(accept)
	var evidenceRevision uint64
	var evidenceURL string
	evidence := &cobra.Command{Use: "delivery-evidence <goal-id>", Short: "Attach a shared screenshot/runtime artifact before publishing goal PRs", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		return requestGoal(c, "goalDeliveryEvidence", goal.DeliveryEvidenceRequest{ID: args[0], Revision: evidenceRevision, URL: evidenceURL})
	}}
	evidence.Flags().Uint64Var(&evidenceRevision, "revision", 0, "Current goal revision (required)")
	evidence.Flags().StringVar(&evidenceURL, "url", "", "HTTPS URL of the shared verification artifact")
	_ = evidence.MarkFlagRequired("revision")
	_ = evidence.MarkFlagRequired("url")
	cmd.AddCommand(evidence)
	cmd.AddCommand(&cobra.Command{Use: "accept-brief <revision.json>", Short: "Accept an exact Linear brief snapshot against the retained task plan and policy", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		b, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		var a goal.BriefAcceptance
		if err = json.Unmarshal(b, &a); err != nil {
			return err
		}
		return requestGoal(c, "goalAcceptBrief", a)
	}})
	cmd.AddCommand(&cobra.Command{Use: "start <authorization.json>", Short: "Authorize the reviewed goal revision and bounded execution policy", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		b, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		var a goal.Authorization
		if err := json.Unmarshal(b, &a); err != nil {
			return err
		}
		if err := a.Policy.ValidateAuthorization(); err != nil {
			return err
		}
		return requestGoal(c, protocol.GoalStartV2, a)
	}})
	for _, action := range []string{"pause", "resume", "stop-workers", "cancel", "retry-verification", "retry-replan", "accept-delivery"} {
		var revision uint64
		var reason string
		control := &cobra.Command{Use: action + " <goal-id>", Short: action + " goal execution while preserving its work", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
			return requestGoal(c, "goalControl", goal.ControlRequest{ID: args[0], Revision: revision, Action: action, Reason: reason})
		}}
		control.Flags().Uint64Var(&revision, "revision", 0, "Current goal revision from goal show (required)")
		_ = control.MarkFlagRequired("revision")
		control.Flags().StringVar(&reason, "reason", "", "Reason recorded in the goal history")
		if action == "accept-delivery" {
			control.Short = "Record human acceptance of the exact verified and merged goal delivery"
			_ = control.MarkFlagRequired("reason")
		}
		cmd.AddCommand(control)
	}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List durable goals", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return requestGoal(c, "goals", nil) }})
	cmd.AddCommand(&cobra.Command{Use: "create <file.json>", Short: "Validate and reserve a goal locally (no Linear writes)", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			var draft goal.Draft
			if err := json.Unmarshal(b, &draft); err != nil {
				return err
			}
			if err := goal.ValidateDraft(draft); err != nil {
				return err
			}
			return requestGoal(c, "goalCreate", draft)
		}})
	for _, action := range []struct{ name, wire, description string }{
		{"sync-brief", "goalSyncBrief", "Read Linear edits into a durable proposal without overwriting human text"},
		{"usage", "goalUsage", "Show observed usage, coverage gaps, and accepted goal budgets"},
		{"show", "goalShow", "Show the goal contract, task routing, and Linear bindings"},
		{"publish", "goalPublish", "Create or reconcile Linear issues and dependencies; does not start workers"},
	} {
		cmd.AddCommand(&cobra.Command{Use: action.name + " <goal-id>", Short: action.description, Args: cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, args []string) error {
				return requestGoal(c, action.wire, protocol.GoalArgs{ID: args[0]})
			}})
	}
	return cmd
}

func requestGoal(c *cobra.Command, command string, args any) error {
	b, err := json.Marshal(args)
	if err != nil {
		return err
	}
	raw, err := tui.Request(protocol.Request{Cmd: command, Args: b})
	if err != nil {
		return err
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("invalid goal response: %w", err)
	}
	encoder := json.NewEncoder(c.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}
