// Package shareterm is the desktop's remote terminal backend: a teammate's shared
// session streamed into the same terminal widget as a local one. It is plain Go
// (no Wails) so it is built and tested everywhere; desktop/termsvc.go is the
// thin glue that routes `share-<id>` stream names here.
package shareterm

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/shareclient"
)

// Remote terminals (docs/team-sessions.md). A teammate's shared session shows in
// the same LiveTerminal as a local one: TermService routes any stream whose name
// starts with Prefix here, and the frontend listens on the same
// `pty:<name>` events. What differs is the far end: a mutual-TLS stream through
// the hub relay to the owner's daemon, pinned to the key this person trusted.
//
// Rules this file keeps:
//   - The owner's key comes ONLY from owners.json, the file a person's explicit
//     "trust" writes. The hub's listing names the owner and the share; it never
//     supplies a key to trust.
//   - Nothing here types except what the frontend sends for the focused
//     terminal. The owner decides whether the keystrokes land (share level).
//   - A stream ends when its connection does; there is no automatic reconnect
//     that could quietly re-attach to a different share.

const (
	// Prefix marks a terminal stream name as a remote share.
	Prefix = "share-"
	// shareDialTimeout bounds relay dial, handshake and the first roster.
	shareDialTimeout = 20 * time.Second
)

// IsStream reports whether name is a remote share's stream.
func IsStream(name string) bool { return strings.HasPrefix(name, Prefix) }

func shareIDOf(name string) string { return strings.TrimPrefix(name, Prefix) }

// remoteDial is the seam over everything between "attach to this share" and a
// connected shareclient.Conn (tests replace it).
type remoteDial func(ctx context.Context, shareID string) (*dialed, error)

// dialed is a connected share and the check that its owner is still trusted.
type dialed struct {
	conn *shareclient.Conn
	// trusted reports whether the owner's pin still stands (nil = always). It is
	// asked while the stream lives, so removing the pin ends the attachment.
	trusted func() bool
}

// remoteStream is one attached remote terminal.
type remoteStream struct {
	conn    *shareclient.Conn
	pane    string
	trusted func() bool

	mu      sync.Mutex
	pending []byte
	closed  bool
	info    protocol.SharedSession
	done    chan struct{}
	once    sync.Once
	// detached: the teardown was the frontend's (Detach), so no exit event.
	detached bool
}

type Terms struct {
	dial        remoteDial
	rosterEvery time.Duration
	emit        func(event string, data any)
	offers      func() (protocol.ShareOfferedData, error)

	mu      sync.Mutex
	streams map[string]*remoteStream
}

// Options wire a Terms to its host.
type Options struct {
	// Emit delivers an event to the frontend (Wails: app.Event.Emit).
	Emit func(event string, data any)
	// Offers asks the daemon which sessions teammates offer this machine (the
	// hub's listing through the daemon's validated client).
	Offers func() (protocol.ShareOfferedData, error)
	// Dial replaces the whole connection step (tests only).
	Dial func(ctx context.Context, shareID string) (*shareclient.Conn, func() bool, error)
	// RosterEvery is how often an attached terminal refreshes who is attached
	// (0 = 3s).
	RosterEvery time.Duration
}

// New returns a ready Terms.
func New(o Options) *Terms {
	r := &Terms{rosterEvery: o.RosterEvery, emit: o.Emit, offers: o.Offers, streams: map[string]*remoteStream{}}
	if o.Dial != nil {
		r.dial = func(ctx context.Context, id string) (*dialed, error) {
			c, t, err := o.Dial(ctx, id)
			if err != nil {
				return nil, err
			}
			return &dialed{conn: c, trusted: t}, nil
		}
	} else {
		r.dial = r.dialShare
	}
	return r
}

// Attach connects to the share and starts streaming. It is exclusive per name:
// an older stream for the same share is closed first.
func (r *Terms) Attach(name string, cols, rows int) (string, error) {
	if r.emit == nil {
		return "", errors.New("terminal service not wired to app")
	}
	shareID := shareIDOf(name)
	if !validShareID(shareID) {
		return "", errors.New("invalid share")
	}
	_ = r.Detach(name)
	ctx, cancel := context.WithTimeout(context.Background(), shareDialTimeout)
	defer cancel()
	dl, err := r.dial(ctx, shareID)
	if err != nil {
		return "", err
	}
	conn := dl.conn
	roster, err := conn.Roster(ctx)
	if err != nil {
		conn.Close()
		return "", err
	}
	var info *protocol.SharedSession
	for i := range roster.Sessions {
		if roster.Sessions[i].ID == shareID {
			info = &roster.Sessions[i]
		}
	}
	if info == nil {
		conn.Close()
		return "", errors.New("this session is no longer shared with you")
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	if err := conn.Subscribe(info.Pane, cols, rows); err != nil {
		conn.Close()
		return "", err
	}
	s := &remoteStream{conn: conn, pane: info.Pane, info: *info, done: make(chan struct{}), trusted: dl.trusted}
	r.mu.Lock()
	r.streams[name] = s
	r.mu.Unlock()
	go r.pump(name, s)
	go r.flush(name, s)
	go r.pollRoster(name, s, shareID)
	return name, nil
}

func validShareID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (r *Terms) get(name string) *remoteStream {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.streams[name]
}

// pump turns the connection's events into terminal bytes.
func (r *Terms) pump(name string, s *remoteStream) {
	defer r.end(name, s)
	for ev := range s.conn.Events() {
		switch ev.Kind {
		case shareclient.Resync:
			// The owner's grid, not ours: the host attaches at its own window size
			// and never resizes for a viewer, so the terminal must adopt that size
			// or lines wrap and absolute cursor moves land on the wrong cells.
			if ev.Screen.Cols > 0 && ev.Screen.Rows > 0 {
				r.emit("ptysize:"+name, Size{Cols: ev.Screen.Cols, Rows: ev.Screen.Rows})
			}
			s.push(shareclient.Render(ev.Screen))
		case shareclient.Output:
			s.push(ev.Data)
		case shareclient.Exit:
			return
		case shareclient.Refused:
			if ev.Pane == s.pane && ev.Code == protocol.CodeUnknownPane {
				return // unshared, or the session is gone
			}
		}
	}
}

func (s *remoteStream) push(b []byte) {
	s.mu.Lock()
	s.pending = append(s.pending, b...)
	s.mu.Unlock()
}

// flush emits at most one event per ~16ms, like a local terminal.
func (r *Terms) flush(name string, s *remoteStream) {
	tick := time.NewTicker(16 * time.Millisecond)
	defer tick.Stop()
	evt := "pty:" + name
	for {
		select {
		case <-s.done:
			return
		case <-tick.C:
		}
		s.mu.Lock()
		chunk := s.pending
		s.pending = nil
		s.mu.Unlock()
		if len(chunk) > 0 {
			r.emit(evt, base64.StdEncoding.EncodeToString(chunk))
		}
	}
}

// pollRoster keeps who is attached current (the owner's daemon is the source).
func (r *Terms) pollRoster(name string, s *remoteStream, shareID string) {
	every := r.rosterEvery
	if every <= 0 {
		every = 3 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
		}
		// Trust is re-read each tick: removing the owner's pin ends the attachment.
		if s.trusted != nil && !s.trusted() {
			s.conn.Close()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), max(every, time.Second))
		roster, err := s.conn.Roster(ctx)
		cancel()
		if err != nil {
			continue
		}
		for _, ss := range roster.Sessions {
			if ss.ID != shareID {
				continue
			}
			s.mu.Lock()
			s.info = ss
			s.mu.Unlock()
			r.emit("share:"+name, ss)
		}
	}
}

// end is the stream's teardown: drop it and tell the frontend unless the
// frontend asked for it.
func (r *Terms) end(name string, s *remoteStream) {
	r.mu.Lock()
	if r.streams[name] == s {
		delete(r.streams, name)
	}
	r.mu.Unlock()
	s.mu.Lock()
	intentional := s.detached
	s.mu.Unlock()
	s.once.Do(func() { close(s.done) })
	s.conn.Close()
	if !intentional && r.emit != nil {
		r.emit("pty:"+name+":exit", "")
	}
}

func (r *Terms) Write(name, data string) error {
	s := r.get(name)
	if s == nil {
		return nil
	}
	return s.conn.Write(s.pane, []byte(data))
}

func (r *Terms) Resize(name string, cols, rows int) error {
	s := r.get(name)
	if s == nil || cols <= 0 || rows <= 0 {
		return nil
	}
	return s.conn.Resize(s.pane, cols, rows)
}

func (r *Terms) Scroll(name string, lines int) error {
	s := r.get(name)
	if s == nil {
		return nil
	}
	return s.conn.Scroll(s.pane, lines)
}

func (r *Terms) Detach(name string) error {
	r.mu.Lock()
	s := r.streams[name]
	delete(r.streams, name)
	r.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.detached = true
	s.mu.Unlock()
	s.once.Do(func() { close(s.done) })
	s.conn.Close()
	return nil
}

func (r *Terms) DetachAll() {
	r.mu.Lock()
	names := make([]string, 0, len(r.streams))
	for n := range r.streams {
		names = append(names, n)
	}
	r.mu.Unlock()
	for _, n := range names {
		_ = r.Detach(n)
	}
}

// Session returns the latest roster row of an attached stream.
func (r *Terms) Session(name string) (protocol.SharedSession, bool) {
	s := r.get(name)
	if s == nil {
		return protocol.SharedSession{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info, true
}

// ---- the real dial -------------------------------------------------------------

// dialShare connects to the owner of shareID: it asks the daemon which share
// this is (the hub's listing, through the daemon's validated client), looks the
// owner's key up in owners.json, dials the hub's member leg and runs the pinned
// TLS handshake over it.
func (r *Terms) dialShare(ctx context.Context, shareID string) (*dialed, error) {
	if r.offers == nil {
		return nil, errors.New("not connected to the daemon")
	}
	offered, err := r.offers()
	if err != nil {
		return nil, err
	}
	var offer *protocol.ShareOffer
	for i := range offered.Offers {
		if offered.Offers[i].ID == shareID {
			offer = &offered.Offers[i]
		}
	}
	switch {
	case offer == nil:
		return nil, errors.New("this session is no longer shared with you")
	case !offer.Online:
		return nil, fmt.Errorf("%s is offline", offer.OwnerName)
	case !offer.Trusted:
		return nil, fmt.Errorf("trust %s before attaching to their sessions", offer.OwnerName)
	}
	home, err := config.Home()
	if err != nil {
		return nil, err
	}
	hostSPKI, err := ownerSPKI(home, offer.OwnerDevice)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, "state")
	id, err := hub.LoadIdentity(dir)
	if err != nil {
		return nil, errors.New("join a team hub first")
	}
	key, err := hub.LoadKey(dir)
	if err != nil || key.DeviceID() != id.DeviceID {
		return nil, errors.New("this machine's hub key is not the one that enrolled")
	}
	dk, err := remote.LoadOrCreateDeviceKey(home, nil)
	if err != nil {
		return nil, err
	}
	rc := &hub.RelayClient{BaseURL: id.URL, Key: key}
	ws, err := rc.DialMember(ctx, shareID)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s through the hub", offer.OwnerName)
	}
	conn, err := shareclient.Dial(ctx, ws, shareclient.Config{Cert: dk.Certificate, HostSPKI: hostSPKI})
	if err != nil {
		_ = ws.CloseNow(1000)
		if errors.Is(err, shareclient.ErrPinMismatch) {
			return nil, fmt.Errorf("%s's key is not the one you trusted; remove and trust them again", offer.OwnerName)
		}
		if errors.Is(err, shareclient.ErrNotReady) {
			return nil, fmt.Errorf("%s has not allowed this machine to attach", offer.OwnerName)
		}
		return nil, err
	}
	device := offer.OwnerDevice
	return &dialed{conn: conn, trusted: func() bool {
		cur, err := ownerSPKI(home, device)
		return err == nil && cur == hostSPKI
	}}, nil
}

// ownerSPKI reads the pinned key of the owner's hub device from owners.json.
func ownerSPKI(home, hubDevice string) ([32]byte, error) {
	var out [32]byte
	reg := devices.OpenOwners(home, time.Now, nil)
	if reg.Err() != nil {
		return out, errors.New("the list of trusted teammates is unreadable")
	}
	for _, d := range reg.List() {
		if d.Hub == nil || d.Hub.DeviceID != hubDevice {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(d.SPKI)
		if err != nil || len(raw) != sha256.Size {
			return out, errors.New("the trusted key is malformed")
		}
		copy(out[:], raw)
		return out, nil
	}
	return out, errors.New("that teammate is not trusted yet")
}

// Size is the owner's pane grid, pushed on `ptysize:<name>` with each screen.
type Size struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}
