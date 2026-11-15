package main

import (
	"encoding/json"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// PhonesService is the Wails-bound bridge for pairing and managing phones. It is
// a thin client of the daemon socket and holds no state: the daemon owns the
// pairing window, the registry and the live connections.
//
// Nothing here ever shows or derives the confirmation code. The human reads it
// on the phone and types it into Confirm. The QR payload returned by Begin is a
// secret until the pairing burns it, so no method logs or persists it, and Wails
// logs a bound call's arguments at debug level, so desktop/main.go must not
// lower its log level. AllowLoopback is deliberately not exposed: it is for the
// CLI and development only.
type PhonesService struct{}

// phoneCallTimeout is the budget of every call that reads daemon memory or the
// device registry and execs nothing.
const phoneCallTimeout = 5 * time.Second

// pairWaitSlack is added to a long-poll's WaitMs so the socket outlives the
// daemon's own wait.
const pairWaitSlack = 5 * time.Second

// pairBeginTimeout covers the daemon's wait for the hub to register the
// window's relay rendezvous, after which it answers with a LAN-only QR: a
// shorter budget would turn an unhealthy hub into a failed pairing.
const pairBeginTimeout = protocol.PairBeginRelayWait + pairWaitSlack

// Begin opens a pairing window and returns the QR payload. A Problem is a state
// the tab renders (listener off, nothing bound), not an error.
func (s *PhonesService) Begin() (protocol.PairBeginData, error) {
	var d protocol.PairBeginData
	err := call(protocol.Request{Cmd: protocol.CmdPairBegin}, pairBeginTimeout, &d)
	return d, err
}

// Status reads one pairing's state. With WaitMs it blocks until the state
// differs from Since, so the timeout covers the wait.
func (s *PhonesService) Status(a protocol.PairStatusArgs) (protocol.PairStatusData, error) {
	var d protocol.PairStatusData
	wait := time.Duration(max(a.WaitMs, 0)) * time.Millisecond
	err := phoneCall(protocol.CmdPairStatus, a, wait+pairWaitSlack, &d)
	return d, err
}

// Confirm accepts a waiting phone with the code the human TYPED from it.
func (s *PhonesService) Confirm(a protocol.PairConfirmArgs) (protocol.PairStatusData, error) {
	var d protocol.PairStatusData
	err := phoneCall(protocol.CmdPairConfirm, a, phoneCallTimeout, &d)
	return d, err
}

// Reject refuses a waiting phone, or cancels a pairing nobody has joined yet.
func (s *PhonesService) Reject(pairing string) (protocol.PairStatusData, error) {
	var d protocol.PairStatusData
	err := phoneCall(protocol.CmdPairReject, protocol.PairRejectArgs{Pairing: pairing}, phoneCallTimeout, &d)
	return d, err
}

// Devices lists the paired phones. A Problem names a broken registry file.
func (s *PhonesService) Devices() (protocol.DevicesData, error) {
	var d protocol.DevicesData
	err := call(protocol.Request{Cmd: protocol.CmdDevices}, phoneCallTimeout, &d)
	return d, err
}

// Revoke removes a device and closes its live connections.
func (s *PhonesService) Revoke(id string) (protocol.RevokeDeviceData, error) {
	var d protocol.RevokeDeviceData
	err := phoneCall(protocol.CmdRevokeDevice, protocol.RevokeDeviceArgs{ID: id}, phoneCallTimeout, &d)
	return d, err
}

// SetTiers replaces a device's tiers; the daemon closes its connections so the
// new set applies at the next handshake.
func (s *PhonesService) SetTiers(id string, tiers []string) (protocol.DeviceInfo, error) {
	var d protocol.DeviceInfo
	err := phoneCall(protocol.CmdSetDeviceTiers, protocol.SetDeviceTiersArgs{ID: id, Tiers: tiers}, phoneCallTimeout, &d)
	return d, err
}

func phoneCall(cmd string, args any, timeout time.Duration, out any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return call(protocol.Request{Cmd: cmd, Args: raw}, timeout, out)
}
