package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopContextSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	s := &ContextService{}
	path := filepath.Join(home, "desktop-context.json")
	read := func() desktopContextRecord {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var record desktopContextRecord
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	current := DesktopContext{View: "cockpit", SessionID: "donso-eng-123", Title: "Fix login", ProjectID: "donso", ProjectName: "Donso", IssueKey: "ENG-123"}
	if err := s.Publish(current); err != nil {
		t.Fatal(err)
	}
	record := read()
	if record.DesktopContext != current || record.PID != os.Getpid() || record.Version != 1 || record.UpdatedAt.IsZero() {
		t.Fatalf("unexpected context: %+v", record)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot must be private: %v, %v", info, err)
	}
	if err := s.Publish(DesktopContext{View: "settings"}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.SessionID != "" || got.IssueKey != "" || got.View != "settings" {
		t.Fatalf("stale session on settings: %+v", got)
	}
	files, _ := os.ReadDir(home)
	if len(files) != 1 {
		t.Fatalf("temporary files left behind: %v", files)
	}
	if err := s.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	// A queued frontend call during teardown must not resurrect the snapshot.
	if err := s.Publish(current); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot survived shutdown: %v", err)
	}
}

func TestDesktopContextShutdownPreservesOtherProcess(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	s := &ContextService{}
	if err := s.Publish(DesktopContext{View: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"version":1,"pid":-1,"view":"home"}`)
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(s.path)
	if err != nil || string(got) != string(data) {
		t.Fatalf("removed another process's context: %s, %v", got, err)
	}
}
