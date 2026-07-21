package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/daemon"
	"github.com/sushidev-team/lola/internal/doctor"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tui"
)

// Build metadata, injected by goreleaser ldflags (-X main.version=…). Plain
// `go build` leaves the dev defaults.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	root := &cobra.Command{
		Use:   "lola",
		Short: "Lola — run, observe, run again: Linear poller and agent dispatcher",
		// Runtime failures (daemon not running, bad config) are not usage
		// errors; print them once ourselves below.
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
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
		openCmd(),
		attachCmd(),
		killCmd(),
		reviveCmd(),
		answerCmd(),
		reviewCmd(),
		coderabbitCmd(),
		configCmd(),
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

// openCmd manually checks out a branch or PR of a project into a throwaway
// worktree with a plain shell so it can be run and tested (`lola open <project>
// <branch|PR#>`): a bare number is a PR (fetched via refs/pull/<n>/head), any
// other value a branch. No coding agent runs — the worktree is DETACHED, so
// teardown never touches the upstream branch. Tear it down later with `lola kill
// <session-id>` (the printed message names it). Send prints the outcome.
func openCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <project> <branch|PR#>",
		Short: "Open a branch or PR of a project in a throwaway worktree + shell to run and test it",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, a []string) error {
			raw, err := json.Marshal(protocol.Request{Cmd: "open", Project: a[0], Ref: a[1]})
			if err != nil {
				return err
			}
			return tui.Send(string(raw))
		},
	}
}

// attachCmd hands the terminal to tmux on lola's isolated server (`lola attach
// [session]`). With no argument it (re)builds a viewer session with one tab per
// live agent and attaches to it — "attach once, tab through every agent"; with a
// session id it attaches straight to that one. It drives tmux directly (no
// daemon needed) and blocks until you detach (Ctrl-b d). Unlike the other
// subcommands it does not go over the socket, so it lives outside the tui.Send
// path.
func attachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [session]",
		Short: "Attach to agents: no arg opens a tab-per-agent viewer; a session id attaches to just that one",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			session := ""
			if len(a) == 1 {
				session = a[0]
			}
			return tui.RunAttach(session)
		},
	}
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

// reviveCmd relaunches a dead session's coding agent on the worktree that was
// kept for inspection (`lola revive <session-id>`). It is the inverse of kill:
// where kill tears a session down, revive brings one back — Claude resumes its
// prior conversation via --continue when a transcript survived, otherwise the
// agent restarts fresh on the same worktree. The daemon refuses if the session
// is already running. Send prints the outcome or the daemon's error (e.g.
// "unknown session", "worktree is gone").
func reviveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revive <session-id>",
		Short: "Revive a dead session: relaunch its agent on the kept worktree (Claude resumes via --continue)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			raw, err := json.Marshal(protocol.Request{Cmd: "revive", Session: a[0]})
			if err != nil {
				return err
			}
			return tui.Send(string(raw))
		},
	}
}

// answerCmd delivers a human's inline reply to a session that stopped for input
// (`lola answer <session> <text...>`): it joins the remaining args into one line
// and sends cmd=answer, which the daemon types into the agent's pane — but only
// while that session is needs_input (the send-keys safety gate). The trailing
// args are the answer, so `lola answer FE-1 2` picks option 2 and `lola answer
// FE-1 use the smaller migration` types a free-form reply. Send prints ok or the
// daemon's error (e.g. "not waiting for input").
func answerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "answer <session> <text...>",
		Short: "Answer a session that is waiting for input (only when it is needs_input)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(c *cobra.Command, a []string) error {
			raw, err := json.Marshal(protocol.Request{Cmd: "answer", Session: a[0], Text: strings.Join(a[1:], " ")})
			if err != nil {
				return err
			}
			return tui.Send(string(raw))
		},
	}
}

// reviewCmd forces the P9 QA review pass for one session now (`lola review
// <session>`): it runs a bounded CodeRabbit review of the session's worktree,
// ignoring the once-per-PR guard, and routes the findings (human notification +
// optionally the worker agent and a Linear comment, per [review] config). Send
// prints the outcome — found issues / clean / skipped (review not enabled) — or
// the daemon's error.
func reviewCmd() *cobra.Command {
	var provider string
	cmd := &cobra.Command{
		Use:   "review <session>",
		Short: "Run the QA review pass for a session now (ignores the once-per-PR guard)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			raw, err := json.Marshal(protocol.Request{Cmd: "review", Session: a[0], Provider: provider})
			if err != nil {
				return err
			}
			return tui.Send(string(raw))
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "",
		"which pass provider to force (coderabbit-cli | claude-session); default: the primary enabled one")
	return cmd
}

// coderabbitCmd forces the [coderabbit] PR-comment watch for one session now
// (`lola coderabbit <session>`): it polls the session's open PR for CodeRabbit
// (GitHub-app) comments, ignoring the watermark, and routes any it finds (human
// notification + optionally the worker agent and a Linear comment, per
// [coderabbit] config). Send prints the outcome — routed feedback / none found /
// skipped (watch not enabled or no open PR) — or the daemon's error.
func coderabbitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "coderabbit <session>",
		Short: "Poll a session's PR for CodeRabbit comments now (ignores the watermark)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			raw, err := json.Marshal(protocol.Request{Cmd: "coderabbit", Session: a[0]})
			if err != nil {
				return err
			}
			return tui.Send(string(raw))
		},
	}
}

// hookCmd is the hidden callback target every coding agent's lifecycle wiring
// invokes as `lola hook <event>`. It serves all three agent kinds:
//   - Claude Code hooks (wired via hook.SettingsJSON) call it directly with a
//     normalized event and write the payload to stdin.
//   - The opencode plugin (OpenCodePluginJS) also calls it directly with a
//     normalized event ("stop"/"notification"/"tool_use") and empty stdin.
//   - Codex's `notify` (CodexConfigTOML) calls `lola hook codex-notify
//     '<json>'`, passing its payload as the NEXT argv element, not stdin; we
//     translate it via agent.ParseCodexNotify and skip unknown notify types.
//
// It best-effort extracts a detail string and posts the event to the daemon.
// It ALWAYS succeeds: a broken lola must never break the agent's turn, so
// failures go to stderr only and the process exits 0. DisableFlagParsing keeps
// even malformed argv (e.g. a JSON payload starting with `-`) from producing a
// cobra error.
func hookCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "hook <event>",
		Short:              "Coding-agent lifecycle callback (internal)",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(c *cobra.Command, args []string) error {
			event := ""
			if len(args) > 0 {
				event = args[0]
			}
			// Codex delivers its notify payload as the next argv element, not
			// stdin. Normalize it; an unknown notify type maps to "" — ignore
			// it and exit 0 without touching the daemon.
			if event == "codex-notify" {
				payload := ""
				if len(args) > 1 {
					payload = args[1]
				}
				normEvent, detail := agent.ParseCodexNotify(payload)
				if normEvent == "" {
					return nil
				}
				if err := hook.Post(normEvent, detail); err != nil {
					fmt.Fprintln(c.ErrOrStderr(), "lola hook:", err)
				}
				return nil
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

// configCmd groups low-level config maintenance. Its only (hidden) subcommand
// is the one-way `migrate-review`, which folds the legacy [review]/[coderabbit]
// tables into the canonical [[review.provider]] catalog — the explicit,
// opt-in resolution for the mutually-exclusive legacy+catalog hard error.
func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "config",
		Short:  "Config maintenance commands",
		Hidden: true,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "migrate-review",
		Short: "Migrate the legacy [review]/[coderabbit] tables into the [[review.provider]] catalog",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			path, err := config.DefaultPath()
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			had := cfg.Review != (config.ReviewConfig{}) || cfg.CodeRabbit != (config.CodeRabbitConfig{})
			if !had {
				fmt.Fprintln(c.OutOrStdout(), "nothing to migrate: no legacy [review]/[coderabbit] tables")
				return nil
			}
			config.MigrateLegacyReview(cfg)
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("migrated config is invalid: %w", err)
			}
			if err := cfg.Save(path); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "migrated legacy review tables into %d provider(s); run `lola reload`\n", len(cfg.ReviewProviders))
			return nil
		},
	})
	return cmd
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
