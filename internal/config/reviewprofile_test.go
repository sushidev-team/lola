package config

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func TestReviewProfileRoundTripAndProviderIndependence(t *testing.T) {
	c := catalogBase()
	c.ReviewProviders = []ReviewProvider{{Provider: provCodexSession, Enabled: true}}
	c.ReviewProfiles = []reviewprofile.Profile{
		{ID: "backend", Kind: "backend", Provider: "codex-session", Version: "1", Enabled: true, Required: true},
		{ID: "uiux", Kind: "uiux", Provider: "codex-session", Version: "2", Enabled: true, Paths: []string{"frontend"}},
	}
	if errs := c.validateReviewProfiles(); len(errs) != 0 {
		t.Fatal(errs)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.ReviewProfiles, loaded.ReviewProfiles) {
		t.Fatalf("profile round trip: %+v", loaded.ReviewProfiles)
	}
	if loaded.Review != (ReviewConfig{}) {
		t.Fatal("profiles materialized legacy review settings")
	}
	c.ReviewProfiles[1].ID = "backend"
	if len(c.validateReviewProfiles()) == 0 {
		t.Fatal("duplicate profile ID accepted")
	}
	c.ReviewProfiles[1].ID = "uiux"
	c.ReviewProfiles[0].Provider = "unknown"
	if len(c.validateReviewProfiles()) == 0 {
		t.Fatal("unknown provider silently defaulted")
	}
}
