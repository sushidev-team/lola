package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/qr"
	"github.com/sushidev-team/lola/internal/timefmt"
	"github.com/sushidev-team/lola/internal/tui"
)

const (
	// pairPollWait is one pairStatus long-poll.
	pairPollWait = 25 * time.Second
	// pairRejectBudget bounds the pairReject sent on an interrupt.
	pairRejectBudget = 2 * time.Second
	// pairBeginBudget covers pairBegin, which may wait for the hub's relay
	// registration (protocol.PairBeginRelayWait) before it answers.
	pairBeginBudget = protocol.PairBeginRelayWait + 5*time.Second
	// pairExitInterrupted is the shell convention for SIGINT.
	pairExitInterrupted = 130
)

// Seams (tests replace them).
var (
	pairSend = tui.RequestResponse
	pairNow  = time.Now
	// pairStdoutTTY reports whether stdout is a terminal and its width in
	// columns (0 when unknown).
	pairStdoutTTY = func() (bool, int) {
		fd := os.Stdout.Fd()
		if !term.IsTerminal(fd) {
			return false, 0
		}
		w, _, err := term.GetSize(fd)
		if err != nil {
			return true, 0
		}
		return true, w
	}
)

var pairTriesLeftRe = regexp.MustCompile(`does not match.*?(\d+) tries left`)

// pairOptions is what the flags resolve to.
type pairOptions struct {
	tiers         []string // nil = the daemon's default
	name          string
	json          bool
	allowLoopback bool
}

func pairCmd() *cobra.Command {
	var noShell, asJSON, allowLoopback bool
	var tiers, name string
	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Pair a phone: show a QR code, then confirm the code the phone displays",
		Long: "Open a pairing window and show its QR code. Scan it with Lola on the phone,\n" +
			"then type the 6-digit code the phone shows. An empty line rejects.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			set, err := pairTiers(tiers, noShell)
			if err != nil {
				return err
			}
			parent := c.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runPair(ctx, pairOptions{tiers: set, name: name, json: asJSON, allowLoopback: allowLoopback},
				c.InOrStdin(), c.OutOrStdout(), c.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&noShell, "no-shell", false, "do not grant the shell tier")
	cmd.Flags().StringVar(&tiers, "tiers", "", "comma-separated tiers to grant (must include read)")
	cmd.Flags().StringVar(&name, "name", "", "name to store for the device instead of the one it sent")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print one JSON object per event")
	cmd.Flags().BoolVar(&allowLoopback, "allow-loopback", false, "accept a loopback-only QR (simulator and development)")
	_ = cmd.Flags().MarkHidden("allow-loopback")
	return cmd
}

// pairTiers resolves --tiers and --no-shell. nil means "the daemon's default".
func pairTiers(list string, noShell bool) ([]string, error) {
	if list == "" && !noShell {
		return nil, nil
	}
	set := slices.Clone(protocol.DefaultTiers)
	if list != "" {
		set = set[:0]
		for _, t := range strings.Split(list, ",") {
			t = strings.TrimSpace(t)
			if !slices.Contains(protocol.Tiers, t) {
				return nil, fmt.Errorf("unknown tier %q (tiers: %s)", t, strings.Join(protocol.Tiers, ", "))
			}
			if !slices.Contains(set, t) {
				set = append(set, t)
			}
		}
	}
	if noShell {
		set = slices.DeleteFunc(set, func(t string) bool { return t == protocol.TierShell })
	}
	if !slices.Contains(set, protocol.TierRead) {
		return nil, errors.New("the tiers must include read")
	}
	return set, nil
}

// pairCall sends one command and decodes its data. It returns as soon as ctx
// ends, so an interrupt is never held up by a long-poll.
func pairCall(ctx context.Context, cmd string, args, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	type result struct {
		resp *protocol.Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := pairSend(protocol.Request{Cmd: cmd, Args: raw})
		ch <- result{resp, err}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if !r.resp.OK {
			if r.resp.Error == "" {
				return errors.New("daemon reported failure")
			}
			return errors.New(r.resp.Error)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(r.resp.Data, out)
	}
}

// pairLines reads stdin one line at a time. Closing stdin yields "" forever,
// which the caller reads as a rejection.
func pairLines(in io.Reader) <-chan string {
	ch := make(chan string)
	go func() {
		r := bufio.NewReader(in)
		for {
			line, _ := r.ReadString('\n')
			ch <- strings.TrimSpace(line)
		}
	}()
	return ch
}

type pairJSONBegin struct {
	Event string `json:"event"`
	protocol.PairBeginData
}

type pairJSONStatus struct {
	Event string `json:"event"`
	protocol.PairStatusData
}

func runPair(ctx context.Context, o pairOptions, in io.Reader, out, errw io.Writer) error {
	// Human text goes to stdout, except under --json where stdout is the event stream.
	say := out
	if o.json {
		say = errw
	}
	emit := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintln(out, string(b))
	}

	var begin protocol.PairBeginData
	bctx, cancel := context.WithTimeout(ctx, pairBeginBudget)
	err := pairCall(bctx, protocol.CmdPairBegin, protocol.PairBeginArgs{AllowLoopback: o.allowLoopback}, &begin)
	cancel()
	if err != nil {
		return err
	}
	if begin.Problem != "" {
		fmt.Fprintln(errw, begin.Problem)
		return sshExit(1)
	}

	// reject cancels the pairing; the daemon treats a reject of a waiting one as a cancel.
	reject := func() {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), pairRejectBudget)
		defer rcancel()
		_ = pairCall(rctx, protocol.CmdPairReject, protocol.PairRejectArgs{Pairing: begin.Pairing}, nil)
	}
	interrupted := func() error {
		reject()
		fmt.Fprintln(errw, "Cancelled.")
		return sshExit(pairExitInterrupted)
	}

	if o.json {
		emit(pairJSONBegin{Event: "begin", PairBeginData: begin})
	} else {
		pairShowCode(out, begin)
	}

	lines := pairLines(in)
	since := protocol.PairStateWaiting
	for {
		var st protocol.PairStatusData
		args := protocol.PairStatusArgs{Pairing: begin.Pairing, Since: since, WaitMs: int(pairPollWait / time.Millisecond)}
		if err := pairCall(ctx, protocol.CmdPairStatus, args, &st); err != nil {
			if ctx.Err() != nil {
				return interrupted()
			}
			return err
		}
		if o.json {
			emit(pairJSONStatus{Event: "status", PairStatusData: st})
		}
		since = st.State
		switch st.State {
		case protocol.PairStateWaiting:
		case protocol.PairStateConfirming:
			done, err := pairConfirm(ctx, o, st, begin.Pairing, lines, say, errw, reject, emit)
			if ctx.Err() != nil {
				return interrupted()
			}
			if err != nil || done {
				return err
			}
			since = protocol.PairStateConfirming
		case protocol.PairStatePaired:
			fmt.Fprintf(say, "Paired %s.\n", pairName(st))
			return nil
		default:
			fmt.Fprintln(errw, pairTerminalText(st))
			return sshExit(1)
		}
	}
}

// pairConfirm prompts for the code and confirms it, re-asking on a wrong one.
// done is true once the pairing ended (paired, or rejected by an empty line).
func pairConfirm(ctx context.Context, o pairOptions, st protocol.PairStatusData, id string, lines <-chan string,
	say, errw io.Writer, reject func(), emit func(any)) (done bool, err error) {
	fmt.Fprint(say, pairPrompt(st))
	for {
		var line string
		select {
		case <-ctx.Done():
			return false, nil
		case line = <-lines:
		}
		if line == "" {
			reject()
			fmt.Fprintln(errw, "Rejected.")
			return true, sshExit(1)
		}
		var res protocol.PairStatusData
		err := pairCall(ctx, protocol.CmdPairConfirm, protocol.PairConfirmArgs{Pairing: id, Code: line, Tiers: o.tiers, Name: o.name}, &res)
		if ctx.Err() != nil {
			return false, nil
		}
		if err != nil {
			if m := pairTriesLeftRe.FindStringSubmatch(err.Error()); m != nil {
				if n, _ := strconv.Atoi(m[1]); n > 0 {
					fmt.Fprintf(say, "That code does not match; %d tries left.\n", n)
					fmt.Fprint(say, "Code: ")
					continue
				}
			}
			fmt.Fprintln(errw, err.Error())
			return true, sshExit(1)
		}
		if o.json {
			emit(pairJSONStatus{Event: "status", PairStatusData: res})
		}
		switch res.State {
		case protocol.PairStatePaired:
			fmt.Fprintf(say, "Paired %s.\n", pairName(res))
			return true, nil
		case protocol.PairStateConfirming, protocol.PairStateWaiting:
			return false, nil
		default:
			fmt.Fprintln(errw, pairTerminalText(res))
			return true, sshExit(1)
		}
	}
}

// pairName is the device's display name from a status.
func pairName(st protocol.PairStatusData) string {
	if st.Device != nil && st.Device.Name != "" {
		return st.Device.Name
	}
	return st.DeviceName
}

// pairPrompt is the one line that names the device and asks for the code.
func pairPrompt(st protocol.PairStatusData) string {
	var b strings.Builder
	if st.NameClash || st.Replaces != "" {
		b.WriteString("New device: ")
	}
	fmt.Fprintf(&b, "%q", st.DeviceName)
	if st.Via == "relay" {
		b.WriteString(" (via relay)")
	}
	if st.Replaces != "" {
		fmt.Fprintf(&b, " (re-pairs %s)", st.Replaces)
	}
	b.WriteString(" wants to connect. Type the 6-digit code shown on the phone (empty line rejects): ")
	return b.String()
}

// pairTerminalText names a pairing that ended without pairing.
func pairTerminalText(st protocol.PairStatusData) string {
	switch st.State {
	case protocol.PairStateRejected:
		return "The pairing was rejected."
	case protocol.PairStateExpired:
		return "The pairing expired. Run lola pair again."
	case protocol.PairStateCancelled:
		return "The pairing was cancelled."
	}
	switch st.Reason {
	case protocol.PairReasonTooManyAttempts, protocol.PairReasonCodeMismatch:
		return "The pairing failed: too many wrong codes."
	case protocol.PairReasonPhoneLeft:
		return "The pairing failed: the phone disconnected."
	case protocol.PairReasonRegistry:
		return "The pairing failed: the device registry could not be written."
	case protocol.PairReasonFull:
		return "The pairing failed: too many devices are paired."
	}
	return "The pairing failed."
}

// pairShowCode prints the QR (or the reason it cannot be shown) and the
// expiry line. The payload text is printed only when stdout is not a terminal.
func pairShowCode(out io.Writer, b protocol.PairBeginData) {
	tty, cols := pairStdoutTTY()
	if !tty {
		fmt.Fprintln(out, b.Payload)
	} else if m, err := qr.Encode([]byte(b.Payload), qr.M); err != nil {
		fmt.Fprintln(out, "The code is too large to show; run lola pair --json.")
	} else {
		if cols <= 0 {
			if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
				cols = n
			} else {
				cols = 80
			}
		}
		s, err := qr.Terminal(m, qr.TerminalOptions{NoColor: os.Getenv("NO_COLOR") != "", Columns: cols})
		if errors.Is(err, qr.ErrTooWide) {
			fmt.Fprintf(out, "Widen the terminal to %d columns to show the code, or run lola pair --json.\n", m.Size+8)
		} else if err == nil {
			fmt.Fprint(out, s)
		}
	}
	left := 5 * time.Minute
	if !b.ExpiresAt.IsZero() {
		left = max(b.ExpiresAt.Sub(pairNow()), 0)
	}
	secs := int(left.Round(time.Second) / time.Second)
	fmt.Fprintf(out, "Scan with Lola on your phone. Expires in %d:%02d.\n", secs/60, secs%60)
}

func devicesCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "devices",
		Short: "List paired phones, revoke one or change its tiers",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d, err := listDevices(c.Context())
			if err != nil {
				return err
			}
			if asJSON {
				b, _ := json.MarshalIndent(d, "", "  ")
				fmt.Fprintln(c.OutOrStdout(), string(b))
				return nil
			}
			return printDevices(c.OutOrStdout(), d.Devices)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the devices as JSON")

	cmd.AddCommand(&cobra.Command{
		Use:   "revoke <id|name>",
		Short: "Revoke a device and close its connections",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			dev, err := resolveDevice(c.Context(), a[0])
			if err != nil {
				return err
			}
			var r protocol.RevokeDeviceData
			if err := pairCall(c.Context(), protocol.CmdRevokeDevice, protocol.RevokeDeviceArgs{ID: dev.ID}, &r); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Revoked %s; closed %d connection(s).\n", dev.Name, r.Closed)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "tiers <id|name> <tier,...>",
		Short: "Replace a device's tiers",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, a []string) error {
			set, err := pairTiers(a[1], false)
			if err != nil {
				return err
			}
			dev, err := resolveDevice(c.Context(), a[0])
			if err != nil {
				return err
			}
			var r protocol.DeviceInfo
			if err := pairCall(c.Context(), protocol.CmdSetDeviceTiers, protocol.SetDeviceTiersArgs{ID: dev.ID, Tiers: set}, &r); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "%s: %s\n", dev.Name, strings.Join(r.Tiers, ", "))
			return nil
		},
	})
	return cmd
}

func listDevices(ctx context.Context) (protocol.DevicesData, error) {
	var d protocol.DevicesData
	if err := pairCall(ctx, protocol.CmdDevices, struct{}{}, &d); err != nil {
		return d, err
	}
	if d.Problem != "" {
		return d, errors.New(d.Problem)
	}
	return d, nil
}

// resolveDevice finds the one device whose id or name is s. Several matches
// are an error that names their ids.
func resolveDevice(ctx context.Context, s string) (protocol.DeviceInfo, error) {
	d, err := listDevices(ctx)
	if err != nil {
		return protocol.DeviceInfo{}, err
	}
	var hits []protocol.DeviceInfo
	for _, dev := range d.Devices {
		if dev.ID == s || dev.Name == s {
			hits = append(hits, dev)
		}
	}
	switch len(hits) {
	case 0:
		return protocol.DeviceInfo{}, fmt.Errorf("no device %q", s)
	case 1:
		return hits[0], nil
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	return protocol.DeviceInfo{}, fmt.Errorf("%q matches %d devices: %s", s, len(hits), strings.Join(ids, ", "))
}

func printDevices(w io.Writer, devs []protocol.DeviceInfo) error {
	if len(devs) == 0 {
		fmt.Fprintln(w, "No paired devices.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tID\tTIERS\tLAST SEEN\tCONNECTED")
	now := pairNow()
	for _, d := range devs {
		seen := "never"
		if !d.LastSeenAt.IsZero() {
			seen = timefmt.Short(d.LastSeenAt, now)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", d.Name, d.ID, strings.Join(d.Tiers, ","), seen, d.Connected)
	}
	return tw.Flush()
}
