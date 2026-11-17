package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeConflictMessageUpgrade(t *testing.T) {
	for _, tt := range []struct {
		name, message, want string
	}{
		{"old default", legacyMergeConflictMessage, DefaultMergeConflictMessage},
		{"new default", DefaultMergeConflictMessage, DefaultMergeConflictMessage},
		{"custom rebase", "Rebase onto develop and ask before pushing.", "Rebase onto develop and ask before pushing."},
		{"empty", "", ""},
	} {
		for _, auto := range []bool{false, true} {
			t.Run(tt.name+map[bool]string{false: "/disabled", true: "/enabled"}[auto], func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "config.toml")
				cfg := &Config{Reactions: defaultReactions()}
				cfg.Reactions.MergeConflict = Reaction{Auto: auto, Retries: 3, Message: tt.message}
				if err := cfg.Save(path); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				loaded, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				want := Reaction{Auto: auto, Retries: 3, Message: tt.want}
				if got := loaded.Reactions.MergeConflict; got != want {
					t.Fatalf("loaded reaction = %+v, want %+v", got, want)
				}
				// Loading upgrades the effective default without rewriting disk.
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(before) {
					t.Fatalf("Load changed the config file: %v", err)
				}
				if err := loaded.Save(path); err != nil {
					t.Fatal(err)
				}
				reloaded, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				if reloaded.Reactions != loaded.Reactions {
					t.Fatal("upgraded reactions changed on save/load")
				}
			})
		}
	}
}
