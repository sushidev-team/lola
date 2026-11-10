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

	cmd.AddCommand(join, status, leave)
	return cmd
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
