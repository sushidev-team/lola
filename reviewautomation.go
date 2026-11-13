package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tui"
)

// Human automation uses the local socket, never the session-token agent bridge.
// Commands issue exactly one request; uncertain writes are not automatically retried.
func reviewAutomationCommands() []*cobra.Command {
	var commands []*cobra.Command
	request := func(c *cobra.Command, session, command string, args any) error {
		raw, err := json.Marshal(args)
		if err != nil {
			return err
		}
		data, err := tui.Request(protocol.Request{Cmd: command, Session: session, Args: raw})
		if err != nil {
			return err
		}
		_, err = c.OutOrStdout().Write(append(data, '\n'))
		return err
	}
	list := &cobra.Command{Use: "list <session>", Args: cobra.ExactArgs(1), Short: "List retained packages and inspection baselines as JSON", RunE: func(c *cobra.Command, a []string) error { return request(c, a[0], protocol.CmdReviewPackages, nil) }}
	commands = append(commands, list)
	var previous string
	mark := &cobra.Command{Use: "mark <session> <package>", Args: cobra.ExactArgs(2), Short: "Record inspection of an exact package, not approval", RunE: func(c *cobra.Command, a []string) error {
		return request(c, a[0], protocol.CmdReviewMark, protocol.ReviewMarkArgs{Package: a[1], Previous: previous})
	}}
	mark.Flags().StringVar(&previous, "previous", "", "Previously inspected package from list (optimistic concurrency)")
	commands = append(commands, mark)
	checks := &cobra.Command{Use: "checks <session> <package>", Args: cobra.ExactArgs(2), Short: "Read independently executed verification receipts", RunE: func(c *cobra.Command, a []string) error {
		return request(c, a[0], protocol.CmdReviewChecks, protocol.ReviewVerifyArgs{Package: a[1]})
	}}
	commands = append(commands, checks)
	var verification protocol.ReviewVerifyArgs
	verify := &cobra.Command{Use: "verify <session> <package>", Args: cobra.ExactArgs(2), Short: "Authorize a bounded offline check of the exact committed revision", RunE: func(c *cobra.Command, a []string) error {
		verification.Package = a[1]
		if err := verification.Spec.Validate(); err != nil {
			return err
		}
		return request(c, a[0], protocol.CmdReviewVerify, verification)
	}}
	verify.Flags().StringVar(&verification.ID, "request-id", "", "32 lowercase hex characters; reuse only to read the same request")
	verify.Flags().StringVar(&verification.Spec.ID, "name", "review", "Check name")
	verify.Flags().StringVar(&verification.Spec.Command, "command", "", "Command to execute in the disposable container")
	verify.Flags().StringVar(&verification.Spec.Image, "image", "", "Locally available image pinned by sha256 digest")
	verify.Flags().IntVar(&verification.Spec.TimeoutSeconds, "timeout", 300, "Command timeout in seconds (1–3600)")
	verify.Flags().IntVar(&verification.Spec.MemoryMiB, "memory", 2048, "Container memory in MiB (256–8192)")
	for _, flag := range []string{"request-id", "command", "image"} {
		_ = verify.MarkFlagRequired(flag)
	}
	commands = append(commands, verify)
	recover := &cobra.Command{Use: "recover <session> <package> <request-id>", Args: cobra.ExactArgs(3), Short: "Reconcile an interrupted verification without rerunning it", RunE: func(c *cobra.Command, a []string) error {
		return request(c, a[0], protocol.CmdReviewRecover, protocol.ReviewVerifyArgs{Package: a[1], ID: a[2]})
	}}
	commands = append(commands, recover)
	var file, section string
	feedback := &cobra.Command{Use: "feedback <session> <package>", Args: cobra.ExactArgs(2), Short: "Send a note bound to retained review material through the guarded queue", RunE: func(c *cobra.Command, a []string) error {
		var reader io.Reader = c.InOrStdin()
		if file != "-" {
			f, err := os.Open(file)
			if err != nil {
				return err
			}
			defer f.Close()
			reader = f
		}
		note, err := io.ReadAll(io.LimitReader(reader, 48001))
		if err != nil {
			return err
		}
		if len(note) > 48000 {
			return errors.New("feedback exceeds 48000 bytes")
		}
		return request(c, a[0], protocol.CmdReviewFeedback, protocol.ReviewFeedbackArgs{Package: a[1], Section: section, Note: string(note)})
	}}
	feedback.Flags().StringVar(&file, "file", "-", "UTF-8 note file; - reads standard input")
	feedback.Flags().StringVar(&section, "section", "Write-up", "Reviewed section or screenshot context")
	commands = append(commands, feedback)
	return commands
}
