package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sushidev-team/lola/hub/internal/admin"
	"github.com/sushidev-team/lola/hub/internal/console"
	"github.com/sushidev-team/lola/hub/internal/escalate"
)

// hubInit sets up a hub host in one step: the org (with its Linear team)
// and the admin console's token. A Linear team KEY ("ENG") is resolved to
// the team's id through the API when LOLA_HUB_LINEAR_KEY is set; otherwise
// the value is stored as given. With LOLA_HUB_SECRET_KEY set as well, that
// Linear key becomes the new org's own key, stored sealed.
func (c *cli) hubInit(args []string) error {
	fs := c.newFlags("init")
	data := c.dataFlag(fs)
	org := fs.String("org", "", "org to create (required)")
	team := fs.String("linear-team", "", "Linear team KEY (e.g. ENG) or id for this org's escalations")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	if *org == "" {
		return usagef("--org is required")
	}
	teamID := *team
	// Read once and dropped from the environment, as serve does; the key is
	// never printed and never in an error.
	key := c.getenv(escalate.EnvKey)
	_ = os.Unsetenv(escalate.EnvKey)
	box, err := c.box()
	if err != nil {
		return err
	}
	if teamID != "" && escalate.IsTeamKey(teamID) {
		if key != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			id, err := (&escalate.Linear{Key: key, Endpoint: c.linearEndpoint}).TeamID(ctx, teamID)
			cancel()
			if err != nil {
				return fmt.Errorf("resolve Linear team %s: %v", teamID, err)
			}
			fmt.Fprintf(c.stderr, "Linear team %s is %s\n", teamID, id)
			teamID = id
		} else {
			fmt.Fprintf(c.stderr, "note: %s looks like a team key; without %s it is stored as given (set the team id with admin org linear)\n", teamID, escalate.EnvKey)
		}
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	if err := admin.InitOrg(st, *org, teamID, time.Now()); err != nil {
		if errors.Is(err, admin.ErrExists) {
			return fmt.Errorf("%v; init never changes an existing org (use admin org linear)", err)
		}
		return err
	}
	keyStored := false
	if key != "" && box != nil {
		if err := admin.SetLinearKey(st, box, *org, key, time.Now()); err != nil {
			return err
		}
		keyStored = true
	}
	dir := filepath.Dir(*data)
	tok, created, err := console.LoadOrCreateToken(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "created org %s", *org)
	if teamID != "" {
		fmt.Fprintf(c.stdout, " (escalations go to Linear team %s)", teamID)
	}
	fmt.Fprintln(c.stdout)
	if keyStored {
		fmt.Fprintf(c.stdout, "stored the Linear key for org %s (sealed)\n", *org)
	}
	path := filepath.Join(dir, console.TokenFile)
	if created {
		// Printed here, once, to the admin's own terminal; serve never
		// prints it because its stderr is the server log.
		fmt.Fprintf(c.stdout, "admin token: %s\n(stored in %s, 0600)\n", tok, path)
	} else {
		fmt.Fprintf(c.stdout, "admin token: unchanged, in %s\n", path)
	}
	fmt.Fprintf(c.stderr, "next: lola-hub policy init --org %s --out <dir> (on your machine), then lola-hub invite ...\n", *org)
	return nil
}

// DefaultInviteTTL is how long an invite's token lives: long enough for a
// teammate to get to it, short enough not to linger in a chat log.
const DefaultInviteTTL = 7 * 24 * time.Hour

// invite mints labelled enroll tokens and prints one invite string per
// token. The trusted policy key comes from the ADMIN's flag, never from the
// store: a compromised hub must not choose the key a new member pins.
func (c *cli) invite(args []string) error {
	fs := c.newFlags("invite")
	data := c.dataFlag(fs)
	hubURL := fs.String("url", "", "the hub URL members join (required)")
	org := fs.String("org", "", "org (required)")
	trusted := fs.String("trusted", "", "policy key <key_id>:<public key> members pin (from policy init)")
	label := fs.String("label", "", "member name (required)")
	count := fs.Int("count", 1, "how many invites (one per device)")
	ttl := fs.Duration("ttl", DefaultInviteTTL, "invite lifetime")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	if *hubURL == "" || *org == "" || *label == "" {
		return usagef("--url, --org and --label are required")
	}
	u, err := admin.CheckHubURL(*hubURL)
	if err != nil {
		return err
	}
	key := ""
	if *trusted != "" {
		if key, err = admin.CheckTrusted(*trusted); err != nil {
			return err
		}
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	toks, exp, err := admin.CreateEnrollTokens(st, *org, *label, *ttl, *count, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(c.stderr, "%d invite(s) for %s in org %s, single use, expire %s.\nShown ONCE (stored only as hashes); send each to its person privately:\n", len(toks), *label, *org, exp.Format(time.RFC3339))
	for _, t := range toks {
		fmt.Fprintln(c.stdout, admin.FormatInvite(u, t, key))
	}
	if key == "" {
		fmt.Fprintln(c.stderr, "note: no --trusted key; members must pin the team's policy key themselves.")
	}
	return nil
}
