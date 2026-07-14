package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/daemon"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/tui"
)

func main() {
	root := &cobra.Command{
		Use:   "lola",
		Short: "Lola — run, observe, run again: Linear poller and agent dispatcher",
		// Runtime failures (daemon not running, bad config) are not usage
		// errors; print them once ourselves below.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(c *cobra.Command, _ []string) error { return tui.Run() }, // bare `lola` opens TUI
	}

	root.AddCommand(
		&cobra.Command{Use: "tui", Short: "Open the TUI",
			RunE: func(c *cobra.Command, _ []string) error { return tui.Run() }},
		&cobra.Command{Use: "run", Short: "Start the daemon (launchd calls this)",
			RunE: func(c *cobra.Command, _ []string) error { return daemon.Run(context.Background()) }},
		&cobra.Command{Use: "stop", Short: "Stop the daemon",
			RunE: func(c *cobra.Command, _ []string) error { return tui.Send(`{"cmd":"stop"}`) }},
		&cobra.Command{Use: "status", Short: "Show poll status",
			RunE: func(c *cobra.Command, _ []string) error { return tui.Send(`{"cmd":"status"}`) }},
		&cobra.Command{Use: "reload", Short: "Reload config",
			RunE: func(c *cobra.Command, _ []string) error { return tui.Send(`{"cmd":"reload"}`) }},
		enableCmd("enable"), enableCmd("disable"),
		pollCmd(),
		logsCmd(),
		hookCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func enableCmd(verb string) *cobra.Command {
	return &cobra.Command{
		Use: verb + " <poll>", Short: verb + " a poll (live pause/resume)", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			return tui.Send(fmt.Sprintf(`{"cmd":%q,"poll":%q}`, verb, a[0]))
		},
	}
}

func pollCmd() *cobra.Command {
	var once, dry bool
	cmd := &cobra.Command{
		Use: "poll <poll>", Short: "Run one tick now (--once, optionally --dry-run)", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			if !once {
				return fmt.Errorf("use --once")
			}
			return tui.Send(fmt.Sprintf(`{"cmd":"pollOnce","poll":%q,"dryRun":%t}`, a[0], dry))
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, "run one tick now")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "print matches, no side effects")
	return cmd
}

// hookCmd is the hidden callback target Claude Code hooks invoke as
// `lola hook <event>` (wired via hook.SettingsJSON). It drains the hook
// payload from stdin, best-effort extracts a detail string, and posts the
// event to the daemon. It ALWAYS succeeds: a broken lola must never break the
// agent's turn, so failures go to stderr only and the process exits 0.
// DisableFlagParsing keeps even malformed argv from producing a cobra error.
func hookCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "hook <event>",
		Short:              "Claude Code hook callback (internal)",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(c *cobra.Command, args []string) error {
			event := ""
			if len(args) > 0 {
				event = args[0]
			}
			if err := hook.Post(event, hookDetail(c.InOrStdin())); err != nil {
				fmt.Fprintln(c.ErrOrStderr(), "lola hook:", err)
			}
			return nil
		},
	}
}

// hookDetail drains the hook's stdin (Claude Code writes the event payload
// there; it must be consumed either way) and best-effort extracts the most
// specific reason field. Any read or parse failure yields "".
func hookDetail(r io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return ""
	}
	var p struct {
		NotificationType string `json:"notification_type"` // Notification
		StopReason       string `json:"stop_reason"`       // Stop
		EndReason        string `json:"end_reason"`        // SessionEnd
	}
	_ = json.Unmarshal(raw, &p)
	for _, s := range []string{p.NotificationType, p.StopReason, p.EndReason} {
		if s != "" {
			return s
		}
	}
	return ""
}

func logsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use: "logs [poll]", Args: cobra.MaximumNArgs(1),
		Short: "Tail the daemon log (optionally filtered by poll)",
		RunE: func(c *cobra.Command, a []string) error {
			poll := ""
			if len(a) == 1 {
				poll = a[0]
			}
			return tui.Logs(poll, follow)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow the log")
	return cmd
}
