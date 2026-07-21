package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in                  string
		wantErr             bool
		major, minor, patch int
		prerelease          string
	}{
		{in: "1.2.3", major: 1, minor: 2, patch: 3},
		{in: "v1.2.3", major: 1, minor: 2, patch: 3},
		{in: "V2.0", major: 2, minor: 0, patch: 0},
		{in: "1.2.3-beta.1", major: 1, minor: 2, patch: 3, prerelease: "beta.1"},
		{in: "dev", wantErr: true},
		{in: "1", wantErr: true},
		{in: "1.2.3.4", wantErr: true},
		{in: "a.b.c", wantErr: true},
	}
	for _, c := range cases {
		v, err := ParseVersion(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseVersion(%q) = %+v, want error", c.in, v)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseVersion(%q) error: %v", c.in, err)
			continue
		}
		if v.Major != c.major || v.Minor != c.minor || v.Patch != c.patch || v.Prerelease != c.prerelease {
			t.Errorf("ParseVersion(%q) = %d.%d.%d-%q, want %d.%d.%d-%q",
				c.in, v.Major, v.Minor, v.Patch, v.Prerelease, c.major, c.minor, c.patch, c.prerelease)
		}
	}
}

func TestCompareOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.9.9", 1},
		// A release outranks the same numbers with a prerelease.
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-rc.2", "1.0.0-rc.1", 1},
	}
	for _, c := range cases {
		a, err := ParseVersion(c.a)
		if err != nil {
			t.Fatalf("parse %q: %v", c.a, err)
		}
		b, err := ParseVersion(c.b)
		if err != nil {
			t.Fatalf("parse %q: %v", c.b, err)
		}
		if got := a.Compare(b); got != c.want {
			t.Errorf("Compare(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsNewerThan(t *testing.T) {
	newer, err := IsNewerThan("1.2.0", "1.3.0")
	if err != nil || !newer {
		t.Errorf("IsNewerThan(1.2.0,1.3.0) = %v,%v want true,nil", newer, err)
	}
	newer, err = IsNewerThan("1.3.0", "1.3.0")
	if err != nil || newer {
		t.Errorf("IsNewerThan(equal) = %v,%v want false,nil", newer, err)
	}
	if _, err := IsNewerThan("dev", "1.0.0"); err == nil {
		t.Error("IsNewerThan(dev,...) want error (non-semver current)")
	}
}

func TestIsValidVersion(t *testing.T) {
	for _, v := range []string{"1.0.0", "v1.0.0", "1.2", "1.0.0-beta.1"} {
		if !IsValidVersion(v) {
			t.Errorf("IsValidVersion(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"dev", "", "1", "latest"} {
		if IsValidVersion(v) {
			t.Errorf("IsValidVersion(%q) = true, want false", v)
		}
	}
}

func TestPickDMG(t *testing.T) {
	arch := GetArchitecture() // e.g. "apple-silicon" on this runner

	// Universal-only: the single universal DMG wins (the shipping case).
	if a := pickDMG([]Asset{{Name: "lola-desktop-1.0.0-universal.dmg"}}); a == nil || a.Name != "lola-desktop-1.0.0-universal.dmg" {
		t.Errorf("pickDMG universal-only = %v, want the universal dmg", a)
	}

	// Arch-specific beats universal for this machine.
	archAsset := "lola-desktop-1.0.0-" + arch + ".dmg"
	got := pickDMG([]Asset{{Name: "lola-desktop-1.0.0-universal.dmg"}, {Name: archAsset}})
	if got == nil || got.Name != archAsset {
		t.Errorf("pickDMG arch preference = %v, want %s", got, archAsset)
	}

	// A lone DMG with no arch/universal token is still accepted.
	if a := pickDMG([]Asset{{Name: "lola-desktop.dmg"}}); a == nil {
		t.Error("pickDMG any-dmg = nil, want the lone dmg")
	}

	// No DMG at all → nil (a checksums file / zip must never be offered).
	if a := pickDMG([]Asset{{Name: "checksums.txt"}, {Name: "src.zip"}}); a != nil {
		t.Errorf("pickDMG no-dmg = %v, want nil", a)
	}
}

func TestPrefsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desktop-update.json")

	// Missing file → defaults.
	if p := LoadPrefs(path); p != DefaultPrefs() {
		t.Errorf("LoadPrefs(missing) = %+v, want defaults %+v", p, DefaultPrefs())
	}

	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	want := Prefs{AutoCheck: false, LastCheckTime: now, SkippedVersion: "1.4.0", CheckIntervalHours: 12}
	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := LoadPrefs(path)
	if !got.LastCheckTime.Equal(want.LastCheckTime) || got.AutoCheck != want.AutoCheck ||
		got.SkippedVersion != want.SkippedVersion || got.CheckIntervalHours != want.CheckIntervalHours {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}

	// A zero/negative interval normalises to 24h on load.
	bad := Prefs{AutoCheck: true, CheckIntervalHours: 0}
	if err := bad.Save(path); err != nil {
		t.Fatalf("Save bad: %v", err)
	}
	if p := LoadPrefs(path); p.CheckIntervalHours != 24 {
		t.Errorf("LoadPrefs normalised interval = %d, want 24", p.CheckIntervalHours)
	}

	// A corrupt file degrades to defaults rather than erroring.
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := LoadPrefs(path); p != DefaultPrefs() {
		t.Errorf("LoadPrefs(corrupt) = %+v, want defaults", p)
	}
}

func TestShouldAutoCheck(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	if (Prefs{AutoCheck: false, CheckIntervalHours: 24}).ShouldAutoCheck(now) {
		t.Error("AutoCheck off should never check")
	}
	if !(Prefs{AutoCheck: true, CheckIntervalHours: 24}).ShouldAutoCheck(now) {
		t.Error("zero LastCheckTime should check")
	}
	recent := Prefs{AutoCheck: true, CheckIntervalHours: 24, LastCheckTime: now.Add(-1 * time.Hour)}
	if recent.ShouldAutoCheck(now) {
		t.Error("checked an hour ago (24h interval) should NOT check")
	}
	stale := Prefs{AutoCheck: true, CheckIntervalHours: 24, LastCheckTime: now.Add(-25 * time.Hour)}
	if !stale.ShouldAutoCheck(now) {
		t.Error("checked 25h ago (24h interval) should check")
	}
}
