package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/config"
)

// DesktopContext is the small, display-only context shared with local time trackers.
// It describes the visible workspace, never agent activity or elapsed work time.
type DesktopContext struct {
	View        string `json:"view"`
	SessionID   string `json:"sessionId,omitempty"`
	Title       string `json:"title,omitempty"`
	ProjectID   string `json:"projectId,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	IssueKey    string `json:"issueKey,omitempty"`
}

type desktopContextRecord struct {
	DesktopContext
	Version   int       `json:"version"`
	PID       int       `json:"pid"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ContextService publishes one private snapshot, not a history or a listener.
// Consumers must match PID to the foreground Lola process before attributing time.
type ContextService struct {
	mu     sync.Mutex
	path   string
	closed bool
}

func (s *ContextService) Publish(current DesktopContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	home, err := config.Home()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(home, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(desktopContextRecord{current, 1, os.Getpid(), time.Now().UTC()})
	if err != nil {
		return err
	}
	// CreateTemp uses 0600; rename keeps readers from observing partial JSON.
	f, err := os.CreateTemp(home, ".desktop-context-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	path := filepath.Join(home, "desktop-context.json")
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	s.path = path
	return nil
}

func (s *ContextService) ServiceShutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var record desktopContextRecord
	if json.Unmarshal(data, &record) != nil || record.PID != os.Getpid() {
		return nil // Another app instance owns the latest snapshot.
	}
	err = os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
