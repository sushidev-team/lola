package main

import (
	"errors"
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
	once sync.Once
	srv  *preview.Server
	// devURLs answers a session's derived dev URLs; a seam for tests.
	devURLs func(session string) ([]string, error)
}

func (s *PreviewService) init() {
	s.once.Do(func() {
		s.srv = preview.New()
		if s.devURLs == nil {
			s.devURLs = sessionDevURLs
		}
	})
}

// sessionDevURLs reads the dev URLs the daemon holds for one session.
func sessionDevURLs(id string) ([]string, error) {
	var d protocol.SessionsData
	if err := call(protocol.Request{Cmd: "sessions"}, shortTimeout, &d); err != nil {
		return nil, err
	}
	for _, si := range d.Sessions {
		if si.ID == id {
			return si.DevURLs, nil
		}
	}
	return nil, errors.New("unknown session " + id)
}

// Open returns the address the pane frames for one of the session's dev URLs.
func (s *PreviewService) Open(session, url string) (string, error) {
	s.init()
	url = strings.TrimSpace(url)
	if _, err := preview.Allowed(url); err != nil {
		return "", err
	}
	urls, err := s.devURLs(session)
	if err != nil {
		return "", err
	}
	for _, u := range urls {
		if u == url {
			return s.srv.Open(url)
		}
	}
	return "", errors.New("that address is not one of this session's dev servers")
}

// ServiceShutdown stops every preview listener when the app quits.
func (s *PreviewService) ServiceShutdown() error {
	s.init()
	s.srv.Close()
	return nil
}
