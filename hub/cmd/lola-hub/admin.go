package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sushidev-team/lola/hub/internal/admin"
	"github.com/sushidev-team/lola/hub/internal/escalate"
	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

func (c *cli) admin(args []string) error {
	if len(args) > 0 && args[0] == "create-code" {
		return c.createCode(args[1:])
	}
	if len(args) < 2 {
		return usagef("admin needs <noun> <verb>")
	}
	noun, verb, rest := args[0], args[1], args[2:]
	switch noun + " " + verb {
	case "org create":
		return c.orgCreate(rest)
	case "org list":
		return c.orgList(rest)
	case "org linear":
		return c.orgLinear(rest)
	case "org linear-key":
		return c.orgSecret(rest, "linear-key")
	case "org digest":
		return c.orgSecret(rest, "digest")
	case "enroll create":
		return c.enrollCreate(rest)
	case "enroll list":
		return c.enrollList(rest)
	case "enroll revoke":
		return c.enrollRevoke(rest)
	case "devices list":
		return c.devicesList(rest)
	case "devices revoke":
		return c.devicesRevoke(rest)
	case "items list":
		return c.itemsList(rest)
	case "items purge":
		return c.itemsPurge(rest)
	case "items add":
		return c.itemsAdd(rest)
	case "items resolve":
		return c.itemsMove(rest, "resolve")
	case "items reopen":
		return c.itemsMove(rest, "reopen")
	case "events list":
		return c.eventsList(rest)
	case "policy publish":
		return c.policyPublish(rest)
	case "policy keys":
		if len(rest) > 0 && rest[0] == "add" {
			return c.policyKeysAdd(rest[1:])
		}
		if len(rest) > 0 && rest[0] == "remove" {
			return c.policyKeysRemove(rest[1:])
		}
		return usagef("admin policy keys add|remove ...")
	}
	return usagef("unknown admin command %q", noun+" "+verb)
}

// box builds the secret box from the environment, as serve does.
func (c *cli) box() (*secret.Box, error) {
	sk := c.getenv(secret.EnvKey)
	_ = os.Unsetenv(secret.EnvKey)
	return secret.New(sk)
}

// readSecret reads one line from stdin: a secret is never a flag, so it
// never sits in argv or a shell history.
func (c *cli) readSecret(what string) (string, error) {
	in := c.stdin
	if in == nil {
		in = os.Stdin
	}
	b, err := io.ReadAll(io.LimitReader(in, 4<<10))
	if err != nil {
		return "", fmt.Errorf("read the %s from stdin: %v", what, err)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", usagef("pipe the %s on stdin (it is never a flag), or use --clear", what)
	}
	return s, nil
}

// orgSecret sets or clears an org's Linear key or digest webhook. The value
// comes from stdin and is stored sealed with LOLA_HUB_SECRET_KEY.
func (c *cli) orgSecret(args []string, which string) error {
	fs := c.newFlags("admin org " + which)
	data := c.dataFlag(fs)
	clear := fs.Bool("clear", false, "remove it")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 1, "<org>"); err != nil {
		return err
	}
	what, set := "Linear API key", admin.SetLinearKey
	if which == "digest" {
		what, set = "Slack incoming webhook", admin.SetDigestWebhook
	}
	value := ""
	if !*clear {
		if value, err = c.readSecret(what); err != nil {
			return err
		}
	}
	box, err := c.box()
	if err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	if err := set(st, box, pos[0], value, time.Now()); err != nil {
		return err
	}
	if *clear {
		fmt.Fprintf(c.stdout, "org %s: %s cleared\n", pos[0], what)
	} else {
		fmt.Fprintf(c.stdout, "org %s: %s stored (sealed)\n", pos[0], what)
	}
	return nil
}

// createCode mints a one-time code that lets its holder create one org
// through the API, and prints it once.
func (c *cli) createCode(args []string) error {
	fs := c.newFlags("admin create-code")
	data := c.dataFlag(fs)
	hubURL := fs.String("url", "", "the hub URL, to print one pasteable line")
	ttl := fs.Duration("ttl", admin.DefaultCreateCodeTTL, "code lifetime")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	u := ""
	if *hubURL != "" {
		if u, err = admin.CheckHubURL(*hubURL); err != nil {
			return err
		}
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	code, exp, err := admin.CreateCreateCode(st, *ttl, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(c.stderr, "create code, single use, expires %s. Its holder can create ONE team on this hub.\nShown ONCE (stored only as a hash); send it privately:\n", exp.Format(time.RFC3339))
	if u != "" {
		fmt.Fprintln(c.stdout, hubproto.FormatCreateCode(u, code))
	} else {
		fmt.Fprintln(c.stdout, code)
	}
	return nil
}

func (c *cli) policyKeysRemove(args []string) error {
	fs := c.newFlags("admin policy keys remove")
	data := c.dataFlag(fs)
	org := fs.String("org", "", "org (required)")
	keyID := fs.String("key-id", "", "policy key id (required)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	if *org == "" || *keyID == "" {
		return usagef("--org and --key-id are required")
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	if err := admin.RemovePolicyKey(st, *org, *keyID, time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "removed policy key %s from org %s: it can no longer publish a policy or change the org's settings\n", *keyID, *org)
	return nil
}

func openStore(path string) (*store.File, error) {
	if path == "" {
		return nil, usagef("--data is empty")
	}
	return store.Open(path)
}

func (c *cli) orgCreate(args []string) error {
	fs := c.newFlags("admin org create")
	data := c.dataFlag(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 1, "<name>"); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	if err := admin.CreateOrg(st, pos[0], time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "created org %s\n", pos[0])
	return nil
}

func (c *cli) orgList(args []string) error {
	fs := c.newFlags("admin org list")
	data := c.dataFlag(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ORG\tDEVICES\tITEMS\tLIVE\tLINEAR TEAM\tPOLICY\tADMIN KEYS")
	err = st.View(func(d *store.Data) error {
		for _, name := range sortedKeys(d.Orgs) {
			o := d.Orgs[name]
			devs := 0
			for _, dv := range d.Devices {
				if dv.Org == name && !dv.Revoked() {
					devs++
				}
			}
			pol := "-"
			if o.Policy != nil {
				pol = fmt.Sprintf("v%d (%s)", o.Policy.Version, o.Policy.KeyID)
			}
			team := o.LinearTeam
			if team == "" {
				team = "-"
			}
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t%s\t%s\n", name, devs, d.OrgItems(name), d.OrgLiveItems(name), team, pol, strings.Join(sortedKeys(o.AdminKeys), ","))
		}
		return nil
	})
	tw.Flush()
	return err
}

func (c *cli) enrollCreate(args []string) error {
	fs := c.newFlags("admin enroll create")
	data := c.dataFlag(fs)
	org := fs.String("org", "", "org the token enrolls into (required)")
	label := fs.String("label", "", "member name the enrolled device carries (e.g. \"Ana Lopez\")")
	ttl := fs.Duration("ttl", admin.DefaultTokenTTL, "token lifetime")
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
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	tok, exp, err := admin.CreateEnrollToken(st, *org, *label, *ttl, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(c.stderr, "enroll token for org %s, single use, expires %s.\nIt is shown ONCE and stored only as a hash:\n", *org, exp.Format(time.RFC3339))
	fmt.Fprintln(c.stdout, tok)
	return nil
}

func (c *cli) devicesList(args []string) error {
	fs := c.newFlags("admin devices list")
	data := c.dataFlag(fs)
	org := fs.String("org", "", "only this org")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "DEVICE\tORG\tNAME\tMEMBER\tSTATUS\tLAST SEEN\tPOLICY\tFREE\tAGENTS\tREPOS")
	err = st.View(func(d *store.Data) error {
		for _, id := range sortedKeys(d.Devices) {
			dv := d.Devices[id]
			if *org != "" && dv.Org != *org {
				continue
			}
			status := "active"
			if dv.Revoked() {
				status = "revoked " + dv.RevokedAt.Format(time.RFC3339)
			}
			seen := "never"
			if !dv.LastSeen.IsZero() {
				seen = dv.LastSeen.Format(time.RFC3339)
			}
			member, pol := dash(dv.Member), "-"
			if dv.PolicyVersion > 0 {
				pol = fmt.Sprintf("v%d", dv.PolicyVersion)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%d\n", dv.ID, dv.Org, dv.Name, member, status, seen, pol, dv.FreeSlots, strings.Join(dv.Agents, ","), len(dv.Repos))
		}
		return nil
	})
	tw.Flush()
	return err
}

// orgLinear sets (or with --clear removes) the Linear team an org's
// escalations are filed in.
func (c *cli) orgLinear(args []string) error {
	fs := c.newFlags("admin org linear")
	data := c.dataFlag(fs)
	team := fs.String("team", "", "Linear team id")
	clear := fs.Bool("clear", false, "remove the org's team")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 1, "<org>"); err != nil {
		return err
	}
	if *clear == (*team != "") {
		return usagef("give exactly one of --team ID or --clear")
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	name := pos[0]
	if err := admin.SetLinearTeam(st, name, *team, time.Now()); err != nil {
		return err
	}
	if *clear {
		fmt.Fprintf(c.stdout, "org %s: Linear team cleared\n", name)
	} else {
		fmt.Fprintf(c.stdout, "org %s: escalations go to Linear team %s\n", name, *team)
	}
	return nil
}

// itemsPurge deletes items: one by id, or an org's items in a state.
func (c *cli) itemsPurge(args []string) error {
	fs := c.newFlags("admin items purge")
	data := c.dataFlag(fs)
	org := fs.String("org", "", "purge this org's items (with --state)")
	state := fs.String("state", "", "only items in this state")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	byID := len(pos) == 1
	if len(pos) > 1 || byID == (*org != "") || (!byID && *state == "") {
		return usagef("admin items purge <item-id> | --org ORG --state STATE")
	}
	if *state != "" && !hubproto.State(*state).Valid() {
		return usagef("unknown --state %q", *state)
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	n := 1
	if byID {
		err = admin.PurgeItem(st, "", pos[0], hubproto.State(*state), time.Now())
	} else {
		n, err = admin.PurgeState(st, *org, hubproto.State(*state), time.Now())
	}
	if err == nil {
		fmt.Fprintf(c.stdout, "purged %d item(s)\n", n)
	}
	return err
}

func (c *cli) devicesRevoke(args []string) error {
	fs := c.newFlags("admin devices revoke")
	data := c.dataFlag(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 1, "<device-id>"); err != nil {
		return err
	}
	id := pos[0]
	if !hubproto.ValidDeviceID(id) {
		return errors.New("device id must be 32 lowercase hex characters")
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	reopened, err := admin.RevokeDevice(st, "", id, time.Now())
	if err == nil {
		fmt.Fprintf(c.stdout, "revoked %s; reopened %d leased item(s)\n", id, reopened)
	}
	return err
}

func (c *cli) itemsList(args []string) error {
	fs := c.newFlags("admin items list")
	data := c.dataFlag(fs)
	org := fs.String("org", "", "only this org")
	state := fs.String("state", "", "only this state")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	if *state != "" && !hubproto.State(*state).Valid() {
		return usagef("unknown --state %q", *state)
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ITEM\tORG\tSTATE\tKIND\tREPO\tSUBJECT\tSEV\tTRIES\tREPORTERS\tLEASE\tOUTCOME\tESCALATION")
	err = st.View(func(d *store.Data) error {
		var items []*store.WorkItem
		for _, w := range d.Items {
			if (*org == "" || w.Org == *org) && (*state == "" || string(w.State) == *state) {
				items = append(items, w)
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].FirstSeen.Before(items[j].FirstSeen) })
		for _, w := range items {
			subject := "-"
			switch {
			case w.Item.DepAdvisory != nil:
				subject = w.Item.DepAdvisory.Advisory + " " + w.Item.DepAdvisory.Package
			case w.Item.RedBumpPR != nil:
				subject = fmt.Sprintf("#%d %s", w.Item.RedBumpPR.PR, w.Item.RedBumpPR.Package)
			}
			ls := "-"
			if w.Lease != nil {
				ls = w.Lease.Device + " until " + w.Lease.Expires.Format(time.RFC3339)
			} else if w.Owner != "" {
				ls = "owner " + w.Owner
			}
			out := "-"
			if w.Outcome != nil {
				out = w.Outcome.PRURL + " (" + string(w.Outcome.Delivery) + ")"
			}
			esc := "-"
			if x := w.Escalation; x != nil {
				switch {
				case x.LinearID != "" && x.Err == escalate.ClosedMark:
					esc = x.LinearID + " (closed)"
				case x.LinearID != "":
					esc = x.LinearID
				case x.Err != "":
					esc = "pending (" + x.Err + ")"
				default:
					esc = "pending"
				}
				esc = x.Reason + ": " + esc
			}
			sev := string(w.Item.Severity())
			if sev == "" {
				sev = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
				w.ID, w.Org, w.State, w.Item.Kind, w.Item.Repo(), subject, sev, w.Attempts, w.Reporters, ls, out, esc)
		}
		return nil
	})
	tw.Flush()
	return err
}

func (c *cli) eventsList(args []string) error {
	fs := c.newFlags("admin events list")
	data := c.dataFlag(fs)
	org := fs.String("org", "", "only this org")
	limit := fs.Int("limit", 50, "most recent N events")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	return st.View(func(d *store.Data) error {
		var evs []store.Event
		for _, e := range d.Events {
			if *org == "" || e.Org == *org {
				evs = append(evs, e)
			}
		}
		if *limit > 0 && len(evs) > *limit {
			evs = evs[len(evs)-*limit:]
		}
		for _, e := range evs {
			fmt.Fprintf(c.stdout, "%s %s %s", e.At.Format(time.RFC3339), e.Org, e.Kind)
			for _, kv := range [][2]string{{"item", e.Item}, {"device", e.Device}, {"detail", e.Detail}} {
				if kv[1] != "" {
					fmt.Fprintf(c.stdout, " %s=%s", kv[0], kv[1])
				}
			}
			fmt.Fprintln(c.stdout)
		}
		return nil
	})
}

func (c *cli) policyKeysAdd(args []string) error {
	fs := c.newFlags("admin policy keys add")
	data := c.dataFlag(fs)
	org := fs.String("org", "", "org (required)")
	keyID := fs.String("key-id", "", "policy key id (required)")
	pubStr := fs.String("public-key", "", "public key, base64url (required)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	if *org == "" || *keyID == "" || *pubStr == "" {
		return usagef("--org, --key-id and --public-key are required")
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	trusted, err := admin.AddPolicyKey(st, *org, *keyID, *pubStr, time.Now())
	if err == nil {
		fmt.Fprintf(c.stdout, "registered policy key %s for org %s\ndaemons pin it with: trusted_policy_keys = [%q]\n", *keyID, *org, trusted)
	}
	return err
}

func (c *cli) policyPublish(args []string) error {
	fs := c.newFlags("admin policy publish")
	data := c.dataFlag(fs)
	register := fs.String("register-key", "", "also register this <key_id>:<public key> (the bundle's signer) in the same step")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 1, "<signed-policy.json>"); err != nil {
		return err
	}
	raw, err := os.ReadFile(pos[0])
	if err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	p, err := admin.PublishPolicyWithKey(st, "", raw, *register, time.Now())
	if err == nil {
		fmt.Fprintf(c.stdout, "published policy v%d for org %s (key %s)\n", p.Version, p.Org, p.KeyID)
	}
	return err
}

// itemsAdd stores a chore by hand, as if a device had reported it.
func (c *cli) itemsAdd(args []string) error {
	fs := c.newFlags("admin items add")
	data := c.dataFlag(fs)
	org := fs.String("org", "", "org (required)")
	var f admin.ItemFields
	fs.StringVar(&f.Kind, "kind", "", "dep_advisory | red_bump_pr (required)")
	fs.StringVar(&f.Repo, "repo", "", "owner/name (required)")
	fs.StringVar(&f.Package, "package", "", "package name (required)")
	fs.StringVar(&f.Ecosystem, "ecosystem", "", "dep_advisory: npm, pip, composer, ...")
	fs.StringVar(&f.Vulnerable, "vulnerable", "", "dep_advisory: vulnerable range, e.g. \"< 1.2.3\"")
	fs.StringVar(&f.Fixed, "fixed", "", "dep_advisory: first fixed version (may be empty)")
	fs.StringVar(&f.Advisory, "advisory", "", "dep_advisory: GHSA/CVE/OSV id")
	fs.StringVar(&f.Severity, "severity", "", "dep_advisory: low|moderate|high|critical")
	fs.StringVar(&f.PR, "pr", "", "red_bump_pr: PR number")
	fs.StringVar(&f.Branch, "branch", "", "red_bump_pr: the PR's head branch")
	fs.StringVar(&f.Author, "author", hubproto.AuthorDependabot, "red_bump_pr: dependabot[bot] | renovate[bot]")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	if *org == "" || f.Kind == "" {
		return usagef("--org and --kind are required")
	}
	it, err := admin.BuildItem(f)
	if err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	id, created, err := admin.AddItem(st, *org, it, time.Now())
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(c.stdout, "added %s %s\n", it.Kind, id)
	} else {
		fmt.Fprintf(c.stdout, "already known as %s (re-reported)\n", id)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// enrollList lists enroll tokens: never the secret, only the public id (a
// hash prefix), the label and the state.
func (c *cli) enrollList(args []string) error {
	fs := c.newFlags("admin enroll list")
	data := c.dataFlag(fs)
	org := fs.String("org", "", "only this org")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	list, err := admin.ListEnrollTokens(st, *org, time.Now())
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TOKEN ID\tORG\tLABEL\tCREATED\tEXPIRES\tSTATE")
	for _, t := range list {
		state := t.Status
		if t.Status == store.TokenUsed {
			state = "used by " + dash(t.UsedBy)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Org, dash(t.Label), t.CreatedAt.Format(time.RFC3339), t.ExpiresAt.Format(time.RFC3339), state)
	}
	return tw.Flush()
}

func (c *cli) enrollRevoke(args []string) error {
	fs := c.newFlags("admin enroll revoke")
	data := c.dataFlag(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 1, "<token-id>"); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	if err := admin.RevokeEnrollToken(st, "", pos[0], time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "revoked enroll token %s\n", pos[0])
	return nil
}

// itemsMove resolves (escalated -> done) or reopens (escalated -> open,
// attempts reset) an escalated item.
func (c *cli) itemsMove(args []string, verb string) error {
	fs := c.newFlags("admin items " + verb)
	data := c.dataFlag(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 1, "<item-id>"); err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	if verb == "resolve" {
		err = admin.ResolveItem(st, "", pos[0], time.Now())
	} else {
		err = admin.ReopenItem(st, "", pos[0], time.Now())
	}
	if err == nil {
		done := map[string]string{"resolve": "resolved (done)", "reopen": "reopened (open, attempts reset)"}[verb]
		fmt.Fprintf(c.stdout, "%s %s\n", done, pos[0])
	}
	return err
}
