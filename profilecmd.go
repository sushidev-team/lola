package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func profileCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "profile", Short: "Run and inspect independent specialist reviews"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List configured review profiles", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return requestGoal(c, "reviewProfiles", nil) }})
	cmd.AddCommand(&cobra.Command{Use: "results", Short: "Show input-bound specialist verdicts and missing evidence", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return requestGoal(c, "profileResults", nil) }})
	cmd.AddCommand(&cobra.Command{Use: "findings", Short: "Inspect durable specialist findings and decision history", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return requestGoal(c, "profileFindings", nil) }})
	cmd.AddCommand(&cobra.Command{Use: "calibration", Short: "Count explicit finding assessments by profile, rubric and repository", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return requestGoal(c, "profileCalibration", nil) }})
	var decision reviewprofile.FindingDecision
	decide := &cobra.Command{Use: "decide <finding-id>", Short: "Record an evidence-backed operator decision without changing review gates", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if os.Getenv("LOLA_SESSION") != "" {
				return fmt.Errorf("finding decisions require an operator")
			}
			if decision.Revision == 0 || decision.State == "" || decision.Evidence == "" {
				return fmt.Errorf("--revision, --state and --evidence required")
			}
			decision.ID = args[0]
			return requestGoal(c, "profileDecideFinding", decision)
		}}
	decide.Flags().Uint64Var(&decision.Revision, "revision", 0, "finding revision reviewed")
	decide.Flags().StringVar(&decision.State, "state", "", "accepted, disputed, dismissed, fixed or verified")
	decide.Flags().StringVar(&decision.Evidence, "evidence", "", "concrete evidence supporting this decision")
	decide.Flags().StringVar(&decision.FixHead, "fix-head", "", "exact fix commit for fixed or verified")
	decide.Flags().StringVar(&decision.VerificationKey, "verification", "", "clean independent review key for verified")
	cmd.AddCommand(decide)
	var profile, contractFile string
	var retry bool
	run := &cobra.Command{Use: "run <session>", Short: "Queue one bounded specialist review", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if profile == "" {
				return fmt.Errorf("--profile required")
			}
			contract := ""
			if contractFile != "" {
				b, err := os.ReadFile(contractFile)
				if err != nil {
					return err
				}
				contract = string(b)
			}
			return requestGoal(c, "profileReview", map[string]any{"session": args[0], "profile": profile, "contract": contract, "retry": retry})
		}}
	run.Flags().StringVar(&profile, "profile", "", "named profile from [[review.profile]]")
	run.Flags().StringVar(&contractFile, "contract", "", "acceptance brief file (default: the linked Linear issue)")
	run.Flags().BoolVar(&retry, "retry", false, "retry an incomplete/error result within the profile's attempt cap")
	cmd.AddCommand(run)
	return cmd
}
