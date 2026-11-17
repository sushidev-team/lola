package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewcapture"
	"github.com/sushidev-team/lola/internal/reviewpack"
)

func reviewPackageCmd() *cobra.Command {
	root := &cobra.Command{Use: "review-package", Short: "Publish, capture, inspect and verify retained PR review material"}
	var app string
	publish := &cobra.Command{
		Use: "publish <manifest.json>", Args: cobra.ExactArgs(1),
		Short: "Retain a Markdown write-up and checkout-relative PNG/JPEG screenshots",
		Long: `Publish a version 1 JSON manifest for $LOLA_SESSION. Images are relative to
the session checkout (the selected member checkout for stacks), not the manifest.
The daemon retains copies; no GitHub comments are posted. Repeating identical
content returns the original receipt. Failures exit nonzero; success prints JSON.

Collect evidence during implementation and testing. Reuse existing captures and
recorded results if they still describe the reviewed revision; refresh only
missing or stale evidence. Keep the manifest with the captures (e.g. .lola/review/).
Use a short title and body sections: Summary (one sentence), Changes (up to three
short bullets), Verification (commands/results), Risks (unresolved items only).
Include an Agent confidence section with two separate lines:
Score: N/100 (integer 0-100)
Basis: a brief rationale naming verification and remaining uncertainty.
Use 0-39 for substantial doubts, 40-69 for meaningful unverified behavior,
70-89 for strong evidence with bounded uncertainty, and 90-100 only for thorough
relevant verification with no known gaps. Pending human approval is not a penalty.
This is self-assessment, not a measured probability.
Put extended technical notes under Details. Do not repeat screenshot captions.

Example manifest:
{"version":1,"title":"Settings layout","body":"## Changes\nDescribe changes, checks and limitations.",
 "base":"<full baseline commit hash>","head":"<full reviewed commit hash>",
 "screens":[{"title":"Settings","before":".lola/before.png","after":".lola/after.png",
 "caption":"Explain the visible change","viewport":"1440 × 900","url":"http://localhost:3000/settings"}]}

Before is optional when a baseline capture is unavailable. Use an empty screens
array for changes without visual evidence. Limit: 64 KiB manifest, four pairs,
4 MiB and 20 megapixels per PNG/JPEG, 20 packages or 128 MiB retained per session.
Capture the actual revisions; dirty-worktree screenshots are not commit proof.`,
		RunE: func(c *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			raw, err := io.ReadAll(io.LimitReader(f, reviewpack.MaxManifest+1))
			if err != nil {
				return err
			}
			m, err := reviewpack.Decode(raw)
			if err != nil {
				return err
			}
			manifest, _ := json.Marshal(m)
			command := protocol.CmdReviewPublish
			if c.Name() == "complete" {
				command = protocol.CmdReviewComplete
			}
			resp, err := hook.SendAgent(command, protocol.ReviewPublishArgs{Manifest: manifest, App: app})
			if err != nil {
				return err
			}
			if !resp.OK {
				return errors.New(resp.Error)
			}
			_, err = fmt.Fprintln(c.OutOrStdout(), string(resp.Data))
			return err
		},
	}
	publish.Flags().StringVar(&app, "app", "", "Stack member name (required for stacks)")
	root.AddCommand(publish)
	root.AddCommand(reviewAutomationCommands()...)
	var capture reviewcapture.Options
	captureCmd := &cobra.Command{Use: "capture <url>", Args: cobra.ExactArgs(1), Short: "Capture a page or element with local Playwright; print a manifest screen entry", RunE: func(c *cobra.Command, args []string) error {
		capture.URL = args[0]
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		screen, err := reviewcapture.Capture(c.Context(), dir, capture, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(screen)
	}}
	captureCmd.Flags().StringVar(&capture.Destination, "output", "", "New checkout-relative PNG path (never overwrites)")
	_ = captureCmd.MarkFlagRequired("output")
	captureCmd.Flags().StringVar(&capture.Title, "title", "Page capture", "Screen title")
	captureCmd.Flags().StringVar(&capture.Selector, "selector", "", "Optional Playwright locator for one element")
	captureCmd.Flags().StringVar(&capture.Channel, "channel", "", "Use installed chrome or msedge instead of Playwright Chromium")
	captureCmd.Flags().IntVar(&capture.Width, "width", 1440, "Viewport width")
	captureCmd.Flags().IntVar(&capture.Height, "height", 900, "Viewport height")
	root.AddCommand(captureCmd)
	complete := &cobra.Command{Use: "complete <manifest.json>", Args: cobra.ExactArgs(1), Short: "Publish final material and report completion (a claim, never approval)", RunE: publish.RunE}
	complete.Flags().StringVar(&app, "app", "", "Stack member name (required for stacks)")
	root.AddCommand(complete)
	return root
}
