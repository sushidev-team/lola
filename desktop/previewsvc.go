package main

import (
	"errors"
	neturl "net/url"
	"slices"
	"strings"
	"sync"

	"github.com/sushidev-team/lola/internal/preview"
	"github.com/sushidev-team/lola/internal/protocol"
)

// PreviewService backs the session preview pane: it turns one of a session's
// own dev URLs into a loopback address the pane can frame and pick elements in
// (internal/preview). It never takes a URL on trust — the address must be one
// the daemon derived for THAT session, or the pane would be a proxy into any
// port on this Mac's loopback.
type PreviewService struct {
	// Order opens and the desktop's regular snapshot reads so a late response
	// cannot resurrect a listener after a newer snapshot retired its origin.
	mu       sync.Mutex
	srv      *preview.Server
	sessions func() (protocol.SessionsData, error)
}

// refreshSessions shares the existing desktop poll. Listeners stay stable for
// as long as their session owns the origin, including across parked review tabs.
func (s *PreviewService) refreshSessions() (protocol.SessionsData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

// snapshot is called with mu held. A failed read gives no evidence of removal.
func (s *PreviewService) snapshot() (protocol.SessionsData, error) {
	d, err := s.sessions()
	if err == nil {
		s.srv.Retain(func(id, origin string) bool { return ownsPreviewOrigin(d, id, origin) })
	}
	return d, err
}

func ownsPreviewOrigin(d protocol.SessionsData, id, origin string) bool {
	matches := func(raw string) bool { return sameOrigin(raw, origin) }
	for i := range d.Sessions {
		session := &d.Sessions[i]
		if session.ID != id {
			continue
		}
		if slices.ContainsFunc(session.DevURLs, matches) {
			return true
		}
		for _, app := range session.Apps {
			if slices.ContainsFunc(app.DevURLs, matches) {
				return true
			}
		}
		break
	}
	return false
}

// Open returns the address the pane frames for one of the session's dev URLs.
// The proxy's *.localhost hosts must be allowed by both macOS bundle plists.
func (s *PreviewService) Open(session, url string) (string, error) {
	url = strings.TrimSpace(url)
	if _, err := preview.Allowed(url); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.snapshot()
	if err != nil {
		return "", err
	}
	if ownsPreviewOrigin(d, session, url) {
		return s.srv.Open(session, url)
	}
	return "", errors.New("that address is not one of this session's dev servers")
}

// sameOrigin reports whether a page lives on a dev server the session owns. A
// review page names a path on it; the origin is what the daemon vouched for.
func sameOrigin(owned, asked string) bool {
	a, err := neturl.Parse(owned)
	if err != nil {
		return false
	}
	b, err := neturl.Parse(asked)
	if err != nil {
		return false
	}
	return a.Hostname() != "" && a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && effectivePort(a) == effectivePort(b)
}

// effectivePort is the explicit port, or the scheme's default when omitted.
func effectivePort(u *neturl.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// ServiceShutdown stops every preview listener when the app quits.
func (s *PreviewService) ServiceShutdown() error {
	s.srv.Close()
	return nil
}
