package remote

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeviceKeyIsGeneratedOnceAndReused. The identity is what a phone pins, so
// silently minting a new one would break every enrolled device with no
// diagnosable symptom.
func TestDeviceKeyIsGeneratedOnceAndReused(t *testing.T) {
	dir := t.TempDir()

	first, err := LoadOrCreateDeviceKey(dir, nil)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if !first.Created {
		t.Error("the first call did not report generating the identity")
	}

	second, err := LoadOrCreateDeviceKey(dir, nil)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if second.Created {
		t.Error("the second call regenerated the identity")
	}
	if first.SPKIPin() != second.SPKIPin() {
		t.Errorf("the pin moved between loads: %s then %s", first.SPKIPin(), second.SPKIPin())
	}
	if first.SPKIPin() == "" {
		t.Error("the pin is empty; M2's QR carries it")
	}
}

// TestSPKIPinIsTheHashOfTheSubjectPublicKeyInfo, not of the certificate:
// hashing the SPKI is what lets the certificate be reissued over the same key
// without invalidating every pairing.
func TestSPKIPinIsTheHashOfTheSubjectPublicKeyInfo(t *testing.T) {
	dk, err := LoadOrCreateDeviceKey(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := sha256.Sum256(dk.Leaf.RawSubjectPublicKeyInfo)
	if got := dk.SPKISHA256(); got != want {
		t.Error("SPKISHA256 does not hash RawSubjectPublicKeyInfo")
	}
	if dk.SPKIPin() != base64.StdEncoding.EncodeToString(want[:]) {
		t.Errorf("SPKIPin %q is not the base64 of the hash", dk.SPKIPin())
	}
}

// TestDeviceKeyFilesArePrivate. The key is a secret and lives on disk rather
// than in the Keychain, because a launchd-started daemon cannot unlock the
// login keychain non-interactively.
func TestDeviceKeyFilesArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lola")
	dk, err := LoadOrCreateDeviceKey(dir, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode %04o, want 0700", got)
	}
	for _, p := range []string{dk.KeyPath, dk.CertPath} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode %04o, want 0600", filepath.Base(p), got)
		}
	}
	if filepath.Base(dk.KeyPath) != DeviceKeyFile || filepath.Base(dk.CertPath) != DeviceCertFile {
		t.Errorf("unexpected file names %s / %s", dk.KeyPath, dk.CertPath)
	}
}

// TestHalfAnIdentityFailsClosed. Regenerating over a missing half would be a
// silent identity change; the operator has to decide that, because it
// invalidates every paired device.
func TestHalfAnIdentityFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		remove string
	}{
		{"the key is gone", DeviceKeyFile},
		{"the certificate is gone", DeviceCertFile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := LoadOrCreateDeviceKey(dir, nil); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if err := os.Remove(filepath.Join(dir, tc.remove)); err != nil {
				t.Fatalf("remove: %v", err)
			}
			_, err := LoadOrCreateDeviceKey(dir, nil)
			if !errors.Is(err, ErrDeviceKeyIncomplete) {
				t.Fatalf("got %v, want ErrDeviceKeyIncomplete", err)
			}
			if _, statErr := os.Stat(filepath.Join(dir, tc.remove)); statErr == nil {
				t.Error("the missing half was regenerated instead of refused")
			}
		})
	}
}

// TestMismatchedKeyAndCertificateFailClosed catches the pair that parses but
// does not belong together — at load, rather than at the first handshake.
func TestMismatchedKeyAndCertificateFailClosed(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if _, err := LoadOrCreateDeviceKey(a, nil); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if _, err := LoadOrCreateDeviceKey(b, nil); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	other, err := os.ReadFile(filepath.Join(b, DeviceCertFile))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(filepath.Join(a, DeviceCertFile), other, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadOrCreateDeviceKey(a, nil); !errors.Is(err, ErrDeviceKeyIncomplete) {
		t.Fatalf("got %v, want ErrDeviceKeyIncomplete", err)
	}
}

// TestAnOverPermissiveDirectoryIsReportedNotSilentlyChanged: the operator's
// directory is not this package's to tighten, but the condition must be
// discoverable.
func TestAnOverPermissiveDirectoryIsReportedNotSilentlyChanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	var lines []string
	if _, err := LoadOrCreateDeviceKey(dir, func(f string, a ...any) { lines = append(lines, fmtSprintf(f, a...)) }); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "0755") {
		t.Errorf("the directory mode was not reported; log was %v", lines)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Error("the operator's directory was re-permissioned behind their back")
	}
}

// TestTLSConfigIsTLS13WithoutResumption. A replayed kill frame is on the threat
// list, so every connection gets an ephemeral handshake and there is no
// resumption to replay against.
func TestTLSConfigIsTLS13WithoutResumption(t *testing.T) {
	dk, err := LoadOrCreateDeviceKey(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg := dk.TLSConfig()
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion %#x, want TLS 1.3", cfg.MinVersion)
	}
	if !cfg.SessionTicketsDisabled {
		t.Error("session tickets are enabled")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("got %d certificates, want 1", len(cfg.Certificates))
	}
}

// TestEmptyDirIsRefused: this package never derives the lola home, so a caller
// that forgot to pass one must not end up writing into the process's cwd.
func TestEmptyDirIsRefused(t *testing.T) {
	if _, err := LoadOrCreateDeviceKey("", nil); err == nil {
		t.Fatal("an empty directory was accepted")
	}
}
