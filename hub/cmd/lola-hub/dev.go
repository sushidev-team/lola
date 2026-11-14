package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hub/internal/admin"
	"github.com/sushidev-team/lola/hub/internal/console"
	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hubproto"
)

// devInfo is what "lola-hub dev" prints once it is listening.
type devInfo struct {
	hubReady
	Org         string
	EnrollToken string
	Expires     time.Time
	Seeded      []string // item ids added by --seed-demo
	CreateCode  string
}

// dev runs a local hub for testing in one command: it ensures the org, mints
// a fresh enroll token, optionally seeds demo chores, and serves the API plus
// the admin console, both on loopback by default.
func (c *cli) dev(args []string) error {
	fs := c.newFlags("dev")
	data := fs.String("data", "./data-dev", "data DIRECTORY (the store is <dir>/hub.json)")
	addr := fs.String("addr", "127.0.0.1:8099", "API listen address")
	adminAddr := fs.String("admin-addr", "127.0.0.1:8098", "admin console address (loopback only)")
	org := fs.String("org", "dev", "org to ensure")
	seed := fs.Bool("seed-demo", false, "add 3 example chores")
	demoRepo := fs.String("demo-repo", "acme/demo", "repo (owner/name) the demo chores name")
	ttl := fs.Duration("token-ttl", admin.DefaultTokenTTL, "enroll token lifetime")
	openTeams := fs.Bool("open-teams", false, "let a team be created without a create code, as serve --open-teams does")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	if *adminAddr == "" {
		return usagef("--admin-addr is required for dev")
	}
	if err := console.CheckLoopback(*adminAddr); err != nil {
		return err
	}
	if *seed && !validRepo(*demoRepo) {
		return usagef("--demo-repo must be owner/name")
	}
	path := filepath.Join(*data, "hub.json")
	st, err := openStore(path)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := admin.CreateOrg(st, *org, now); err != nil && !errors.Is(err, admin.ErrExists) {
		return err
	}
	// Tokens are stored hashed, so an existing one cannot be printed again:
	// every dev start mints a fresh one.
	tok, exp, err := admin.CreateEnrollToken(st, *org, "", *ttl, now)
	if err != nil {
		return err
	}
	// A create code too, so "Create a team" can be tried from the app.
	code, _, err := admin.CreateCreateCode(st, *ttl, now)
	if err != nil {
		return err
	}
	// dev only: the secret key lives next to the store so a local hub can
	// hold a Linear key without any setup. serve takes it from the
	// environment and nowhere else.
	sk := c.getenv(secret.EnvKey)
	if sk == "" {
		if sk, err = devSecretKey(*data); err != nil {
			return err
		}
	}
	info := devInfo{Org: *org, EnrollToken: tok, Expires: exp, CreateCode: code}
	if *seed {
		for _, it := range demoItems(*demoRepo) {
			id, _, err := admin.AddItem(st, *org, it, now)
			if err != nil {
				return fmt.Errorf("seed: %w", err)
			}
			info.Seeded = append(info.Seeded, id)
		}
	}
	o := hubOpts{
		addr: *addr, adminAddr: *adminAddr, data: path,
		team: c.getenv("LOLA_HUB_LINEAR_TEAM"), secretKey: sk, openTeams: *openTeams,
		ttl: lease.DefaultTTL, escAfter: lease.DefaultEscalateAfter, sweep: time.Minute,
	}
	ctx, stop := c.context()
	defer stop()
	return c.runHub(ctx, o, func(r hubReady) {
		info.hubReady = r
		c.printDev(info)
		if c.onReady != nil {
			c.onReady(info)
		}
	})
}

// devSecretKey loads or creates <dir>/secret.key (0600).
func devSecretKey(dir string) (string, error) {
	path := filepath.Join(dir, "secret.key")
	if b, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	k, err := secret.NewKey()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(k+"\n"), 0o600); err != nil {
		return "", err
	}
	return k, nil
}

func validRepo(s string) bool {
	return hubproto.Validate(hubproto.Item{Kind: hubproto.KindRedBumpPR, RedBumpPR: &hubproto.RedBumpPR{
		Repo: s, PR: 1, Branch: "dependabot/x", Package: "x", Author: hubproto.AuthorDependabot,
	}}) == nil
}

// demoItems are three valid example chores in repo: two advisories and one
// red dependency-bump PR.
func demoItems(repo string) []hubproto.Item {
	return []hubproto.Item{
		{Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
			Repo: repo, Ecosystem: "npm", Package: "lodash", Vulnerable: "< 4.17.21", Fixed: "4.17.21",
			Advisory: "GHSA-35jh-r3h4-6jhm", Severity: hubproto.SeverityHigh,
		}},
		{Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
			Repo: repo, Ecosystem: "npm", Package: "minimist", Vulnerable: "< 1.2.6", Fixed: "1.2.6",
			Advisory: "GHSA-xvch-5gv4-984h", Severity: hubproto.SeverityModerate,
		}},
		{Kind: hubproto.KindRedBumpPR, RedBumpPR: &hubproto.RedBumpPR{
			Repo: repo, PR: 42, Branch: "dependabot/npm_and_yarn/axios-1.7.4", Package: "axios", Author: hubproto.AuthorDependabot,
		}},
	}
}

func (c *cli) printDev(i devInfo) {
	var b strings.Builder
	fmt.Fprintf(&b, "\nlola-hub dev is running (Ctrl-C to stop)\n\n")
	fmt.Fprintf(&b, "  Hub URL        %s\n", i.APIURL)
	fmt.Fprintf(&b, "  Org            %s\n", i.Org)
	fmt.Fprintf(&b, "  Enroll token   %s   (single use, expires %s)\n", i.EnrollToken, i.Expires.Local().Format("Jan 2 15:04"))
	fmt.Fprintf(&b, "  Invite         %s\n", hubproto.FormatInvite(i.APIURL, i.EnrollToken, ""))
	fmt.Fprintf(&b, "  Create a team  %s\n", hubproto.FormatCreateCode(i.APIURL, i.CreateCode))
	fmt.Fprintf(&b, "  Admin console  %s\n", i.ConsoleURL)
	fmt.Fprintf(&b, "  Admin token    %s\n", i.AdminToken)
	if len(i.Seeded) > 0 {
		fmt.Fprintf(&b, "  Demo chores    %d in the org (repo must be opted in on a daemon to be offered)\n", len(i.Seeded))
	}
	fmt.Fprintf(&b, "\nJoin from Lola: Settings → Team hub → paste the Hub URL and the enroll token.\n")
	fmt.Fprintf(&b, "Or from a terminal:\n  echo %s | lola hub join %s\n\n", i.EnrollToken, i.APIURL)
	fmt.Fprint(c.stdout, b.String())
}
