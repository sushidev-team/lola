package sshstore

import (
	"errors"
	"sync/atomic"
	"testing"
)

func TestSetNotifyAfterUnlock(t *testing.T) {
	s, err := Open(t.TempDir(), "sess", true, nil)
	if err != nil || s == nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	s.SetNotify(func() {
		n.Add(1)
		s.View(func(*State) {}) // deadlocks if the lock were still held
	})

	if err := s.Update(func(*State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Fatalf("update: %d", n.Load())
	}
	s.Volatile(func(*State) bool { return false })
	if n.Load() != 1 {
		t.Fatalf("volatile false fired: %d", n.Load())
	}
	s.Volatile(func(*State) bool { return true })
	if n.Load() != 2 {
		t.Fatalf("volatile true: %d", n.Load())
	}
	// A failed fn commits nothing.
	_ = s.Update(func(*State) error { return errors.New("no") })
	if n.Load() != 2 {
		t.Fatalf("failed update fired: %d", n.Load())
	}
	s.SetNotify(nil)
	_ = s.Update(func(*State) error { return nil })
	if n.Load() != 2 {
		t.Fatal("cleared notify fired")
	}
}
