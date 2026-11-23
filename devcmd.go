package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/protocol"
)

// devCmd lets a coding agent run its OWN session's dev server through lola
// (the project's dev_commands, in lola's dev tabs) instead of a private process
// the humans cannot see. The addresses it prints are the ones the review
// package can name as live pages. It acts only on $LOLA_SESSION and goes over
// the socket the agent already uses; a sandboxed agent's bridge does not admit
// it, and the daemon's own rules for a dev start (one holder per project, port
// clashes, goal previews) apply unchanged.
func devCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "dev",
		Short: "Start, stop or inspect this session's dev server (agent)",
		Long: `Run this session's dev server through lola so humans can open it in the
embedded preview. 'start' waits briefly for the server's addresses and prints
them; use them as "pages" in a review-package manifest. If the project's fixed
ports stop the server from running next to another one, ask the human with
'lola report ports "<why>"' instead of editing port settings yourself.`,
	}
	var wait time.Duration
	start := &cobra.Command{
		Use: "start [why]", Args: cobra.ArbitraryArgs, Short: "Start the dev commands and print the addresses they serve (a sandboxed session asks the human instead)",
		RunE: func(c *cobra.Command, _ []string) error {
			if os.Getenv("LOLA_SANDBOX") != "" {
				return devAsk(c.OutOrStdout(), strings.Join(c.Flags().Args(), " "))
			}
			if err := devToggle(true); err != nil {
				return err
			}
			deadline := time.Now().Add(wait)
			for {
				urls, active, err := devStatus()
				if err != nil {
					return err
				}
				if len(urls) > 0 || time.Now().After(deadline) {
					return printDev(c.OutOrStdout(), active, urls)
				}
				time.Sleep(2 * time.Second)
			}
		},
	}
	start.Flags().DurationVar(&wait, "wait", 20*time.Second, "how long to wait for an address to appear")
	stop := &cobra.Command{
		Use: "stop", Args: cobra.NoArgs, Short: "Stop this session's dev processes",
		RunE: func(*cobra.Command, []string) error {
			if os.Getenv("LOLA_SANDBOX") != "" {
				return errors.New("a sandboxed session cannot stop dev servers; ask the human")
			}
			return devToggle(false)
		},
	}
	status := &cobra.Command{
		Use: "status", Args: cobra.NoArgs, Short: "Print whether the dev server runs and its addresses",
		RunE: func(c *cobra.Command, _ []string) error {
			urls, active, err := devStatus()
			if err != nil {
				return err
			}
			return printDev(c.OutOrStdout(), active, urls)
		},
	}
	root.AddCommand(start, stop, status, devLoginCmd())
	return root
}

// devAsk is `dev start` in a sandbox: the bridge admits no dev command (dev tabs
// run on the host, in a tree the agent can write), so it records a REQUEST the
// human answers with one click in the Dev panel.
func devAsk(w io.Writer, why string) error {
	resp, err := hook.SendAgent("agentReport", protocol.ReportArgs{Argv: append([]string{"devstart"}, why)})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	_, err = fmt.Fprintln(w, "asked the human to start the dev server (a sandboxed session cannot start it itself). Carry on; once they start it, its address is theirs to open - name it in the review manifest's pages.")
	return err
}

func devToggle(on bool) error {
	resp, err := hook.SendAgent("dev", protocol.DevArgs{Session: os.Getenv("LOLA_SESSION"), On: on})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

func devStatus() (urls []string, active bool, err error) {
	if os.Getenv("LOLA_SANDBOX") != "" {
		return nil, false, errors.New("a sandboxed session cannot read or run dev servers; `lola dev start` asks the human to start it")
	}
	s, err := currentDevSession()
	if err != nil {
		return nil, false, err
	}
	urls = append(urls, s.DevURLs...)
	for _, a := range s.Apps {
		urls = append(urls, a.DevURLs...)
	}
	return urls, s.DevActive, nil
}

func currentDevSession() (protocol.SessionInfo, error) {
	resp, err := hook.SendAgent("sessions", nil)
	if err != nil {
		return protocol.SessionInfo{}, err
	}
	if !resp.OK {
		return protocol.SessionInfo{}, errors.New(resp.Error)
	}
	var data protocol.SessionsData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return protocol.SessionInfo{}, err
	}
	me := os.Getenv("LOLA_SESSION")
	for _, s := range data.Sessions {
		if s.ID == me {
			return s, nil
		}
	}
	return protocol.SessionInfo{}, fmt.Errorf("unknown session %s", me)
}

func printDev(w io.Writer, active bool, urls []string) error {
	if !active {
		_, err := fmt.Fprintln(w, "dev server: not running")
		return err
	}
	if len(urls) == 0 {
		_, err := fmt.Fprintln(w, "dev server: running, no address seen yet")
		return err
	}
	fmt.Fprintln(w, "dev server: running")
	for _, u := range urls {
		fmt.Fprintln(w, u)
	}
	return nil
}
