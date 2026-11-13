package main

// `lola ssh`: the agent's verbs and the human's. The agent's are a real command and
// may fail, unlike `lola report`, because an agent must not mistake "nothing ran" for
// "it worked". Each ends with the remote exit code or one of lola's reserved codes,
// and every non-remote outcome also says why in one `lola ssh:` line on stderr, so a
// remote command that happens to exit 240 cannot be mistaken for a denial.
//
// A long wait never holds a connection: the CLI polls on a new short one each time,
// so the sandbox bridge's per-connection deadline is never in play. The block's life
// is the daemon's. A CLI that dies or gives up does not stop a command a human
// approved, unless it was started with --cancel-on-exit.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshelement"
	"github.com/sushidev-team/lola/internal/tui"
)

// sshExitError carries an exit code to main. The message is already on stderr.
type sshExitError struct{ code int }

func (e sshExitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

func sshExit(code int) error {
	if code == 0 {
		return nil
	}
	return sshExitError{code}
}

// Seams.
var (
	sshAgentSend = hook.SendAgent
	sshHumanSend = func(cmd string, args any, session string) (json.RawMessage, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		return tui.Request(protocol.Request{Cmd: cmd, Args: raw, Session: session})
	}
	sshPollStart = 250 * time.Millisecond
	sshPollMax   = 2 * time.Second
)

// defaultSSHWait is below Claude Code's Bash tool cap of 10 minutes on purpose: the
// CLI should come back with exit 243 and a block id rather than be killed.
const defaultSSHWait = 9 * time.Minute

// sshAgentCall sends one agent-side command. A failure to reach the daemon is
// reported as exit 248, and a daemon's own "no" as 246.
func sshAgentCall[T any](errOut io.Writer, cmd string, args any) (T, int) {
	var out T
	resp, err := sshAgentSend(cmd, args)
	if err != nil {
		if errors.Is(err, hook.ErrNoDaemon) {
			fmt.Fprintf(errOut, "lola ssh: cannot reach the lola daemon: %v\n", err)
			return out, protocol.SSHExitDaemon
		}
		fmt.Fprintf(errOut, "lola ssh: %v\n", err)
		return out, protocol.SSHExitPolicyRefused
	}
	if !resp.OK {
		fmt.Fprintf(errOut, "lola ssh: %s\n", strings.TrimSpace(resp.Error))
		return out, protocol.SSHExitPolicyRefused
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &out); err != nil {
			fmt.Fprintf(errOut, "lola ssh: bad reply from the daemon: %v\n", err)
			return out, protocol.SSHExitDaemon
		}
	}
	return out, 0
}

func sshPrintInbox(errOut io.Writer, inbox []string) {
	for _, m := range inbox {
		// Each entry is framed by lola already: "[lola] The human says: ...", or one of
		// its own lines about a command the agent stopped waiting for.
		fmt.Fprintf(errOut, "lola ssh: %s\n", m)
	}
}

// sshRefused prints a refusal and returns its exit code.
func sshRefused(errOut io.Writer, r *protocol.SSHRefusal) int {
	fmt.Fprintf(errOut, "lola ssh: %s\n", r.Message)
	return protocol.SSHRefusalExit(r.Code)
}

// sshOutcomeLine is the one line for a block that did not simply run to an exit.
// StatusDetail is written for the human, so the agent gets its own sentence here.
func sshOutcomeLine(b protocol.SSHBlock) string {
	switch b.Status {
	case protocol.SSHBlockDenied:
		if b.Approval.Reason != "" {
			return fmt.Sprintf("%s was denied by the human: %s", b.ID, b.Approval.Reason)
		}
		return b.ID + " was denied by the human."
	case protocol.SSHBlockExpired:
		return b.ID + " expired: nobody approved it in time, and it did not run."
	case protocol.SSHBlockRefused:
		switch b.RefusalCode {
		case protocol.SSHRefuseHumanControl:
			return b.ID + " did not run: the human has the wheel. Wait for the handback, do not retry."
		case protocol.SSHRefuseAwaitingGo:
			return b.ID + " did not run: the human has not given the go yet."
		case protocol.SSHRefuseNotConnected:
			return b.ID + " did not run: the connection is not up. Only a human can reconnect it."
		}
		return b.ID + " did not run: " + b.RefusalCode + "."
	case protocol.SSHBlockTimedOut:
		return b.ID + " timed out. The remote process may keep running."
	case protocol.SSHBlockStopped:
		return b.ID + " was stopped. The remote process may keep running."
	case protocol.SSHBlockLost:
		return b.ID + "'s result is unknown: the connection or the daemon went away. The remote process may still be running."
	}
	return ""
}

// sshFollow prints a block's output as it arrives and returns the exit code. It
// tolerates a few failed polls, because a daemon restarting is not a verdict on a
// command, then gives up with 248.
func sshFollow(out, errOut io.Writer, id string, wait time.Duration, cancelOnExit bool) int {
	deadline := time.Now().Add(wait)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)

	var soff, eoff int64
	delay := sshPollStart
	lastStatus := ""
	failures := 0
	for {
		d, code := sshAgentCall[protocol.SSHAgentPollData](io.Discard, protocol.CmdSSHAgentPoll, protocol.SSHAgentPollArgs{Block: id, StdoutOffset: soff, StderrOffset: eoff})
		if code == protocol.SSHExitDaemon {
			failures++
			if failures >= 5 {
				fmt.Fprintf(errOut, "lola ssh: cannot reach the lola daemon. %s may still be running: lola ssh wait %s\n", id, id)
				return protocol.SSHExitDaemon
			}
		} else if code != 0 {
			fmt.Fprintf(errOut, "lola ssh: no block %s in this session.\n", id)
			return code
		} else {
			failures = 0
			sshPrintInbox(errOut, d.Inbox)
			if d.Stdout != "" {
				fmt.Fprint(out, d.Stdout)
				delay = sshPollStart
			}
			if d.Stderr != "" {
				fmt.Fprint(errOut, d.Stderr)
				delay = sshPollStart
			}
			soff, eoff = d.StdoutOffset, d.StderrOffset
			if d.Block.Status != lastStatus {
				lastStatus = d.Block.Status
				if lastStatus == protocol.SSHBlockPendingApproval {
					fmt.Fprintf(errOut, "lola ssh: %s is waiting for a human's approval on %s (%s).\n", id, d.Block.Host, d.Block.Tier)
				}
			}
			if d.Done {
				if line := sshOutcomeLine(d.Block); line != "" {
					fmt.Fprintln(errOut, "lola ssh: "+line)
				}
				return protocol.SSHCLIExit(d.Block)
			}
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(errOut, "lola ssh: %s is still %s. Pick it up with: lola ssh wait %s\n", id, strings.ReplaceAll(lastStatus, "_", " "), id)
			return protocol.SSHExitStillWaiting
		}
		select {
		case <-sig:
			if cancelOnExit {
				_, _ = sshAgentSend(protocol.CmdSSHAgentCancel, protocol.SSHAgentCancelArgs{Block: id})
				fmt.Fprintf(errOut, "lola ssh: stopped %s. The remote process may keep running.\n", id)
				return protocol.SSHExitStopped
			}
			fmt.Fprintf(errOut, "lola ssh: leaving; %s lives on. Pick it up with: lola ssh wait %s\n", id, id)
			return protocol.SSHExitStillWaiting
		case <-time.After(delay):
		}
		delay = min(delay*2, sshPollMax)
	}
}

// sshCommandText rebuilds the command the agent wrote. One word is a shell line and
// goes through verbatim, which is how a pipe or && reaches the host. Several words
// already had their quotes removed by the agent's shell, so each is quoted again, or
// the remote shell would split `--format "table {{.Names}}"` into different arguments
// from the ones that were written and approved.
func sshCommandText(words []string) string {
	if len(words) == 1 {
		return words[0]
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = sshQuoteWord(w)
	}
	return strings.Join(quoted, " ")
}

// sshQuoteWord leaves a word alone when no shell would read it differently, so an
// approval shows `docker ps` and not `'docker' 'ps'`.
func sshQuoteWord(w string) string {
	if w != "" && strings.IndexFunc(w, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r))
	}) < 0 {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

func sshRunVerb() *cobra.Command {
	var why, cwd string
	var timeout int
	var wait time.Duration
	var detach, cancelOnExit bool
	c := &cobra.Command{
		Use:   "run <host> --why W [--cwd D] [--timeout N] [--wait 9m] [--detach] [--cancel-on-exit] -- cmd...",
		Short: "Run one command on a host (agent)",
		Long:  "Run one command on a connected host, recorded where the human can see it. It may wait for a human's approval.\n\nA command runs in its own shell, so exports do not carry over. The folder does: it starts where the last command on this connection ended, and --cwd overrides that. Separate words are quoted for you, so pass a pipe or && as one quoted string: lola ssh run web --why W -- \"ps aux | grep nginx\". Exits with the remote exit code, or 240 denied, 241 the human has the wheel, 242 not connected, 243 still waiting, 244 timed out, 245 stopped, 246 refused, 247 result unknown, 248 lola unreachable.",
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) < 2 {
				fmt.Fprintln(c.ErrOrStderr(), "lola ssh: run needs a host and a command: lola ssh run <host> --why W -- cmd...")
				return sshExit(protocol.SSHExitPolicyRefused)
			}
			d, code := sshAgentCall[protocol.SSHAgentRunData](c.ErrOrStderr(), protocol.CmdSSHAgentRun, protocol.SSHAgentRunArgs{
				Host: args[0], Why: why, Cwd: cwd, Command: sshCommandText(args[1:]), TimeoutSeconds: timeout,
			})
			if code != 0 {
				return sshExit(code)
			}
			sshPrintInbox(c.ErrOrStderr(), d.Inbox)
			if d.Refusal != nil {
				return sshExit(sshRefused(c.ErrOrStderr(), d.Refusal))
			}
			if d.Block == nil {
				fmt.Fprintln(c.ErrOrStderr(), "lola ssh: the daemon did not record a command.")
				return sshExit(protocol.SSHExitLost)
			}
			if d.Block.Cwd != "" {
				fmt.Fprintf(c.ErrOrStderr(), "lola ssh: in %s\n", d.Block.Cwd)
			}
			if detach {
				fmt.Fprintf(c.ErrOrStderr(), "lola ssh: %s is %s. Pick it up with: lola ssh wait %s\n", d.Block.ID, strings.ReplaceAll(d.Block.Status, "_", " "), d.Block.ID)
				return sshExit(protocol.SSHExitStillWaiting)
			}
			return sshExit(sshFollow(c.OutOrStdout(), c.ErrOrStderr(), d.Block.ID, wait, cancelOnExit))
		},
	}
	sshAgentFlagErrors(c)
	f := c.Flags()
	f.StringVar(&why, "why", "", "why you need this; shown to the human as your claim (required)")
	f.StringVar(&cwd, "cwd", "", "remote directory to run in")
	f.IntVar(&timeout, "timeout", 0, "seconds, clamped to the host's maximum")
	f.DurationVar(&wait, "wait", defaultSSHWait, "how long this CLI waits before exit 243; the command lives on")
	f.BoolVar(&detach, "detach", false, "return at once with exit 243 and the block id")
	f.BoolVar(&cancelOnExit, "cancel-on-exit", false, "stop the command when this CLI is interrupted")
	return c
}

// sshAgentFlagErrors makes a bad flag exit 246, like every other bad request.
func sshAgentFlagErrors(c *cobra.Command) {
	c.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		fmt.Fprintf(c.ErrOrStderr(), "lola ssh: %v\n", err)
		return sshExit(protocol.SSHExitPolicyRefused)
	})
}

func sshRequestVerb() *cobra.Command {
	var why, scope string
	var wait time.Duration
	c := &cobra.Command{
		Use:   "request <host> --why W [--scope S] [--wait 9m]",
		Short: "Ask for a connection and wait for the human's go (agent)",
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) != 1 {
				fmt.Fprintln(c.ErrOrStderr(), "lola ssh: request needs one host.")
				return sshExit(protocol.SSHExitPolicyRefused)
			}
			d, code := sshAgentCall[protocol.SSHAgentRequestData](c.ErrOrStderr(), protocol.CmdSSHAgentRequest, protocol.SSHAgentRequestArgs{Host: args[0], Why: why, Scope: scope})
			if code != 0 {
				return sshExit(code)
			}
			sshPrintInbox(c.ErrOrStderr(), d.Inbox)
			if d.Refusal != nil {
				return sshExit(sshRefused(c.ErrOrStderr(), d.Refusal))
			}
			return sshExit(sshAwaitConn(c.OutOrStdout(), c.ErrOrStderr(), args[0], d.Conn, wait))
		},
	}
	sshAgentFlagErrors(c)
	f := c.Flags()
	f.StringVar(&why, "why", "", "why you need this host; shown to the human as your claim (required)")
	f.StringVar(&scope, "scope", "", "what you say you will do; shown to the human as your claim")
	f.DurationVar(&wait, "wait", defaultSSHWait, "how long to wait for the human before exit 243")
	return c
}

// sshAwaitConn polls until a human has decided, and says how it went.
func sshAwaitConn(out, errOut io.Writer, host, conn string, wait time.Duration) int {
	deadline := time.Now().Add(wait)
	delay := sshPollStart
	last := ""
	failures := 0
	for {
		d, code := sshAgentCall[protocol.SSHAgentHostsData](io.Discard, protocol.CmdSSHAgentHosts, protocol.SSHAgentHostsArgs{Host: host})
		if code == protocol.SSHExitDaemon {
			if failures++; failures >= 5 {
				fmt.Fprintln(errOut, "lola ssh: cannot reach the lola daemon.")
				return code
			}
		} else if code != 0 {
			fmt.Fprintln(errOut, "lola ssh: the daemon refused to report on this host.")
			return code
		} else {
			failures = 0
			sshPrintInbox(errOut, d.Inbox)
			var row protocol.SSHAgentHost
			for _, h := range d.Hosts {
				if h.Name == host {
					row = h
				}
			}
			if row.State != last {
				last = row.State
				switch last {
				case protocol.SSHConnRequested:
					fmt.Fprintf(errOut, "lola ssh: waiting for the human's go on %s. Nothing has connected.\n", host)
				case protocol.SSHConnConnecting, protocol.SSHConnAuthenticating:
					fmt.Fprintf(errOut, "lola ssh: the human gave the go. They are signing in to %s.\n", host)
				}
			}
			switch last {
			case protocol.SSHConnConnected:
				if row.Wheel == protocol.SSHWheelHuman {
					fmt.Fprintf(errOut, "lola ssh: connected to %s, and the human has the wheel. Wait for the handback.\n", host)
					return protocol.SSHExitHumanControl
				}
				fmt.Fprintf(out, "connected %s %s\n", host, conn)
				return 0
			case protocol.SSHConnDenied:
				msg := "the human denied the connection to " + host + "."
				if row.Reason != "" {
					msg += " Their reason: " + row.Reason
				}
				fmt.Fprintln(errOut, "lola ssh: "+msg)
				return protocol.SSHExitDenied
			case protocol.SSHConnFailed, protocol.SSHConnDisconnected, protocol.SSHConnClosed, "":
				word := last
				if word == "" {
					word = "closed"
				}
				fmt.Fprintf(errOut, "lola ssh: the connection to %s is %s. Only a human can bring it back.\n", host, word)
				return protocol.SSHExitNotConnected
			}
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(errOut, "lola ssh: still waiting on the human for %s. Run this again to keep waiting.\n", host)
			return protocol.SSHExitStillWaiting
		}
		time.Sleep(delay)
		delay = min(delay*2, sshPollMax)
	}
}

func sshWaitVerb() *cobra.Command {
	var wait time.Duration
	var cancelOnExit bool
	c := &cobra.Command{
		Use:   "wait <block> [--wait 9m]",
		Short: "Pick up a command that was still waiting, printing all its output (agent)",
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) != 1 {
				fmt.Fprintln(c.ErrOrStderr(), "lola ssh: wait needs one block id.")
				return sshExit(protocol.SSHExitPolicyRefused)
			}
			return sshExit(sshFollow(c.OutOrStdout(), c.ErrOrStderr(), args[0], wait, cancelOnExit))
		},
	}
	sshAgentFlagErrors(c)
	c.Flags().DurationVar(&wait, "wait", defaultSSHWait, "how long this CLI waits before exit 243")
	c.Flags().BoolVar(&cancelOnExit, "cancel-on-exit", false, "stop the command when this CLI is interrupted")
	return c
}

func sshOutputVerb() *cobra.Command {
	var stream string
	c := &cobra.Command{
		Use:   "output <block> [--stream stdout|stderr]",
		Short: "Print what a command has produced so far (agent)",
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) != 1 {
				fmt.Fprintln(c.ErrOrStderr(), "lola ssh: output needs one block id.")
				return sshExit(protocol.SSHExitPolicyRefused)
			}
			var soff, eoff int64
			for {
				d, code := sshAgentCall[protocol.SSHAgentPollData](c.ErrOrStderr(), protocol.CmdSSHAgentPoll, protocol.SSHAgentPollArgs{Block: args[0], StdoutOffset: soff, StderrOffset: eoff})
				if code != 0 {
					return sshExit(code)
				}
				sshPrintInbox(c.ErrOrStderr(), d.Inbox)
				if stream != protocol.SSHStreamStderr {
					fmt.Fprint(c.OutOrStdout(), d.Stdout)
				}
				if stream != protocol.SSHStreamStdout {
					fmt.Fprint(c.ErrOrStderr(), d.Stderr)
				}
				if d.Done || (d.StdoutOffset == soff && d.StderrOffset == eoff) {
					if d.Block.OutputPruned {
						fmt.Fprintf(c.ErrOrStderr(), "lola ssh: %s's output was removed by retention.\n", args[0])
					}
					return nil
				}
				soff, eoff = d.StdoutOffset, d.StderrOffset
			}
		},
	}
	sshAgentFlagErrors(c)
	c.Flags().StringVar(&stream, "stream", "", "stdout or stderr; both when empty")
	return c
}

// sshExpandPostFiles turns --file and --data-file into --html and --data.
func sshExpandPostFiles(args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(out, args[i:]...), nil
		}
		name, val, inline := strings.Cut(a, "=")
		to := map[string]string{"--file": "--html", "--data-file": "--data"}[name]
		if to == "" {
			out = append(out, a)
			continue
		}
		if !inline {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s needs a path", name)
			}
			i++
			val = args[i]
		}
		info, err := os.Stat(val)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if info.Size() > sshelement.MaxWidgetHTML {
			return nil, fmt.Errorf("%s is %d bytes, the limit is %d", val, info.Size(), sshelement.MaxWidgetHTML)
		}
		b, err := os.ReadFile(val)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, to, string(b))
	}
	return out, nil
}

func sshGuideVerb() *cobra.Command {
	return &cobra.Command{
		Use:   "guide [answers|widgets|widget-template]",
		Short: "How to structure answers and write widgets (agent)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			topic := "answers"
			if len(args) == 1 {
				topic = args[0]
			}
			switch topic {
			case "answers":
				fmt.Fprint(c.OutOrStdout(), sshelement.GuideAnswers)
			case "widgets":
				fmt.Fprint(c.OutOrStdout(), sshelement.GuideWidgets)
			case "widget-template":
				fmt.Fprint(c.OutOrStdout(), sshelement.GuideWidgetTemplate)
			default:
				return fmt.Errorf("no guide named %q: try answers, widgets or widget-template", topic)
			}
			return nil
		},
	}
}

func sshPostVerb() *cobra.Command {
	return &cobra.Command{
		Use:                "post <kind> [text] [flags]",
		Short:              "Publish a timeline element for the humans watching (agent)",
		Long:               sshelement.Usage,
		DisableFlagParsing: true,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
				fmt.Fprint(c.OutOrStdout(), sshelement.Usage)
				return nil
			}
			// Files are read here, on the agent's side: a sandboxed agent's paths mean
			// nothing to the daemon, and the daemon must not open what an agent names.
			args, err := sshExpandPostFiles(args)
			if err != nil {
				fmt.Fprintln(c.ErrOrStderr(), "lola ssh: "+err.Error())
				return sshExit(protocol.SSHExitPolicyRefused)
			}
			d, code := sshAgentCall[protocol.SSHAgentPostData](c.ErrOrStderr(), protocol.CmdSSHAgentPost, protocol.SSHAgentPostArgs{Argv: args})
			if code != 0 {
				return sshExit(code)
			}
			sshPrintInbox(c.ErrOrStderr(), d.Inbox)
			fmt.Fprintln(c.OutOrStdout(), d.ID)
			sshPrintWidgetAdvice(c.OutOrStdout(), args)
			return nil
		},
	}
}

// sshPrintWidgetAdvice warns about a page that cannot work, right when the agent can still
// fix it. The post has gone through by now: this is advice, and the sandbox is the control.
func sshPrintWidgetAdvice(w io.Writer, args []string) {
	p, err := sshelement.Parse(args)
	if err != nil || p.Element.Kind != sshelement.KindWidget {
		return
	}
	for _, a := range sshelement.WidgetAdvice(p.Element.HTML) {
		fmt.Fprintln(w, "warning: "+a)
	}
}

func sshHostsVerb() *cobra.Command {
	return &cobra.Command{
		Use:   "hosts",
		Short: "List configured hosts: inside a session its standing, else what a direct session would do",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			w := tabwriter.NewWriter(c.OutOrStdout(), 0, 2, 2, ' ', 0)
			defer w.Flush()
			if os.Getenv("LOLA_SESSION") != "" {
				d, code := sshAgentCall[protocol.SSHAgentHostsData](c.ErrOrStderr(), protocol.CmdSSHAgentHosts, protocol.SSHAgentHostsArgs{})
				if code != 0 {
					return sshExit(code)
				}
				sshPrintInbox(c.ErrOrStderr(), d.Inbox)
				fmt.Fprintln(w, "HOST\tTIER\tUSER\tSTATE\tWHEEL\tNOTE")
				for _, h := range d.Hosts {
					note := h.Refused
					if note == "" {
						note = h.Reason
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", h.Name, h.Tier, dash(h.User), dash(h.State), dash(h.Wheel), note)
				}
				return nil
			}
			var d protocol.SSHHostsData
			if err := sshHuman(protocol.CmdSSHHosts, struct{}{}, "", &d); err != nil {
				return err
			}
			fmt.Fprintln(w, "HOST\tALIAS\tTIER\tUSER\tSANDBOX\tGRANT\tNOTE")
			for _, h := range d.Hosts {
				grant := "per command"
				if h.GrantMaxMinutes > 0 {
					grant = fmt.Sprintf("up to %dm", h.GrantMaxMinutes)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", h.Name, h.Alias, h.Tier, dash(h.User), dash(h.DirectSandbox), grant, h.DirectRefused)
			}
			return nil
		},
	}
}

func sshCwdWord(cwd string) string {
	if cwd == "" {
		return "login directory"
	}
	return cwd
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// sshHuman sends one human-side command and decodes the reply.
func sshHuman(cmd string, args any, session string, into any) error {
	raw, err := sshHumanSend(cmd, args, session)
	if err != nil {
		return err
	}
	if into == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}

func sshPrintConn(w io.Writer, c protocol.SSHConnInfo) {
	fmt.Fprintf(w, "%s  %s (%s)  %s  wheel: %s\n", c.ID, c.Host, c.Tier, c.State, c.Wheel)
	if c.StateDetail != "" {
		fmt.Fprintln(w, "  "+c.StateDetail)
	}
	if c.Tab != "" && c.TabAlive {
		fmt.Fprintf(w, "  terminal: tmux -L lola attach -t =%s\n", c.Tab)
	}
}

func sshConnectVerb() *cobra.Command {
	var drive bool
	c := &cobra.Command{
		Use:   "connect <session> <host> [--drive]",
		Short: "Connect an existing session to a host (human)",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			a := protocol.SSHConnectArgs{Session: args[0], Host: args[1]}
			if drive {
				a.Wheel = protocol.SSHWheelHuman
			}
			var d protocol.SSHConnectData
			if err := sshHuman(protocol.CmdSSHConnect, a, "", &d); err != nil {
				return err
			}
			w := c.OutOrStdout()
			sshPrintConn(w, d.Conn)
			if d.Warning != "" {
				fmt.Fprintln(w, d.Warning)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&drive, "drive", false, "start with the wheel yourself")
	return c
}

func sshOpenVerb() *cobra.Command {
	var agentKind, name, brief string
	var drive bool
	c := &cobra.Command{
		Use:   "open <host> [--agent K] [--name N] [--brief B] [--drive]",
		Short: "Start an agent-assisted session on a host (human)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			a := protocol.SSHOpenArgs{Host: args[0], AgentKind: agentKind, Name: name, Brief: brief}
			if drive {
				a.Wheel = protocol.SSHWheelHuman
			}
			var d protocol.SSHOpenData
			if err := sshHuman(protocol.CmdSSHOpen, a, "", &d); err != nil {
				return err
			}
			w := c.OutOrStdout()
			fmt.Fprintf(w, "session %s\n", d.Session)
			sshPrintConn(w, d.Conn)
			if d.Warning != "" {
				fmt.Fprintln(w, d.Warning)
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&agentKind, "agent", "", "coding agent: claude, codex or opencode")
	f.StringVar(&name, "name", "", "session name")
	f.StringVar(&brief, "brief", "", "what to tell the agent first; with none it waits for you")
	f.BoolVar(&drive, "drive", false, "start with you driving, to look around before asking")
	return c
}

func sshLsVerb() *cobra.Command {
	return &cobra.Command{
		Use:   "ls [session]",
		Short: "List connections and the commands waiting for approval (human)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			raw, err := tui.Request(protocol.Request{Cmd: "sessions"})
			if err != nil {
				return err
			}
			var data protocol.SessionsData
			if err := json.Unmarshal(raw, &data); err != nil {
				return err
			}
			w := c.OutOrStdout()
			found := false
			for _, s := range data.Sessions {
				if s.SSH == nil || (len(args) == 1 && s.ID != args[0]) {
					continue
				}
				found = true
				fmt.Fprintf(w, "%s\n", s.ID)
				if s.SSH.NeedsHuman {
					fmt.Fprintln(w, "  needs you: "+s.SSH.NeedsHumanWhy)
				}
				for _, conn := range s.SSH.Connections {
					fmt.Fprint(w, "  ")
					sshPrintConn(w, conn)
				}
				var tl protocol.SSHTimelineData
				if err := sshHuman(protocol.CmdSSHTimeline, protocol.SSHTimelineArgs{Session: s.ID}, "", &tl); err != nil {
					return err
				}
				pending := false
				for _, it := range tl.Items {
					if it.Block == nil || it.Block.Status != protocol.SSHBlockPendingApproval {
						continue
					}
					pending = true
					b := it.Block
					fmt.Fprintf(w, "  pending %s on %s (%s), conn %s\n    $ %s\n", b.ID, b.Host, b.Tier, b.Conn, b.Command)
					fmt.Fprintf(w, "    cwd: %s  user: %s\n    the agent says: %s\n", sshCwdWord(b.Cwd), dash(b.User), b.Why)
					for _, conn := range tl.Connections {
						if conn.ID == b.Conn {
							fmt.Fprintf(w, "    alias: %s  hostname: %s\n", conn.Alias, dash(conn.ResolvedHostname))
							if !conn.Sandboxed {
								fmt.Fprintln(w, "    "+protocol.SSHUnsandboxedNote)
							}
						}
					}
				}
				if pending {
					fmt.Fprintln(w, "  "+protocol.SSHDataWarning)
					fmt.Fprintln(w, "  "+protocol.SSHUserIsTheLimit)
				}
			}
			if !found {
				fmt.Fprintln(w, "no ssh connections")
			}
			return nil
		},
	}
}

func sshConnVerb(use, short string, cmd string, build func(args []string) any, nArgs int, print func(io.Writer, json.RawMessage)) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(nArgs),
		RunE: func(c *cobra.Command, args []string) error {
			var raw json.RawMessage
			if err := sshHuman(cmd, build(args), "", &raw); err != nil {
				return err
			}
			print(c.OutOrStdout(), raw)
			return nil
		},
	}
}

func sshPrintConnRaw(w io.Writer, raw json.RawMessage) {
	var c protocol.SSHConnInfo
	if json.Unmarshal(raw, &c) == nil {
		sshPrintConn(w, c)
	}
}

func sshPrintBlockRaw(w io.Writer, raw json.RawMessage) {
	var b protocol.SSHBlock
	if json.Unmarshal(raw, &b) == nil {
		fmt.Fprintf(w, "%s %s\n", b.ID, b.Status)
	}
}

func sshCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ssh",
		Short: "Agent-assisted ssh: a local agent works on a remote host through lola, with a human watching",
		Long: "Agent-assisted ssh. The agent stays local and holds no key: lola owns the connection, a human authenticates in a real terminal, and every command is recorded where the human can see it.\n\n" +
			"The request, run, wait, output and post verbs are for the agent inside a session. The rest are for a human.\n\n" +
			protocol.SSHDataWarning + " " + protocol.SSHUserIsTheLimit,
	}
	root.AddCommand(sshHostsVerb(), sshRequestVerb(), sshRunVerb(), sshWaitVerb(), sshOutputVerb(), sshPostVerb(), sshGuideVerb(), sshOpenVerb(), sshConnectVerb(), sshLsVerb())

	root.AddCommand(sshConnVerb("go <conn>", "Give the go for a connection, or reconnect one (human)", protocol.CmdSSHGo,
		func(a []string) any { return protocol.SSHGoArgs{Conn: a[0]} }, 1,
		func(w io.Writer, raw json.RawMessage) {
			sshPrintConnRaw(w, raw)
			fmt.Fprintln(w, protocol.SSHDataWarning)
		}))

	var denyReason string
	deny := sshConnVerb("deny <conn>", "Deny a requested connection (human)", protocol.CmdSSHDeny,
		func(a []string) any { return protocol.SSHDenyArgs{Conn: a[0], Reason: denyReason} }, 1, sshPrintConnRaw)
	deny.Flags().StringVar(&denyReason, "reason", "", "returned to the agent")
	root.AddCommand(deny)

	var grant int
	approve := sshConnVerb("approve <conn> <block>", "Approve one command (human)", protocol.CmdSSHApprove,
		func(a []string) any {
			return protocol.SSHApproveArgs{Conn: a[0], Block: a[1], Decision: protocol.SSHDecisionApprove, GrantMinutes: grant}
		}, 2, func(w io.Writer, raw json.RawMessage) {
			sshPrintBlockRaw(w, raw)
			fmt.Fprintln(w, protocol.SSHDataWarning)
		})
	approve.Flags().IntVar(&grant, "grant", 0, "also allow every command for this many minutes (not on production)")
	root.AddCommand(approve)

	var rejectReason string
	reject := sshConnVerb("reject <conn> <block>", "Deny one command (human)", protocol.CmdSSHApprove,
		func(a []string) any {
			return protocol.SSHApproveArgs{Conn: a[0], Block: a[1], Decision: protocol.SSHDecisionDeny, Reason: rejectReason}
		}, 2, sshPrintBlockRaw)
	reject.Flags().StringVar(&rejectReason, "reason", "", "returned to the agent")
	root.AddCommand(reject)

	var stopInFlight bool
	take := sshConnVerb("take <conn>", "Take the wheel: lola ssh run is refused until you hand back (human)", protocol.CmdSSHTakeWheel,
		func(a []string) any { return protocol.SSHTakeWheelArgs{Conn: a[0], StopInFlight: stopInFlight} }, 1,
		func(w io.Writer, raw json.RawMessage) {
			var d protocol.SSHTakeWheelData
			if json.Unmarshal(raw, &d) != nil {
				return
			}
			sshPrintConn(w, d.Conn)
			for _, b := range d.InFlight {
				fmt.Fprintf(w, "  in flight: %s %s  $ %s\n", b.ID, b.Status, b.Command)
			}
			if d.Warning != "" {
				fmt.Fprintln(w, d.Warning)
			}
			fmt.Fprintln(w, protocol.SSHDrivingNote)
		})
	take.Flags().BoolVar(&stopInFlight, "stop", false, "also stop commands in flight")
	root.AddCommand(take)

	root.AddCommand(sshHandbackVerb())

	root.AddCommand(sshConnVerb("stop <conn> <block>", "Stop a command (human)", protocol.CmdSSHStop,
		func(a []string) any { return protocol.SSHStopArgs{Conn: a[0], Block: a[1]} }, 2,
		func(w io.Writer, raw json.RawMessage) {
			var d protocol.SSHStopData
			if json.Unmarshal(raw, &d) != nil {
				return
			}
			fmt.Fprintf(w, "%s %s\n", d.Block.ID, d.Block.Status)
			if d.Warning != "" {
				fmt.Fprintln(w, d.Warning)
			}
		}))
	root.AddCommand(sshConnVerb("close <conn>", "Close a connection (human)", protocol.CmdSSHClose,
		func(a []string) any { return protocol.SSHConnArgs{Conn: a[0]} }, 1,
		func(w io.Writer, raw json.RawMessage) {
			var d protocol.SSHCloseData
			if json.Unmarshal(raw, &d) != nil {
				return
			}
			sshPrintConn(w, d.Conn)
			for _, m := range d.Warnings {
				fmt.Fprintln(w, m)
			}
		}))
	root.AddCommand(sshConnVerb("trash <conn>", "Close a connection and delete its record for good (human)", protocol.CmdSSHTrash,
		func(a []string) any { return protocol.SSHConnArgs{Conn: a[0]} }, 1,
		func(w io.Writer, raw json.RawMessage) {
			var d protocol.SSHCloseData
			if json.Unmarshal(raw, &d) != nil {
				return
			}
			for _, m := range d.Warnings {
				fmt.Fprintln(w, m)
			}
		}))
	root.AddCommand(sshConnVerb("terminal <conn>", "Open a new shell on a live connection (human)", protocol.CmdSSHTerminal,
		func(a []string) any { return protocol.SSHConnArgs{Conn: a[0]} }, 1, sshPrintConnRaw))
	return root
}

func sshHandbackVerb() *cobra.Command {
	var note string
	var transcript bool
	c := &cobra.Command{
		Use:   "handback <conn> [--note N] [--transcript]",
		Short: "Give the wheel back (human)",
		Long:  "Give the wheel back to the agent. With --transcript, what your terminal shows since you took the wheel goes along.\n\n" + protocol.SSHTranscriptWarning,
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			a := protocol.SSHHandbackArgs{Conn: args[0], Note: note}
			if transcript {
				var p protocol.SSHHandbackPreviewData
				if err := sshHuman(protocol.CmdSSHHandbackPreview, protocol.SSHConnArgs{Conn: args[0]}, "", &p); err != nil {
					return err
				}
				a.Transcript = p.Transcript
			}
			var d protocol.SSHHandbackData
			if err := sshHuman(protocol.CmdSSHHandback, a, "", &d); err != nil {
				return err
			}
			w := c.OutOrStdout()
			sshPrintConn(w, d.Conn)
			if d.Delivered {
				fmt.Fprintln(w, "Delivered to the agent.")
			} else {
				fmt.Fprintln(w, "Queued. It reaches the agent on its next lola ssh command or at its prompt.")
			}
			return nil
		},
	}
	c.Flags().StringVar(&note, "note", "", "what you want to tell the agent")
	c.Flags().BoolVar(&transcript, "transcript", false, "send what your terminal showed while you drove")
	return c
}
