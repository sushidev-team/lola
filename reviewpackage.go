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
	"github.com/sushidev-team/lola/internal/reviewpack"
)

func reviewPackageCmd() *cobra.Command {
	root := &cobra.Command{Use: "review-package", Short: "Publish retained PR review material for this session"}
	var app string
	publish := &cobra.Command{
		Use: "publish <manifest.json>", Args: cobra.ExactArgs(1),
		Short: "Retain a Markdown write-up and checkout-relative PNG/JPEG screenshots",
		Long: `Publish a version 1 JSON manifest for $LOLA_SESSION. Images are relative to
the session checkout (the selected member checkout for stacks), not the manifest.
The daemon retains copies; no GitHub comments are posted. Repeating identical
content returns the original receipt. Failures exit nonzero; success prints JSON.

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
			resp, err := hook.SendAgent(protocol.CmdReviewPublish, protocol.ReviewPublishArgs{Manifest: manifest, App: app})
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
	return root
}
