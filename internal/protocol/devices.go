package protocol

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"
)

// Phone pairing and the device registry: the wire contract. docs/mobile/01 is the
// spec. Every command here except CmdRemotePair travels on the 0600 unix socket
// only (LocalOnlyCommands): a phone can neither start a pairing nor change what a
// device may do.
const (
	CmdPairBegin      = "pairBegin"      // PairBeginArgs → PairBeginData
	CmdPairStatus     = "pairStatus"     // PairStatusArgs → PairStatusData
	CmdPairConfirm    = "pairConfirm"    // PairConfirmArgs → PairStatusData
	CmdPairReject     = "pairReject"     // PairRejectArgs → PairStatusData
	CmdDevices        = "devices"        // no args → DevicesData
	CmdRevokeDevice   = "revokeDevice"   // RevokeDeviceArgs → RevokeDeviceData
	CmdSetDeviceTiers = "setDeviceTiers" // SetDeviceTiersArgs → DeviceInfo

	// CmdRemotePair is the one frame an unpaired certificate may send (PairRequest).
	// internal/remote consumes it during authentication; it never reaches d.handle.
	CmdRemotePair = "remote.pair"
)

// PairBeginRelayWait is how long pairBegin may wait for the hub to register a
// new window's relay rendezvous before it answers without one. A client's
// budget for pairBegin must exceed it, or a slow hub turns a usable LAN-only
// QR into a client timeout.
const PairBeginRelayWait = 5 * time.Second

// LocalOnlyCommands are reachable from the unix socket and nowhere else: tier
// "never" in internal/remote, refused again by Daemon.handleRemote, and named by no
// hook-bridge allowlist.
var LocalOnlyCommands = []string{
	CmdPairBegin, CmdPairStatus, CmdPairConfirm, CmdPairReject,
	CmdDevices, CmdRevokeDevice, CmdSetDeviceTiers,
}

// Capability tiers. A device holds a subset of Tiers; TierNever is a
// classification for commands no device can hold, never a grantable tier.
const (
	TierRead        = "read"
	TierAnswer      = "answer"
	TierControl     = "control"
	TierDestructive = "destructive"
	TierShell       = "shell"
	TierNever       = "never"
)

// Tiers is the closed set a device may hold, in ascending reach.
var Tiers = []string{TierRead, TierAnswer, TierControl, TierDestructive, TierShell}

// DefaultTiers is what a new device gets when the confirmer names none.
var DefaultTiers = Tiers

// Pairing states. Terminal: paired, rejected, expired, failed, cancelled.
const (
	PairStateWaiting    = "waiting"
	PairStateConfirming = "confirming"
	PairStatePaired     = "paired"
	PairStateRejected   = "rejected"
	PairStateExpired    = "expired"
	PairStateFailed     = "failed"
	PairStateCancelled  = "cancelled"
)

// Reasons a pairing failed (PairStatusData.Reason, state failed only).
const (
	PairReasonTooManyAttempts = "too_many_attempts"
	PairReasonCodeMismatch    = "code_mismatch"
	PairReasonPhoneLeft       = "phone_left"
	PairReasonRegistry        = "registry"
	PairReasonFull            = "full"
)

// PairBeginArgs is Request.Args for cmd=pairBegin.
type PairBeginArgs struct {
	// AllowLoopback accepts a loopback-only QR (simulator and development).
	AllowLoopback bool `json:"allowLoopback,omitempty"`
}

// PairBeginData is Response.Data for cmd=pairBegin. Payload is a SECRET until the
// pairing burns it: it is never logged and never placed in an error. With a Problem
// nothing else is set.
type PairBeginData struct {
	Pairing   string    `json:"pairing"` // 16 hex; "" with Problem
	Payload   string    `json:"payload"` // "lola2.…"
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
	Addrs     []string  `json:"addrs,omitempty"` // as in the payload, for display
	Port      int       `json:"port,omitempty"`
	Pin       string    `json:"pin,omitempty"`   // daemon SPKI, std base64
	Relay     bool      `json:"relay,omitempty"` // the payload carries a relay rendezvous
	Problem   string    `json:"problem,omitempty"`
}

// PairStatusArgs is Request.Args for cmd=pairStatus. With WaitMs the call blocks
// until State != Since (clamped by the daemon).
type PairStatusArgs struct {
	Pairing string `json:"pairing"`
	Since   string `json:"since,omitempty"`
	WaitMs  int    `json:"waitMs,omitempty"`
}

// PairStatusData is one pairing's state. There is deliberately NO code field: the
// daemon never shows the confirmation code. The human reads it on the phone and
// types it (PairConfirmArgs.Code).
type PairStatusData struct {
	Pairing    string      `json:"pairing"`
	State      string      `json:"state"` // PairState*
	DeviceName string      `json:"deviceName,omitempty"`
	Platform   string      `json:"platform,omitempty"`
	NameClash  bool        `json:"nameClash,omitempty"` // DeviceName equals a paired device's name
	Replaces   string      `json:"replaces,omitempty"`  // id of the entry this key re-pairs
	TriesLeft  int         `json:"triesLeft,omitempty"`
	Via        string      `json:"via,omitempty"` // "lan" | "relay": the presenting connection's path
	Device     *DeviceInfo `json:"device,omitempty"`
	Reason     string      `json:"reason,omitempty"` // failed only: PairReason*
	ExpiresAt  time.Time   `json:"expiresAt,omitzero"`
}

// PairConfirmArgs is Request.Args for cmd=pairConfirm.
type PairConfirmArgs struct {
	Pairing string   `json:"pairing"`
	Code    string   `json:"code"`            // the 6 digits the human TYPED from the phone
	Tiers   []string `json:"tiers,omitempty"` // empty = DefaultTiers
	Name    string   `json:"name,omitempty"`
}

// PairRejectArgs is Request.Args for cmd=pairReject; it also cancels a waiting pairing.
type PairRejectArgs struct {
	Pairing string `json:"pairing"`
}

// DeviceInfo is one registry entry as a client may see it.
type DeviceInfo struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Platform    string    `json:"platform,omitempty"`
	Tiers       []string  `json:"tiers"`
	PairedAt    time.Time `json:"pairedAt"`
	LastSeenAt  time.Time `json:"lastSeenAt,omitzero"`
	Connected   int       `json:"connected"`   // live connections; 0 when the listener is down
	Fingerprint string    `json:"fingerprint"` // hex(SPKIHash[:8]) in 4-char groups
}

// DevicesData is Response.Data for cmd=devices.
type DevicesData struct {
	Devices []DeviceInfo `json:"devices"`
	Problem string       `json:"problem,omitempty"` // registry broken: names the file
}

// RevokeDeviceArgs is Request.Args for cmd=revokeDevice.
type RevokeDeviceArgs struct {
	ID string `json:"id"`
}

// RevokeDeviceData is Response.Data for cmd=revokeDevice.
type RevokeDeviceData struct {
	ID     string `json:"id"`
	Closed int    `json:"closed"` // live connections ended
}

// SetDeviceTiersArgs is Request.Args for cmd=setDeviceTiers (reply: DeviceInfo).
type SetDeviceTiersArgs struct {
	ID    string   `json:"id"`
	Tiers []string `json:"tiers"`
}

// PairRequest is the payload of the one frame an unpaired certificate may send
// (cmd=remote.pair).
type PairRequest struct {
	Proof    string `json:"proof"` // base64url(PairProof(secret, exporterProof))
	Name     string `json:"name"`
	Platform string `json:"platform,omitempty"`
}

// PairResult is the Response.Data of an accepted remote.pair.
type PairResult struct {
	DeviceID string     `json:"deviceId"`
	Name     string     `json:"name"`
	Tiers    []string   `json:"tiers"`
	Host     string     `json:"host"`            // display only
	Relay    *PairRelay `json:"relay,omitempty"` // nil when no relay credential was minted
}

// PairRelay is the device's own relay credential, delivered once inside the inner
// TLS. RV is fresh (never the QR's); the bearer is long-lived and the daemon keeps
// only its hash.
type PairRelay struct {
	RV     string `json:"rv"`
	Bearer string `json:"bearer"`
}

const (
	pairCodeLabel = "lola-pair-code-v1"
	pairCodeMod   = 1_000_000
)

// PairProof is the proof a phone presents: HMAC-SHA256(key = secret, msg = exporter),
// where exporter is the TLS exporter "EXPORTER-lola-pair-proof-v1" (32 bytes). Binding
// to the exporter makes a captured proof useless on any other TLS session. Callers
// compare with hmac.Equal.
func PairProof(secret, exporter []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write(exporter)
	return m.Sum(nil)
}

// PairCode is the six digits both sides compute and the human compares: the MAC,
// keyed by the TLS exporter "EXPORTER-lola-pair-code-v1", over a fixed label and both
// SPKI hashes, reduced to a decimal number.
func PairCode(exporterCode []byte, daemonSPKI, phoneSPKI [32]byte) string {
	m := hmac.New(sha256.New, exporterCode)
	m.Write([]byte(pairCodeLabel))
	m.Write(daemonSPKI[:])
	m.Write(phoneSPKI[:])
	mac := m.Sum(nil)
	return fmt.Sprintf("%06d", binary.BigEndian.Uint64(mac[:8])%pairCodeMod)
}
