package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/secrets"
	"github.com/sushidev-team/lola/internal/tui"
)

// planCmd is the plan-approval gate's CLI ([[project]].require_plan):
//
//	lola plan submit [file]              the AGENT hands in its plan (stdin by default)
//	lola plan approve <session>          a human approves (or waives the gate)
//	lola plan reject <session> <comment> a human sends it back with feedback
func planCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "plan",
		Short: "Plan-approval gate: submit a plan (agent), approve or reject it (human)",
	}
	root.AddCommand(
		&cobra.Command{
			Use:   "submit [file]",
			Short: "Submit this session's plan for human approval (reads stdin without a file)",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(c *cobra.Command, a []string) error {
				var r io.Reader = c.InOrStdin()
				if len(a) == 1 && a[0] != "-" {
					f, err := os.Open(a[0])
					if err != nil {
						return err
					}
					defer f.Close()
					r = f
				}
				plan, err := io.ReadAll(io.LimitReader(r, 1<<20))
				if err != nil {
					return err
				}
				if err := hook.SubmitPlan(string(plan)); err != nil {
					if errors.Is(err, hook.ErrNoDaemon) {
						return fmt.Errorf("plan not delivered: %w — retry in a moment", err)
					}
					return err
				}
				fmt.Fprintln(c.OutOrStdout(), "Plan submitted. End your turn now and wait for the approval message.")
				return nil
			},
		},
		&cobra.Command{
			Use:   "approve <session> [comment...]",
			Short: "Approve a session's submitted plan (or waive the gate before one arrives)",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(c *cobra.Command, a []string) error {
				return sendPlanDecide(a[0], true, strings.Join(a[1:], " "))
			},
		},
		&cobra.Command{
			Use:   "reject <session> <comment...>",
			Short: "Send a session's plan back with feedback; the agent re-plans",
			Args:  cobra.MinimumNArgs(2),
			RunE: func(c *cobra.Command, a []string) error {
				return sendPlanDecide(a[0], false, strings.Join(a[1:], " "))
			},
		},
	)
	return root
}

func sendPlanDecide(session string, approve bool, comment string) error {
	args, err := json.Marshal(protocol.PlanDecideArgs{Approve: approve, Comment: comment})
	if err != nil {
		return err
	}
	raw, err := json.Marshal(protocol.Request{Cmd: "planDecide", Session: session, Args: args})
	if err != nil {
		return err
	}
	return tui.Send(string(raw))
}

// linearAgentCmd manages the native Linear agent's credentials. Every secret
// it handles goes to the keychain via stdin (never argv) and is never printed.
func linearAgentCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "linear-agent",
		Short: "Native Linear agent: install lola as a Linear agent (OAuth) and manage its secrets",
	}
	root.AddCommand(
		&cobra.Command{
			Use:   "login",
			Short: "Install lola as an agent in your Linear workspace (opens the browser)",
			Args:  cobra.NoArgs,
			RunE:  func(c *cobra.Command, _ []string) error { return linearAgentLogin(c) },
		},
		&cobra.Command{
			Use:   "set-secret <client|webhook>",
			Short: "Store the OAuth client secret or the webhook signing secret in the keychain (reads stdin)",
			Args:  cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, a []string) error {
				cfg, err := loadConfig()
				if err != nil {
					return err
				}
				service := ""
				switch a[0] {
				case "client":
					service = orDefault(cfg.LinearAgent.ClientSecretKeychain, config.DefaultLinearAgentSecretKeychain)
				case "webhook":
					service = orDefault(cfg.LinearAgent.WebhookSecretKeychain, config.DefaultLinearAgentWebhookKeychain)
				default:
					return fmt.Errorf("unknown secret %q: want client or webhook", a[0])
				}
				fmt.Fprintf(c.ErrOrStderr(), "Paste the %s secret and press Enter: ", a[0])
				line, err := readSecretLine(c.InOrStdin())
				if err != nil {
					return err
				}
				if err := secrets.Store(service, line); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "\nStored in the keychain under %q.\n", service)
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show the Linear agent loop's status",
			Args:  cobra.NoArgs,
			RunE:  func(c *cobra.Command, _ []string) error { return tui.Send(`{"cmd":"status"}`) },
		},
	)
	return root
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

func loadConfig() (*config.Config, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return nil, err
	}
	return config.Load(path)
}

// readSecretLine reads one line without echoing it when stdin is a terminal.
func readSecretLine(r io.Reader) (string, error) {
	if f, ok := r.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			// stty acts on ITS stdin, so it must be handed the terminal itself —
			// an unset Stdin is /dev/null and the call fails. A terminal whose
			// echo cannot be turned off is refused: the secret would be printed.
			if err := stty(f, "-echo"); err != nil {
				return "", errors.New("cannot hide terminal input; pipe the secret instead: `pbpaste | lola linear-agent set-secret <kind>`")
			}
			defer func() { _ = stty(f, "echo") }()
		}
	}
	b, err := io.ReadAll(io.LimitReader(lineReader{r}, 8<<10))
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", errors.New("empty secret")
	}
	return s, nil
}

func stty(tty *os.File, arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = tty
	return cmd.Run()
}

// lineReader stops at the first newline so an interactive paste returns.
type lineReader struct{ r io.Reader }

func (l lineReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n, err := l.r.Read(p[:1])
	if n == 1 && p[0] == '\n' {
		return 0, io.EOF
	}
	return n, err
}

// linearAgentLogin runs the OAuth authorization-code flow with actor=app:
// a loopback callback, a CSRF state nonce, the code exchanged with the client
// secret from the keychain, and the tokens written to the keychain.
func linearAgentLogin(c *cobra.Command) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	la := cfg.LinearAgent
	if la.ClientID == "" {
		return errors.New("[linear_agent].client_id is not set — create an OAuth app in Linear (Settings → API → OAuth applications), enable it for agents, and put its client id in config.toml")
	}
	secretService := orDefault(la.ClientSecretKeychain, config.DefaultLinearAgentSecretKeychain)
	secret, err := secrets.Resolve("linear agent client secret", secretService, la.ClientSecretEnv)
	if err != nil {
		return fmt.Errorf("%w — store it with `lola linear-agent set-secret client`", err)
	}
	redirect := la.RedirectURI()
	port := la.RedirectPort
	if port == 0 {
		port = config.DefaultLinearAgentRedirectPort
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	state := hex.EncodeToString(nonce)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("listen for the OAuth callback on port %d: %w", port, err)
	}
	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	// Never block a handler on the result: only the first outcome is read, and a
	// browser retry or reload must not stall shutdown waiting on a full channel.
	finish := func(r result) {
		select {
		case done <- r:
		default:
		}
	}
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			switch {
			case q.Get("state") != state:
				// Not ours (any local page can hit this port): refuse it without
				// ending the login a real callback may still complete.
				http.Error(w, "state mismatch", http.StatusBadRequest)
			case q.Get("error") != "":
				http.Error(w, "authorization was not granted", http.StatusBadRequest)
				finish(result{err: errors.New("authorization was not granted")})
			case q.Get("code") == "":
				http.Error(w, "no authorization code", http.StatusBadRequest)
				finish(result{err: errors.New("no authorization code in callback")})
			default:
				fmt.Fprintln(w, "lola is installed as a Linear agent. You can close this tab.")
				finish(result{code: q.Get("code")})
			}
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	authURL := linear.AgentAuthorizeURL(la.ClientID, redirect, state)
	fmt.Fprintf(c.OutOrStdout(), "Open this URL to install lola as a Linear agent (an admin must approve app installs):\n\n  %s\n\nWaiting for the callback on %s …\n", authURL, redirect)
	if runtime.GOOS == "darwin" {
		_ = exec.Command("open", authURL).Start()
	}

	var res result
	select {
	case res = <-done:
	case <-time.After(5 * time.Minute):
		return errors.New("timed out waiting for the OAuth callback")
	}
	if res.err != nil {
		return res.err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tok, err := linear.OAuthClient{ClientID: la.ClientID, ClientSecret: secret}.Exchange(ctx, res.code, redirect)
	if err != nil {
		return err
	}
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	tokenService := orDefault(la.TokenKeychain, config.DefaultLinearAgentTokenKeychain)
	if err := secrets.Store(tokenService, string(b)); err != nil {
		return fmt.Errorf("store the agent token: %w", err)
	}
	if u, err := linear.NewBearer(cfg.Linear.Endpoint, tok.AccessToken).AgentViewer(ctx); err == nil {
		fmt.Fprintf(c.OutOrStdout(), "\nInstalled: lola acts in Linear as %q.\n", u.Name)
	}
	fmt.Fprintln(c.OutOrStdout(), "Token stored in the keychain. Set [linear_agent].enabled = true and run `lola reload`.")
	return nil
}
