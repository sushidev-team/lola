package daemon

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
)

// Pairing and device commands (docs/mobile/01-pairing-and-tiers.md §6.1-6.4,
// §8.8). All seven are LOCAL-ONLY: the unix socket reaches them, a phone never
// does (tier "never" in internal/remote, refused again by handleRemote).
//
// Nothing on these paths logs a request or a reply. pairBegin's reply carries
// the pairing secret inside the QR payload and pairConfirm's request carries
// the typed code; the audit lines below name ids, names and tiers only.

// errUnknownDevice is the sentence a human gets for an id the registry does not
// hold. It names no other device.
var errUnknownDevice = errors.New("no such device")

// maxQRAddrs mirrors the payload's address bound (remote.PairPayload).
const maxQRAddrs = 8

// handlePairBegin opens a pairing window and returns the QR payload for it.
//
// It answers about the listener that is RUNNING, not about configuration: the
// only reason the question is put to the daemon at all. A Problem is a rendered
// state (a human pressed a button and must learn which thing to fix); nothing
// else in the reply is set with it and no window is left open.
func (d *Daemon) handlePairBegin(ctx context.Context, a protocol.PairBeginArgs) (protocol.PairBeginData, error) {
	// Lock order is remoteMu -> d.mu and never the reverse, so the config is
	// read and RELEASED before the server is looked at.
	d.mu.Lock()
	listens := d.cfg.Remote.Listens()
	d.mu.Unlock()

	d.remoteMu.Lock()
	srv := d.remote
	lastErr := d.remoteErr
	d.remoteMu.Unlock()

	if srv == nil {
		switch {
		case !listens:
			return protocol.PairBeginData{Problem: "The phone listener is off. Enable [remote] and reload before connecting a phone."}, nil
		case lastErr != "":
			// The REASON the listener refused, rather than a guess at one.
			// internal/remote never lets a key reach an error.
			return protocol.PairBeginData{Problem: "[remote] is enabled but the listener did not start: " + lastErr}, nil
		}
		return protocol.PairBeginData{
			Problem: "[remote] is enabled but no listener came up, and the daemon recorded no reason — " +
				"it may still be starting. The daemon log has the detail.",
		}, nil
	}

	hosts, port := listenerDial(srv.Addrs())
	hasAddrs := len(hosts) > 0 && port > 0

	ticket, err := d.pairer.Begin()
	if err != nil {
		return protocol.PairBeginData{}, fmt.Errorf("pairBegin: %w", err)
	}
	// nil = no relay in this payload (not eligible, or the hub refused).
	qr := d.relayPairOffer(ctx, ticket)
	if qr != nil && !strings.HasPrefix(qr.URL, "https://") {
		// A loopback development hub (`lola-hub dev`) may be plain http, but a
		// QR's relay must be https on both ends (EncodePairPayload, the phone's
		// PairPayload). Keeping it would refuse the whole pairBegin, LAN path
		// included, so this window is LAN-only; a paired phone learns the hub
		// through relayCred.
		d.pairer.SetWindowRelay(ticket.ID, "", "")
		d.hubRelayDirty()
		qr = nil
	}

	problem := ""
	switch {
	case !hasAddrs && qr == nil:
		if len(srv.Addrs()) == 0 {
			problem = "The phone server has no listener and no hub relay is available. Join a hub, or set [remote].bind to accept phones on the network."
		} else {
			problem = "The listener is up but reported no address to connect to."
		}
	case hasAddrs && qr == nil && !a.AllowLoopback && !anyNonLoopback(hosts):
		problem = `The listener only accepts this Mac; set [remote].bind = "lan" or join a hub.`
	}
	if problem != "" {
		_, _ = d.pairer.Reject(ticket.ID)
		return protocol.PairBeginData{Problem: problem}, nil
	}

	payload := remote.PairPayload{
		V:      remote.PairPayloadVersion,
		Pin:    srv.SPKIPin(),
		Secret: base64.RawURLEncoding.EncodeToString(ticket.Secret),
		Relay:  qr,
	}
	if hasAddrs {
		payload.Addrs = hosts
		if len(payload.Addrs) > maxQRAddrs {
			payload.Addrs = payload.Addrs[:maxQRAddrs] // routable ones come first
		}
		payload.Port = port
	}
	encoded, err := remote.EncodePairPayload(payload)
	if err != nil {
		// EncodePairPayload never puts a field value in its error.
		_, _ = d.pairer.Reject(ticket.ID)
		return protocol.PairBeginData{}, fmt.Errorf("pairBegin: %w", err)
	}
	return protocol.PairBeginData{
		Pairing:   ticket.ID,
		Payload:   encoded,
		ExpiresAt: ticket.ExpiresAt,
		Addrs:     payload.Addrs,
		Port:      payload.Port,
		Pin:       payload.Pin,
		Relay:     qr != nil,
	}, nil
}

// abandonReply undoes what a reply that never reached its client left behind.
// A pairBegin answer carries the only copy of the window's secret, so a window
// whose QR nobody received is closed at once rather than left open for its
// whole TTL, during which any certificate passes TLS.
func (d *Daemon) abandonReply(req protocol.Request, resp protocol.Response) {
	if req.Cmd != protocol.CmdPairBegin || !resp.OK || d.pairer == nil {
		return
	}
	var data protocol.PairBeginData
	if json.Unmarshal(resp.Data, &data) != nil || data.Pairing == "" {
		return
	}
	if _, err := d.pairer.Reject(data.Pairing); err == nil {
		d.remoteLogf("devices: pairing %s closed: its QR never reached the client", data.Pairing)
	}
}

// handlePairStatus reports a pairing; with Since and WaitMs it long-polls.
func (d *Daemon) handlePairStatus(ctx context.Context, a protocol.PairStatusArgs) (protocol.PairStatusData, error) {
	return d.pairer.Status(ctx, a)
}

// handlePairConfirm checks the code the human typed on this machine and
// registers the phone. The relay credential is minted fresh here (never the
// QR window's rv); after a successful Confirm the registry's subscriber
// (devicesChanged) tells the relay loop, so nothing else is called.
func (d *Daemon) handlePairConfirm(a protocol.PairConfirmArgs) (protocol.PairStatusData, error) {
	st, err := d.pairer.Confirm(a, d.relayMint())
	if err != nil {
		return protocol.PairStatusData{}, err
	}
	if st.State == protocol.PairStatePaired && st.Device != nil {
		d.remoteLogf("devices: paired device=%s name=%q tiers=%s", st.Device.ID, st.Device.Name, strings.Join(st.Device.Tiers, ","))
	}
	return st, nil
}

// handlePairReject cancels a waiting pairing or rejects a confirming one.
func (d *Daemon) handlePairReject(a protocol.PairRejectArgs) (protocol.PairStatusData, error) {
	return d.pairer.Reject(a.Pairing)
}

// handleDevices lists the paired phones with their live connection counts.
func (d *Daemon) handleDevices() protocol.DevicesData {
	d.remoteMu.Lock()
	srv := d.remote
	d.remoteMu.Unlock()

	out := protocol.DevicesData{Devices: []protocol.DeviceInfo{}}
	if err := d.devices.Err(); err != nil {
		// A broken registry lists nothing and says why (the error names the file).
		out.Problem = err.Error()
		return out
	}
	for _, dev := range d.devices.List() {
		conns := 0
		if srv != nil {
			conns = srv.DeviceConns(dev.ID)
		}
		out.Devices = append(out.Devices, deviceInfoFor(dev, conns))
	}
	return out
}

// handleRevokeDevice removes a phone and ends its live connections.
//
// The connections are dropped AFTER the registry change so a reconnect cannot
// succeed in between, and even when the revocation could only be journaled or
// kept in memory: the phone is gone for this run either way. A memory-only
// revocation is reported as an error so a human knows a restart would forget it.
func (d *Daemon) handleRevokeDevice(a protocol.RevokeDeviceArgs) (protocol.RevokeDeviceData, error) {
	dev, err := d.devices.Revoke(a.ID)
	if dev.ID == "" {
		if errors.Is(err, devices.ErrUnknownDevice) {
			return protocol.RevokeDeviceData{}, errUnknownDevice
		}
		return protocol.RevokeDeviceData{}, fmt.Errorf("revoke: %w", err)
	}
	closed := d.dropRemoteDevice(dev.ID)
	saved := "file"
	switch {
	case errors.Is(err, devices.ErrRevokeUnsaved):
		saved = "memory"
	case err != nil:
		saved = "journal"
	}
	d.remoteLogf("devices: revoke device=%s name=%q closed=%d saved=%s", dev.ID, dev.Name, closed, saved)
	if saved == "memory" {
		return protocol.RevokeDeviceData{ID: dev.ID, Closed: closed}, err
	}
	return protocol.RevokeDeviceData{ID: dev.ID, Closed: closed}, nil
}

// handleSetDeviceTiers replaces a phone's tiers and closes its connections:
// tiers come only from the `ready` frame, so a reconnect is the one way a
// change reaches the phone.
func (d *Daemon) handleSetDeviceTiers(a protocol.SetDeviceTiersArgs) (protocol.DeviceInfo, error) {
	dev, err := d.devices.SetTiers(a.ID, a.Tiers)
	if err != nil {
		if errors.Is(err, devices.ErrUnknownDevice) {
			return protocol.DeviceInfo{}, errUnknownDevice
		}
		return protocol.DeviceInfo{}, err
	}
	d.dropRemoteDevice(dev.ID)
	d.remoteLogf("devices: tiers device=%s tiers=%s", dev.ID, strings.Join(dev.Tiers, ","))
	d.remoteMu.Lock()
	srv := d.remote
	d.remoteMu.Unlock()
	conns := 0
	if srv != nil {
		conns = srv.DeviceConns(dev.ID)
	}
	return deviceInfoFor(dev, conns), nil
}

// deviceInfoFor renders a registry entry for the wire. The fingerprint is the
// first 8 bytes of the SPKI hash in 4-character groups, the string both the
// phone and the desktop show for a human to compare.
func deviceInfoFor(dev devices.Device, conns int) protocol.DeviceInfo {
	var groups []string
	if raw, err := base64.StdEncoding.DecodeString(dev.SPKI); err == nil && len(raw) >= 8 {
		hx := hex.EncodeToString(raw[:8])
		for i := 0; i+4 <= len(hx); i += 4 {
			groups = append(groups, hx[i:i+4])
		}
	}
	return protocol.DeviceInfo{
		ID:          dev.ID,
		Name:        dev.Name,
		Platform:    dev.Platform,
		Tiers:       slices.Clone(dev.Tiers),
		PairedAt:    dev.PairedAt,
		LastSeenAt:  dev.LastSeenAt,
		Connected:   conns,
		Fingerprint: strings.Join(groups, " "),
	}
}

func anyNonLoopback(hosts []string) bool {
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil && !ip.IsLoopback() {
			return true
		}
	}
	return false
}

// listenerDial reduces the bound addresses to the host halves a client dials —
// without duplicates — and the port they were taken on.
//
// It reads what was BOUND rather than what was configured, so an address the
// daemon cannot deliver is never offered to a phone. ORDER: routable addresses
// first (ReachableHosts order for a wildcard bind, then explicit binds in bind
// order), loopback last (kept for the Simulator, which shares the Mac's
// loopback), so a payload cut at maxQRAddrs loses the least useful ones.
//
// A WILDCARD is replaced by the machine's own reachable addresses: "::" scans
// perfectly and then times out, the worst failure shape because it looks like
// every other network problem. An address that cannot be split, and a
// zone-scoped one (the zone names an interface on THIS machine), is dropped
// rather than passed through. The port comes from the FIRST address that
// parsed: every listener of one Server binds the one port.
func listenerDial(addrs []remote.BindAddr) ([]string, int) {
	var routable, loopback []string
	seen := map[string]bool{}
	port := 0
	add := func(host string) {
		if host == "" || seen[host] {
			return
		}
		seen[host] = true
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			loopback = append(loopback, host)
			return
		}
		routable = append(routable, host)
	}
	for _, ba := range addrs {
		host, p, err := net.SplitHostPort(ba.Addr)
		if err != nil || host == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			continue
		}
		if port == 0 {
			port = n
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			for _, h := range remote.ReachableHosts() {
				add(h)
			}
			add("127.0.0.1")
			continue
		}
		if strings.Contains(host, "%") {
			continue
		}
		add(host)
	}
	return append(routable, loopback...), port
}
