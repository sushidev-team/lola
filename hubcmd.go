package main

import (
	"bufio"
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
	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/doctor"
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
	var trustKey bool
	join := &cobra.Command{
		Use:   "join [url]",
		Short: "Enroll this machine with a lola hub using an invite or a one-time token",
		Long: "Enroll this machine with a lola hub. The invite an admin gave you\n" +
			"(lola-hub:<url>#<token>[#<key_id>:<public key>]) or a bare one-time token is\n" +
			"read from stdin (prompted for without echo on a terminal) or from\n" +
			"--token-file, NEVER from the command line: an argument is visible to every\n" +
			"local user in ps and is kept in shell history. With a bare token, give the\n" +
			"hub's url as the argument.\n\n" +
			"An invite may carry the team's policy key. lola prints its id and a short\n" +
			"fingerprint and pins it in [hub].trusted_policy_keys only after you confirm\n" +
			"it (or pass --trust-key); compare the fingerprint with your admin first.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			inv, interactive, err := hubReadInvite(tokenFile, c.InOrStdin(), c.ErrOrStderr(), stdinTerminal)
			if err != nil {
				return err
			}
			url, err := hubJoinURL(a, inv)
			if err != nil {
				return err
			}
			pin := false
			if inv.KeyID != "" {
				if pin, err = hubConfirmKey(inv, trustKey, interactive, c.InOrStdin(), c.ErrOrStderr()); err != nil {
					return err
				}
			}
			out := c.OutOrStdout()
			// A running daemon enrolls itself (and reloads); a daemon that is
			// down, or predates cmd=hubJoin, falls back to the local join.
			local := false
			if resp, ok := hubDaemon(protocol.CmdHubJoin, protocol.HubJoinArgs{URL: url, Token: inv.Token, Name: name}); ok {
				if !resp.OK {
					return errors.New(resp.Error)
				}
				var jd protocol.HubJoinData
				if err := json.Unmarshal(resp.Data, &jd); err != nil {
					return err
				}
				if jd.KeyCreated {
					fmt.Fprintln(out, "created a new device key")
				}
				fmt.Fprintf(out, "joined org %s as %q (device %s)\n", jd.Org, jd.DeviceName, jd.DeviceID)
			} else {
				local = true
				path, state, err := hubPaths()
				if err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(c.Context(), hubJoinTimeout)
				defer cancel()
				res, err := hub.Join(ctx, hub.JoinOptions{URL: url, Token: inv.Token, DeviceName: name, ConfigPath: path, StateDir: state})
				if err != nil {
					return err
				}
				if res.KeyCreated {
					fmt.Fprintln(out, "created a new device key")
				}
				fmt.Fprintf(out, "joined org %s as %q (device %s)\n", res.Identity.Org, res.Identity.DeviceName, res.Identity.DeviceID)
			}
			if pin {
				path, err := config.DefaultPath()
				if err != nil {
					return err
				}
				added, err := hub.PinTrustedKey(path, inv.KeyID, inv.PublicKey)
				switch {
				case err != nil:
					fmt.Fprintf(c.ErrOrStderr(), "the team policy key was NOT pinned: %v\n", err)
				case added:
					fmt.Fprintf(out, "pinned team policy key %s in [hub].trusted_policy_keys\n", inv.KeyID)
					local = true
				default:
					fmt.Fprintf(out, "team policy key %s was already pinned\n", inv.KeyID)
				}
			}
			if local {
				reloadDaemon(c)
			}
			if cfg, err := loadHubConfig(); err == nil {
				fmt.Fprintln(out, "\nready for team chores?")
				fmt.Fprint(out, renderReadiness(doctor.HubReadiness(c.Context(), cfg)))
			}
			return nil
		},
	}
	join.Flags().StringVar(&name, "name", "", "device name shown to the team (default: hostname)")
	join.Flags().StringVar(&tokenFile, "token-file", "", "read the invite or token from this file (\"-\" = stdin)")
	join.Flags().BoolVar(&trustKey, "trust-key", false, "pin the policy key the invite carries without asking")

	var statusJSON bool
	status := &cobra.Command{
		Use:   "status",
		Short: "Show whether this machine is in a team, what the daemon's hub loop is doing, and what is missing for chores",
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
			st := hub.LocalStatus(cfg, state)
			var daemon *protocol.HubStatusData
			daemonNote := ""
			data, err := tui.Request(protocol.Request{Cmd: hubStatusCmd})
			switch {
			case err != nil && strings.Contains(err.Error(), "unknown cmd"):
				daemonNote = "the running daemon has no hub loop (restart it)"
			case err != nil:
				daemonNote = "not running"
			default:
				var d protocol.HubStatusData
				if err := json.Unmarshal(data, &d); err != nil {
					daemonNote = "answered something lola cannot read"
				} else {
					daemon = &d
				}
			}
			ready := doctor.HubReadiness(c.Context(), cfg)
			if statusJSON {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(hubStatusJSONOf(st, daemon, daemonNote, ready))
			}
			out := c.OutOrStdout()
			fmt.Fprint(out, st.Render())
			fmt.Fprint(out, renderHubDaemon(daemon, daemonNote))
			fmt.Fprintln(out, "\nready for team chores?")
			fmt.Fprint(out, renderReadiness(ready))
			return nil
		},
	}
	status.Flags().BoolVar(&statusJSON, "json", false, "print JSON instead of text")

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
				fmt.Fprintln(c.OutOrStdout(), "left the hub: its leases were released where it answered, and nothing more is reported to it; an admin revokes the device on the hub side")
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

	cmd.AddCommand(join, status, leave, hubBoardCmd(), hubOrgCmd(), hubStatsCmd(), hubClaimCmd(), hubReleaseCmd(), hubSyncCmd())
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
	var project, note string
	var patch, minor, major bool
	cmd := &cobra.Command{
		Use:   "claim <item-id> [item-id...]",
		Short: "Claim hub items now and start one session for them",
		Long: "Claim one hub item, or several dependency advisories of one repository for\n" +
			"ONE session. --patch / --minor / --major also update the repository's other\n" +
			"dependencies by that much, and --note adds your own words for the agent.",
		Args: cobra.RangeArgs(1, config.MaxGroupedChores),
		RunE: func(c *cobra.Command, a []string) error {
			args := protocol.HubClaimArgs{ItemID: a[0], Project: project}
			if len(a) > 1 {
				args.ItemIDs = a
			}
			if patch || minor || major || note != "" {
				args.Scope = &protocol.HubUpdateScope{Patch: patch, Minor: minor, Major: major, Note: note}
			}
			var d protocol.HubClaimData
			if err := hubDaemonData(protocol.CmdHubClaim, args, &d); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "claimed %s for %s: session %s\n", d.ItemID, d.Project, d.Session)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "the [[project]] to work it in, when several match its repo")
	cmd.Flags().BoolVar(&patch, "patch", false, "also update the repository's other dependencies by patch versions")
	cmd.Flags().BoolVar(&minor, "minor", false, "also update them by minor versions")
	cmd.Flags().BoolVar(&major, "major", false, "also update them by major versions")
	cmd.Flags().StringVar(&note, "note", "", "your own note for the agent")
	return cmd
}

func hubOrgCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "org",
		Short: "List the team's machines: who they belong to, when they were last seen, which policy they apply",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			var o protocol.HubOrgData
			if err := hubDaemonData(protocol.CmdHubOrg, nil, &o); err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(o)
			}
			fmt.Fprint(c.OutOrStdout(), renderHubOrg(o, time.Now()))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the daemon's JSON")
	return cmd
}

// renderHubOrg is the org view as a terminal table.
func renderHubOrg(o protocol.HubOrgData, now time.Time) string {
	var out strings.Builder
	fmt.Fprintf(&out, "team %s", o.Org)
	if o.PolicyVersion > 0 {
		fmt.Fprintf(&out, ", policy v%d", o.PolicyVersion)
	}
	out.WriteString("\n")
	if len(o.Devices) == 0 {
		out.WriteString("no machines\n")
		return out.String()
	}
	tw := tabwriter.NewWriter(&out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "MACHINE\tMEMBER\tLAST SEEN\tFREE\tAGENTS\tPOLICY")
	for _, d := range o.Devices {
		name := d.Name
		if d.Me {
			name += " (this one)"
		}
		seen := "never"
		if !d.LastSeen.IsZero() {
			seen = now.Sub(d.LastSeen).Round(time.Minute).String() + " ago"
		}
		if !d.Active {
			seen += ", inactive"
		}
		policy := "-"
		if d.PolicyVersion > 0 {
			policy = fmt.Sprintf("v%d", d.PolicyVersion)
		}
		member := d.Member
		if member == "" {
			member = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", name, member, seen, d.FreeSlots, strings.Join(d.Agents, ","), policy)
	}
	tw.Flush()
	return out.String()
}

func hubStatsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show the team's chore counts per day: reported, fixed, failed, escalated",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			var v hubproto.StatsView
			if err := hubDaemonData(protocol.CmdHubStats, nil, &v); err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(v)
			}
			fmt.Fprint(c.OutOrStdout(), renderHubStats(v))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the hub's JSON")
	return cmd
}

// renderHubStats is the org's daily counters as a terminal table, newest
// day first. The average fix time is the day's fix seconds over its fixes.
func renderHubStats(v hubproto.StatsView) string {
	if len(v.Days) == 0 {
		return "no stats yet\n"
	}
	var out strings.Builder
	tw := tabwriter.NewWriter(&out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "DAY (UTC)\tREPORTED\tFIXED\tFAILED\tESCALATED\tAVG FIX")
	for i := len(v.Days) - 1; i >= 0; i-- {
		d := v.Days[i]
		avg := "-"
		if d.Done > 0 {
			avg = (time.Duration(d.FixSecondsSum/int64(d.Done)) * time.Second).Round(time.Minute).String()
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\n", d.Date, d.Opened, d.Done, d.Failed, d.Escalated, avg)
	}
	tw.Flush()
	return out.String()
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

// hubTokenMax bounds what is read as an invite or token.
const hubTokenMax = 4 << 10

// stdinTerminal reports whether stdin is a terminal, and its fd.
func stdinTerminal() (uintptr, bool) {
	fd := os.Stdin.Fd()
	return fd, term.IsTerminal(fd)
}

// readSecret reads a line from the terminal without echo (a seam for tests).
var readSecret = term.ReadPassword

// hubReadInvite reads the invite or bare token from --token-file or stdin —
// without echo when stdin is a terminal — and parses it. There is no argv
// path: a secret on the command line shows in ps and shell history.
// interactive reports that a person at a terminal typed it, so a question
// can follow on the same terminal.
func hubReadInvite(tokenFile string, stdin io.Reader, stderr io.Writer, tty func() (uintptr, bool)) (inv hub.Invite, interactive bool, err error) {
	var raw []byte
	switch {
	case tokenFile != "" && tokenFile != "-":
		f, ferr := os.Open(tokenFile)
		if ferr != nil {
			return hub.Invite{}, false, ferr
		}
		defer f.Close()
		raw, err = io.ReadAll(io.LimitReader(f, hubTokenMax+1))
	default:
		if fd, ok := tty(); ok && tokenFile == "" {
			fmt.Fprint(stderr, "invite or one-time enroll token: ")
			raw, err = readSecret(fd)
			fmt.Fprintln(stderr)
			interactive = true
		} else {
			raw, err = io.ReadAll(io.LimitReader(stdin, hubTokenMax+1))
		}
	}
	if err != nil {
		return hub.Invite{}, false, fmt.Errorf("read the invite: %w", err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return hub.Invite{}, false, errors.New("no invite or enroll token: pipe it on stdin or use --token-file")
	}
	inv, err = hub.ParseInvite(string(raw))
	return inv, interactive, err
}

// hubJoinURL picks the hub url: the invite's, or the argument (which must
// agree with the invite when both are given).
func hubJoinURL(args []string, inv hub.Invite) (string, error) {
	arg := ""
	if len(args) > 0 {
		arg = strings.TrimSuffix(strings.TrimSpace(args[0]), "/")
	}
	switch {
	case inv.URL != "" && arg != "" && arg != inv.URL:
		return "", errors.New("the url given does not match the invite's")
	case inv.URL != "":
		return inv.URL, nil
	case arg == "":
		return "", errors.New("a bare token needs the hub's url: lola hub join <url>")
	}
	return arg, nil
}

// hubConfirmKey shows the invite's policy key (id + fingerprint, never asked
// of the hub) and decides whether to pin it: --trust-key, or a person typing
// "yes" at the terminal. Piped input cannot confirm, so it pins nothing and
// says how to.
func hubConfirmKey(inv hub.Invite, trust, interactive bool, in io.Reader, w io.Writer) (bool, error) {
	fmt.Fprintf(w, "the invite carries team policy key %s, fingerprint %s\n", inv.KeyID, hub.KeyFingerprint(inv.PublicKey))
	if trust {
		return true, nil
	}
	if !interactive {
		fmt.Fprintln(w, "not pinned: compare the fingerprint with your admin, then join again with --trust-key")
		return false, nil
	}
	fmt.Fprint(w, "compare the fingerprint with your admin. Pin this key? Type yes: ")
	line, err := bufio.NewReader(io.LimitReader(in, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read the answer: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(line), "yes") {
		return true, nil
	}
	fmt.Fprintln(w, "not pinned")
	return false, nil
}

// loadHubConfig loads the config file (no overlay: this is the file).
func loadHubConfig() (*config.Config, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return nil, err
	}
	return config.Load(path)
}

// renderReadiness prints the readiness rows: "ok" or "--" and the plain
// words of each.
func renderReadiness(rows []doctor.Result) string {
	var b strings.Builder
	for _, r := range rows {
		mark := "--"
		if r.OK {
			mark = "ok"
		}
		fmt.Fprintf(&b, "  %s  %s: %s\n", mark, r.Name, r.Detail)
	}
	return b.String()
}

// renderHubDaemon prints the daemon's hub loop state in plain words.
func renderHubDaemon(d *protocol.HubStatusData, note string) string {
	var b strings.Builder
	if d == nil {
		fmt.Fprintf(&b, "daemon:   %s\n", note)
		return b.String()
	}
	switch {
	case !d.Enabled:
		b.WriteString("daemon:   hub off\n")
	case d.LastHeartbeat.IsZero():
		b.WriteString("daemon:   has not reached the hub yet\n")
	default:
		fmt.Fprintf(&b, "daemon:   last reached the hub at %s\n", d.LastHeartbeat.Local().Format(time.RFC3339))
	}
	if d.LastError != "" {
		fmt.Fprintf(&b, "          last problem: %s\n", d.LastError)
	}
	if d.LegacyHub {
		b.WriteString("          the hub is an older version; lola uses its older requests\n")
	}
	fmt.Fprintf(&b, "          chores claimed today: %d of %d\n", d.ClaimsToday, d.MaxClaimsPerDay)
	if d.PendingOutcomes > 0 {
		fmt.Fprintf(&b, "          results waiting to be sent: %d\n", d.PendingOutcomes)
	}
	if p := d.Policy; p != nil {
		if p.Applied {
			fmt.Fprintf(&b, "          team policy v%d applies (%d setting(s) locked)\n", p.Version, len(p.AppliedKeys))
			for _, k := range p.AppliedKeys {
				fmt.Fprintf(&b, "            %s: team %s, yours %s\n", k.Key, k.Value, k.Local)
			}
		} else {
			fmt.Fprintf(&b, "          team policy v%d does not apply: %s\n", p.Version, p.ApplyNote)
		}
	}
	for _, s := range d.Sessions {
		state := "working"
		switch {
		case s.Released:
			state = "released"
		case s.LeaseLost:
			state = "lease lost"
		case s.InReview:
			state = "in review"
		case s.Reported != "":
			state = "reported " + s.Reported
		}
		fmt.Fprintf(&b, "          session %s: %s (%s)\n", s.Session, s.Summary, state)
	}
	return b.String()
}

// hubStatusJSON is `lola hub status --json`.
type hubStatusJSON struct {
	Membership string                  `json:"membership"`
	Enabled    bool                    `json:"enabled"`
	URL        string                  `json:"url,omitempty"`
	Identity   *hub.Identity           `json:"identity,omitempty"`
	Revoked    *hub.Revocation         `json:"revoked,omitempty"`
	KeyPresent bool                    `json:"keyPresent"`
	KeyProblem string                  `json:"keyProblem,omitempty"`
	Daemon     *protocol.HubStatusData `json:"daemon,omitempty"`
	DaemonNote string                  `json:"daemonNote,omitempty"`
	Readiness  []hubReadyJSON          `json:"readiness"`
}

type hubReadyJSON struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func hubStatusJSONOf(st hub.Status, d *protocol.HubStatusData, note string, ready []doctor.Result) hubStatusJSON {
	out := hubStatusJSON{Membership: st.Membership(), Enabled: st.Enabled, URL: st.URL, Revoked: st.Revoked,
		KeyPresent: st.KeyPresent, Daemon: d, DaemonNote: note, Readiness: []hubReadyJSON{}}
	if st.Joined {
		id := st.Identity
		out.Identity = &id
	}
	switch {
	case st.KeyErr != "":
		out.KeyProblem = st.KeyErr
	case st.KeyMismatch:
		out.KeyProblem = "the device key is not the one that enrolled"
	}
	for _, r := range ready {
		out.Readiness = append(out.Readiness, hubReadyJSON{Name: r.Name, OK: r.OK, Detail: r.Detail, Fix: string(r.Fix)})
	}
	return out
}
