package main

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sushidev-team/lola/internal/secrets"
)

// Seams over the keychain, so tests never touch the operator's real one.
var (
	sandboxStoreSecret  = secrets.Store
	sandboxDeleteSecret = secrets.Delete
)

// sandboxTokenRe picks the token out of `claude setup-token`'s output, which
// wraps it in prose. Anthropic's tokens all start sk-ant-.
var sandboxTokenRe = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]+`)

func sandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Container sandbox helpers ([[project]].sandbox = \"docker\")",
	}
	var clear bool
	token := &cobra.Command{
		Use:   "token",
		Short: "Store the Claude Code token sandboxed agents log in with",
		Long: `Stores the Claude Code token a sandboxed claude authenticates with, in the
macOS keychain (service "` + secrets.SandboxClaudeService + `"). A container cannot
read the keychain the host claude logs in through, so lola hands this token to
each sandboxed session in its private 0600 env file.

Create a long-lived token with ` + "`claude setup-token`" + `, then paste it here (the input
is not echoed), or pipe it in on stdin. It is never accepted as an argument:
argv is visible in ps and kept in shell history.

CLAUDE_CODE_OAUTH_TOKEN in the daemon's environment takes precedence over the
stored token. ANTHROPIC_API_KEY is passed only with [sandbox].use_api_key = true
(it bills per token instead of your Claude plan). --clear deletes it.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if clear {
				if err := sandboxDeleteSecret(secrets.SandboxClaudeService); err != nil {
					return err
				}
				fmt.Fprintln(c.OutOrStdout(), "Sandbox Claude token removed.")
				return nil
			}
			tok, err := readSandboxToken(c.InOrStdin(), c.ErrOrStderr(), stdinTerminal)
			if err != nil {
				return err
			}
			if err := sandboxStoreSecret(secrets.SandboxClaudeService, tok); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "Sandbox Claude token stored. New sandboxed sessions use it.")
			return nil
		},
	}
	token.Flags().BoolVar(&clear, "clear", false, "delete the stored token")
	cmd.AddCommand(token)
	return cmd
}

// readSandboxToken reads the token from stdin, without echo on a terminal.
// The token itself never appears in an error.
func readSandboxToken(stdin io.Reader, stderr io.Writer, tty func() (uintptr, bool)) (string, error) {
	var raw []byte
	var err error
	if fd, ok := tty(); ok {
		fmt.Fprint(stderr, "Claude Code token (from `claude setup-token`): ")
		raw, err = term.ReadPassword(fd)
		fmt.Fprintln(stderr)
	} else {
		raw, err = io.ReadAll(io.LimitReader(stdin, 64<<10))
	}
	if err != nil {
		return "", fmt.Errorf("read the token: %w", err)
	}
	if m := sandboxTokenRe.FindAll(raw, -1); len(m) == 1 {
		return string(m[0]), nil
	} else if len(m) > 1 {
		return "", errors.New("the input holds more than one token; give exactly one")
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" {
		return "", errors.New("no token: paste it, or pipe it on stdin")
	}
	if strings.ContainsAny(tok, " \t\r\n") {
		return "", errors.New("the token must be a single line with no spaces")
	}
	return tok, nil
}
