package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/daemon"
	"github.com/sushidev-team/lola/internal/doctor"
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
		killCmd(),
		logsCmd(),
		doctorCmd(),
		setupCmd(),
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

// killCmd terminates one native session and cleans up after it: `lola kill
// <session-id>` kills the agent's tmux session and, when the worktree is clean,
// removes the worktree and frees the issue's slot. A dirty worktree (uncommitted
// changes) is kept and the command exits nonzero telling you to rerun with
// --force; --force removes it anyway. The outcome message is printed by Send.
func killCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use: "kill <session-id>", Short: "Kill a session; remove its clean worktree (--force removes a dirty one)", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			return tui.Send(fmt.Sprintf(`{"cmd":"kill","session":%q,"force":%t}`, a[0], force))
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove the worktree even with uncommitted changes")
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

// setupCmd always runs the first-run configuration wizard, even when a config
// already exists (bare `lola` only enters the wizard when none does).
func setupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Run the first-run configuration wizard (Linear key, project, defaults)",
		RunE:  func(c *cobra.Command, _ []string) error { return tui.Setup() },
	}
}

// doctorCmd runs the structured health checks and prints an aligned report.
// It exits 1 when a critical check failed (Report.OK() is false). The Linear
// API key value is never printed — doctor reports only where a key was found.
func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check lola's runtime health (tools, keychain key, daemon, config)",
		RunE: func(c *cobra.Command, _ []string) error {
			if !runDoctor(context.Background(), c.OutOrStdout()) {
				os.Exit(1)
			}
			return nil
		},
	}
}

// runDoctor loads the config best-effort, runs the checks, writes the rendered
// report to w, and reports whether every critical check passed. Split out from
// doctorCmd so tests can assert the outcome without os.Exit.
func runDoctor(ctx context.Context, w io.Writer) bool {
	rep := doctor.Check(ctx, loadConfigBestEffort())
	fmt.Fprint(w, renderDoctorReport(rep))
	return rep.OK()
}

// loadConfigBestEffort returns the loaded config, or nil when the config is
// absent or unreadable. A nil config makes doctor skip the config-dependent
// checks with a single explanatory note rather than hard-failing on defaults.
func loadConfigBestEffort() *config.Config {
	path, err := config.DefaultPath()
	if err != nil {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil // no config yet (first run): skip config-dependent checks
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil
	}
	return cfg
}

var (
	doctorOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	doctorWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	doctorFail = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// renderDoctorReport formats the report as aligned "<glyph> <name> <detail>"
// lines followed by the one-line summary. lipgloss auto-degrades the color to
// plain text when stdout is not a terminal.
func renderDoctorReport(rep doctor.Report) string {
	nameW := 0
	for _, r := range rep.Results {
		if w := lipgloss.Width(r.Name); w > nameW {
			nameW = w
		}
	}
	var b strings.Builder
	for _, r := range rep.Results {
		var glyph string
		switch {
		case r.OK:
			glyph = doctorOK.Render("✓")
		case r.Critical:
			glyph = doctorFail.Render("✗")
		default:
			glyph = doctorWarn.Render("⚠")
		}
		pad := strings.Repeat(" ", nameW-lipgloss.Width(r.Name))
		fmt.Fprintf(&b, "%s  %s%s  %s\n", glyph, r.Name, pad, r.Detail)
	}
	b.WriteString("\n" + rep.Summary() + "\n")
	return b.String()
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
