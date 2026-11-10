package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tui"
)

// hubStatusCmd is the daemon socket command `lola hub status` asks for the
// daemon's hub loop state. A daemon predating the hub loop answers
// `unknown cmd`, which status tolerates.
const hubStatusCmd = protocol.CmdHubStatus

// hubJoinTimeout bounds the whole enrollment round trip.
const hubJoinTimeout = 60 * time.Second

func hubCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "hub", Short: "Join, inspect or leave a team lola hub"}

	var name, tokenFile string
	join := &cobra.Command{
		Use:   "join <url>",
		Short: "Enroll this machine with a lola hub using a one-time token",
		Long: "Enroll this machine with a lola hub. The one-time token is read from stdin\n" +
			"(prompted for without echo on a terminal) or from --token-file, never from\n" +
			"the command line: an argument is visible to every local user in ps and is\n" +
			"kept in shell history. A token given as a second argument still works, with\n" +
			"a warning.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(c *cobra.Command, a []string) error {
			token, err := hubJoinToken(a, tokenFile, c.InOrStdin(), c.ErrOrStderr(), stdinTerminal)
			if err != nil {
				return err
			}
			// A running daemon enrolls itself (and reloads); a daemon that is
			// down, or predates cmd=hubJoin, falls back to the local join.
			if resp, ok := hubDaemon(protocol.CmdHubJoin, protocol.HubJoinArgs{URL: a[0], Token: token, Name: name}); ok {
				if !resp.OK {
					return errors.New(resp.Error)
				}
				var jd protocol.HubJoinData
				if err := json.Unmarshal(resp.Data, &jd); err != nil {
					return err
				}
				if jd.KeyCreated {
					fmt.Fprintln(c.OutOrStdout(), "created a new device key")
				}
				fmt.Fprintf(c.OutOrStdout(), "joined org %s as %q (device %s)\n", jd.Org, jd.DeviceName, jd.DeviceID)
				fmt.Fprintln(c.OutOrStdout(), "opt a project in with [[project]].hub_chores = true")
				return nil
			}
			path, state, err := hubPaths()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context(), hubJoinTimeout)
			defer cancel()
			res, err := hub.Join(ctx, hub.JoinOptions{URL: a[0], Token: token, DeviceName: name, ConfigPath: path, StateDir: state})
			if err != nil {
				return err
			}
			if res.KeyCreated {
				fmt.Fprintln(c.OutOrStdout(), "created a new device key")
			}
			fmt.Fprintf(c.OutOrStdout(), "joined org %s as %q (device %s)\n", res.Identity.Org, res.Identity.DeviceName, res.Identity.DeviceID)
			fmt.Fprintln(c.OutOrStdout(), "opt a project in with [[project]].hub_chores = true")
			reloadDaemon(c)
			return nil
		},
	}
	join.Flags().StringVar(&name, "name", "", "device name shown to the team (default: hostname)")
	join.Flags().StringVar(&tokenFile, "token-file", "", "read the one-time token from this file (\"-\" = stdin)")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show the hub identity, the verified team policy and the daemon's hub state",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			path, state, err := hubPaths()
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			fmt.Fprint(c.OutOrStdout(), hub.LocalStatus(cfg, state).Render())
			data, err := tui.Request(protocol.Request{Cmd: hubStatusCmd})
			switch {
			case err != nil && strings.Contains(err.Error(), "unknown cmd"):
				fmt.Fprintln(c.OutOrStdout(), "daemon:   no hub loop in the running daemon")
			case err != nil:
				fmt.Fprintf(c.OutOrStdout(), "daemon:   %v\n", err)
			default:
				var out bytes.Buffer
				if json.Indent(&out, data, "          ", "  ") != nil {
					out.Reset()
					out.Write(data)
				}
				fmt.Fprintf(c.OutOrStdout(), "daemon:   %s\n", out.String())
			}
			return nil
		},
	}

	var forget bool
	leave := &cobra.Command{
		Use:   "leave",
		Short: "Remove the hub identity and disable the hub (keeps the device key unless --forget-key)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if resp, ok := hubDaemon(protocol.CmdHubLeave, protocol.HubLeaveArgs{ForgetKey: forget}); ok {
				if !resp.OK {
					return errors.New(resp.Error)
				}
				fmt.Fprintln(c.OutOrStdout(), "left the hub; an admin revokes the device on the hub side")
				return nil
			}
			path, state, err := hubPaths()
			if err != nil {
				return err
			}
			if err := hub.Leave(hub.LeaveOptions{ConfigPath: path, StateDir: state, ForgetKey: forget}); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "left the hub; an admin revokes the device on the hub side")
			reloadDaemon(c)
			return nil
		},
	}
	leave.Flags().BoolVar(&forget, "forget-key", false, "also delete the device key")

	cmd.AddCommand(join, status, leave, hubBoardCmd(), hubClaimCmd(), hubReleaseCmd(), hubSyncCmd())
	return cmd
}

// hubSend is the socket seam (tests replace it).
var hubSend = tui.RequestResponse

// hubDaemon sends one hub command. ok=false means "no daemon to ask": it is
// not running, or it predates the command — the caller falls back to the
// local path. A daemon that refused answers ok=true with resp.OK=false.
func hubDaemon(cmd string, args any) (*protocol.Response, bool) {
	req := protocol.Request{Cmd: cmd}
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, false
		}
		req.Args = raw
	}
	resp, err := hubSend(req)
	if err != nil || resp == nil {
		return nil, false
	}
	if !resp.OK && strings.Contains(resp.Error, "unknown cmd") {
		return nil, false
	}
	return resp, true
}

// hubDaemonData is hubDaemon for a command with no local fallback: the
// daemon must run, and its data is decoded into out.
func hubDaemonData(cmd string, args, out any) error {
	resp, ok := hubDaemon(cmd, args)
	if !ok {
		return errors.New("the daemon is not running, or predates this command: start or restart it (`lola run`)")
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(resp.Data, out)
}

func hubBoardCmd() *cobra.Command {
	var state string
	var limit int
	var fresh, asJSON bool
	cmd := &cobra.Command{
		Use:   "board",
		Short: "List the team's hub items and whether this machine could claim each",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			var b protocol.HubBoardData
			if err := hubDaemonData(protocol.CmdHubBoard, protocol.HubBoardArgs{State: state, Limit: limit, Fresh: fresh}, &b); err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(b)
			}
			fmt.Fprint(c.OutOrStdout(), renderHubBoard(b))
			return nil
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "only items in this state (open|claimed|done|failed|escalated)")
	cmd.Flags().IntVar(&limit, "limit", 0, "at most this many items (default: the hub's, max 500)")
	cmd.Flags().BoolVar(&fresh, "fresh", false, "skip the daemon's 20s cache")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the daemon's JSON")
	return cmd
}

// renderHubBoard is the board as a terminal table.
func renderHubBoard(b protocol.HubBoardData) string {
	if len(b.Items) == 0 {
		return "no items on the board\n"
	}
	var out strings.Builder
	tw := tabwriter.NewWriter(&out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tITEM\tREPO\tHERE")
	for _, it := range b.Items {
		here := it.Local.Reason
		switch {
		case it.Local.Session != "":
			here = "session " + it.Local.Session
		case it.Local.Claimable && it.Local.Project != "":
			here = "claimable (" + it.Local.Project + ")"
		case it.Local.Claimable:
			here = "claimable (--project " + strings.Join(it.Local.Projects, "|") + ")"
		}
		state := it.State
		if it.Outcome != nil {
			state += " " + it.Outcome.PRURL
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", it.ID, state, it.Label, it.Repo, here)
	}
	tw.Flush()
	return out.String()
}

func hubClaimCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "claim <item-id>",
		Short: "Claim one hub item now and start a session for it",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			var d protocol.HubClaimData
			if err := hubDaemonData(protocol.CmdHubClaim, protocol.HubClaimArgs{ItemID: a[0], Project: project}, &d); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "claimed %s for %s: session %s\n", d.ItemID, d.Project, d.Session)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "the [[project]] to work it in, when several match its repo")
	return cmd
}

func hubReleaseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "release <item-id>",
		Short: "Give a claimed hub item back (a session working it keeps running)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			var d protocol.HubReleaseData
			if err := hubDaemonData(protocol.CmdHubRelease, protocol.HubReleaseArgs{ItemID: a[0]}, &d); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), d.Message)
			return nil
		},
	}
}

func hubSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Run one hub loop pass now",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := hubDaemonData(protocol.CmdHubSync, nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "hub pass queued")
			return nil
		},
	}
}

func hubPaths() (cfgPath, stateDir string, err error) {
	if cfgPath, err = config.DefaultPath(); err != nil {
		return "", "", err
	}
	stateDir, err = hub.StateDir()
	return cfgPath, stateDir, err
}

// reloadDaemon asks a running daemon to re-read the config. Best effort: a
// daemon that is down picks the change up when it starts.
func reloadDaemon(c *cobra.Command) {
	if _, err := tui.Request(protocol.Request{Cmd: "reload"}); err != nil {
		fmt.Fprintln(c.ErrOrStderr(), "daemon not reloaded (it reads the config when it starts):", err)
	}
}

// hubTokenMax bounds what is read as a token.
const hubTokenMax = 4 << 10

// stdinTerminal reports whether stdin is a terminal, and its fd.
func stdinTerminal() (uintptr, bool) {
	fd := os.Stdin.Fd()
	return fd, term.IsTerminal(fd)
}

// hubJoinToken resolves the enroll token: a positional argument (deprecated:
// it shows in ps and shell history, so it warns), --token-file, or stdin —
// read without echo when stdin is a terminal.
func hubJoinToken(args []string, tokenFile string, stdin io.Reader, stderr io.Writer, tty func() (uintptr, bool)) (string, error) {
	var raw []byte
	var err error
	switch {
	case len(args) > 1:
		if tokenFile != "" {
			return "", errors.New("give the token either as an argument or with --token-file, not both")
		}
		fmt.Fprintln(stderr, "warning: a token on the command line is visible in ps and kept in shell history; pipe it on stdin or use --token-file")
		raw = []byte(args[1])
	case tokenFile != "" && tokenFile != "-":
		f, ferr := os.Open(tokenFile)
		if ferr != nil {
			return "", ferr
		}
		defer f.Close()
		raw, err = io.ReadAll(io.LimitReader(f, hubTokenMax))
	default:
		if fd, ok := tty(); ok && tokenFile == "" {
			fmt.Fprint(stderr, "one-time enroll token: ")
			raw, err = term.ReadPassword(fd)
			fmt.Fprintln(stderr)
		} else {
			raw, err = io.ReadAll(io.LimitReader(stdin, hubTokenMax))
		}
	}
	if err != nil {
		return "", fmt.Errorf("read the enroll token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("no enroll token: pipe it on stdin or use --token-file")
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("the enroll token must be a single line with no spaces")
	}
	return token, nil
}
