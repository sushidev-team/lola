package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/daemon"
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
