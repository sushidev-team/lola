// Command lola-hub is the team coordinator for lola daemons.
//
//	lola-hub serve   [flags]                  run the HTTP API (+ loopback admin console)
//	lola-hub dev     [flags]                  a local hub for testing, in one command
//	lola-hub init    --org NAME [flags]       org + admin token in one step (host)
//	lola-hub invite  --url U --org O ...      labelled invites, printed once (host)
//	lola-hub secret-key                       a fresh LOLA_HUB_SECRET_KEY
//	lola-hub admin   <noun> <verb> [flags]    operate on the store file (host only)
//	lola-hub policy  init|keygen|sign [flags] offline policy signing (admin laptop)
//
// Run "lola-hub help" for the full list.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sushidev-team/lola/hub/internal/secret"
)

const usage = `lola-hub — team coordinator for lola daemons

Server:
  lola-hub serve [--addr :8080] [--data PATH] [--tls-cert FILE --tls-key FILE]
                 [--admin-addr 127.0.0.1:8098] [--linear-team ID]
                 [--lease-ttl 30m] [--escalate-after 2]
                 [--max-lease-age 24h] [--stale-after 48h]
                 [--open-teams [--max-teams 50]]
      --admin-addr starts the admin web console. LOOPBACK ONLY: any other
      address is refused. Its token is <data dir>/admin.token (0600).
      --open-teams lets anyone who can reach the hub create a team WITHOUT
      a create code, up to --max-teams. Off by default: use it only on a
      hub strangers cannot reach.
  lola-hub secret-key
      prints a fresh LOLA_HUB_SECRET_KEY (the key that seals each org's
      Linear key and digest webhook in the store).

Setup (on the hub host):
  lola-hub init --org NAME [--linear-team KEY_OR_ID]
      creates the org and the console's admin token. A team KEY (ENG) is
      resolved to its id when LOLA_HUB_LINEAR_KEY is set.
  lola-hub invite --url HUBURL --org NAME --label "Name" [--trusted KEYID:PUB]
                  [--count 1] [--ttl 168h]
      prints one invite per device, once:
      lola-hub:<url>#<token>[#<key_id>:<pub>]
      The trusted key is taken ONLY from --trusted, never from the hub.

Local testing (org + enroll token + API + console in one command):
  lola-hub dev [--data ./data-dev] [--addr 127.0.0.1:8099]
               [--admin-addr 127.0.0.1:8098] [--org dev]
               [--seed-demo [--demo-repo acme/demo]]

Admin (operates on the store file directly, from the host):
  lola-hub admin org create <name>
  lola-hub admin org list
  lola-hub admin org linear <org> --team ID | --clear
  lola-hub admin org linear-key <org> [--clear]   the key is read from stdin
  lola-hub admin org digest <org> [--clear]       the Slack webhook, from stdin
  lola-hub admin create-code [--url HUBURL] [--ttl 168h]
      a one-time code that lets a teammate create ONE team from Lola:
      lola-hub-create:<url>#<code>
  lola-hub admin enroll create --org ORG [--label "Name"] [--ttl 24h]
  lola-hub admin enroll list [--org ORG]
  lola-hub admin enroll revoke <token-id>
  lola-hub admin devices list [--org ORG]
  lola-hub admin devices revoke <device-id>
  lola-hub admin items list [--org ORG] [--state open|claimed|in_review|done|failed|escalated]
  lola-hub admin items resolve <item-id>     escalated -> done (a person fixed it)
  lola-hub admin items reopen <item-id>      escalated -> open, attempts reset
  lola-hub admin items purge <item-id> | --org ORG --state STATE
  lola-hub admin items add --org ORG --kind dep_advisory --repo O/R --ecosystem E
                 --package P --vulnerable RANGE [--fixed V] --advisory ID --severity S
  lola-hub admin items add --org ORG --kind red_bump_pr --repo O/R --pr N
                 --branch B --package P [--author dependabot[bot]|renovate[bot]]
  lola-hub admin events list [--org ORG] [--limit 50]
  lola-hub admin policy keys add --org ORG --key-id ID --public-key B64
  lola-hub admin policy keys remove --org ORG --key-id ID
  lola-hub admin policy publish [--register-key KEYID:PUB] <signed-policy.json>

Policy signing (offline, on an admin's machine; the hub never holds these keys):
  lola-hub policy init --org NAME --out DIR
  lola-hub policy keygen --out FILE
  lola-hub policy sign --key FILE [--key-id ID] [--out FILE] <unsigned-policy.json>

Every admin command takes --data PATH (default $LOLA_HUB_DATA or data/hub.json).
Environment: LOLA_HUB_SECRET_KEY (32 random bytes, base64; "lola-hub
             secret-key" prints one). It seals each org's Linear key and
             digest webhook in the store. Read once, removed from the
             environment, never logged, never stored. Without it the hub
             stores no secret and escalations stay pending.
             LOLA_HUB_LINEAR_TEAM (--linear-team wins): a team id for a
             single-org hub whose org set none.
             LOLA_HUB_LINEAR_KEY and LOLA_HUB_DIGEST_WEBHOOK are no longer
             read at runtime. A single-org hub that still has them adopts
             them into its org at the next start (sealed); remove them after.
`

// errUsage marks a usage error (exit 2).
var errUsage = errors.New("usage")

func usagef(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errUsage}, args...)...)
}

// cli carries the process environment so tests can drive commands.
type cli struct {
	stdout, stderr io.Writer
	// stdin is where a secret is read from (nil = os.Stdin): never argv.
	stdin  io.Reader
	getenv func(string) string
	// ctx, when set, replaces the signal context of serve/dev (tests).
	ctx context.Context
	// onReady, when set, is told a dev hub is listening (tests).
	onReady func(devInfo)
	// linearEndpoint, when set, replaces Linear's GraphQL endpoint (tests).
	linearEndpoint string
}

func main() {
	c := &cli{stdout: os.Stdout, stderr: os.Stderr, getenv: os.Getenv}
	os.Exit(c.run(os.Args[1:]))
}

func (c *cli) run(args []string) int {
	err := c.dispatch(args)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		fmt.Fprintf(c.stderr, "lola-hub: %v\n\n%s", err, usage)
		return 2
	default:
		fmt.Fprintf(c.stderr, "lola-hub: %v\n", err)
		return 1
	}
}

func (c *cli) dispatch(args []string) error {
	if len(args) == 0 {
		return usagef("missing command")
	}
	if isHelp(args[0]) || (args[0] != "serve" && args[0] != "dev" && wantsGroupHelp(args[1:])) {
		fmt.Fprint(c.stdout, usage)
		return nil
	}
	switch args[0] {
	case "serve":
		return c.serve(args[1:])
	case "dev":
		return c.dev(args[1:])
	case "init":
		return c.hubInit(args[1:])
	case "invite":
		return c.invite(args[1:])
	case "admin":
		return c.admin(args[1:])
	case "policy":
		return c.policy(args[1:])
	case "secret-key":
		if err := want(args[1:], 0, ""); err != nil {
			return err
		}
		k, err := secret.NewKey()
		if err != nil {
			return err
		}
		fmt.Fprintln(c.stdout, k)
		fmt.Fprintf(c.stderr, "set it as %s in the hub's environment (a secret manager, never a flag or a file in the repo).\nKeep a copy: without it the stored Linear keys and webhooks cannot be opened and must be entered again.\n", secret.EnvKey)
		return nil
	}
	return usagef("unknown command %q", args[0])
}

func isHelp(s string) bool { return s == "help" || s == "-h" || s == "--help" }

// wantsGroupHelp reports a help request where an admin or policy group expects
// its noun or verb ("admin --help", "admin org -h", "policy help"). A help flag
// after a full verb is the verb's own flag set's business.
func wantsGroupHelp(rest []string) bool {
	for i, a := range rest {
		if i >= 2 {
			return false
		}
		if isHelp(a) {
			return true
		}
	}
	return false
}

// newFlags builds a FlagSet that reports through the cli.
func (c *cli) newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	return fs
}

// parse parses flags that may appear before, between or after positional
// arguments, and returns the positional ones.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, usagef("%v", err)
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// dataFlag registers --data with its default.
func (c *cli) dataFlag(fs *flag.FlagSet) *string {
	def := c.getenv("LOLA_HUB_DATA")
	if def == "" {
		def = "data/hub.json"
	}
	return fs.String("data", def, "store snapshot path")
}

func want(pos []string, n int, what string) error {
	if len(pos) != n {
		if n == 0 {
			return usagef("unexpected argument %q", strings.Join(pos, " "))
		}
		return usagef("expected %s", what)
	}
	return nil
}
