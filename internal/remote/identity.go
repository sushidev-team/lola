package remote

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// The daemon's own TLS identity: an ECDSA P-256 key and the self-signed
// certificate that carries it, generated once and then never again.
//
// It lives at ~/.lola/device.key and ~/.lola/device.crt, 0600 inside a 0700
// directory, and deliberately NOT in the macOS Keychain: a launchd-started
// process cannot unlock the login keychain non-interactively, and the daemon
// has to come up headless. The files are written with the same temp+rename
// dance internal/session's Store uses for its snapshot, so a crash mid-write
// leaves the previous identity intact rather than a half-file that would look
// like corruption.
//
// The identity is STABLE, and that is its whole purpose. A phone pins the SPKI
// hash at pairing time (M2 puts it in the QR), so silently minting a new key
// would break every enrolled device with no diagnosable symptom. Hence the
// fail-closed rules below: a key without its certificate, a certificate without
// its key, or a pair that does not match are all errors naming both paths.
// Recovery is a human deleting the files and re-pairing, which is a decision,
// not something this function makes on their behalf.

const (
	// DeviceKeyFile and DeviceCertFile are the file names under the lola home
	// directory. They are exported so the doctor and the pairing UI can name
	// them in a message without re-deriving the convention.
	DeviceKeyFile  = "device.key"
	DeviceCertFile = "device.crt"

	// deviceCertLifetime is long because this is a device identity rather than
	// a web certificate: nothing checks it against a public trust store, the
	// client pins the SPKI, and rotation is a re-pair by design (there is no
	// key-update protocol, because one would be a second enrolment path to get
	// wrong for a saving of thirty seconds).
	deviceCertLifetime = 10 * 365 * 24 * time.Hour

	// deviceCertBackdate absorbs clock skew between the daemon and a phone.
	deviceCertBackdate = time.Hour
)

// ErrDeviceKeyIncomplete is returned when exactly one half of the identity
// exists on disk, or when the two halves do not belong together. It never
// triggers a regeneration: see the note above.
var ErrDeviceKeyIncomplete = errors.New("remote: device identity is incomplete")

// DeviceKey is the daemon's TLS identity.
type DeviceKey struct {
	// Certificate is ready to hand to crypto/tls.
	Certificate tls.Certificate

	// Leaf is the parsed certificate, kept so callers can read the SPKI and the
	// validity window without re-parsing.
	Leaf *x509.Certificate

	// KeyPath and CertPath are where it was loaded from or written to.
	KeyPath  string
	CertPath string

	// Created records whether this call generated the identity (a first run)
	// rather than loading it. The caller logs it once; nothing branches on it.
	Created bool
}

// SPKISHA256 is the SHA-256 of the certificate's SubjectPublicKeyInfo — the
// value a client pins. Hashing the SPKI rather than the whole certificate is
// what lets the certificate be reissued over the same key without invalidating
// every pairing.
func (dk *DeviceKey) SPKISHA256() [32]byte {
	return sha256.Sum256(dk.Leaf.RawSubjectPublicKeyInfo)
}

// SPKIPin is the base64 of SPKISHA256, the form M2's QR payload carries and the
// form a human compares in a log line. It is standard base64 with padding, the
// same encoding HPKP used, so a value pasted between tools means one thing.
func (dk *DeviceKey) SPKIPin() string {
	h := dk.SPKISHA256()
	return base64.StdEncoding.EncodeToString(h[:])
}

// TLSConfig is the server configuration for this identity.
//
// TLS 1.3 only, and session tickets disabled: a replayed kill frame is on the
// threat list, so every connection gets an ephemeral handshake and there is no
// resumption to replay against. Go's TLS 1.3 server does not implement 0-RTT at
// all, which is the other half of that requirement and is why nothing here has
// to switch it off.
//
// ClientAuth is NoClientCert in M1 because M1 has no cryptography to
// authenticate with; the peer proves itself in band. M2 turns this into
// RequireAnyClientCert plus a registry check inside VerifyPeerCertificate, so
// an unpaired laptop on the same cafe WiFi is rejected inside the TLS stack and
// never reaches the WebSocket upgrade, the JSON parser or the envelope decoder.
func (dk *DeviceKey) TLSConfig() *tls.Config {
	return &tls.Config{
		Certificates:           []tls.Certificate{dk.Certificate},
		MinVersion:             tls.VersionTLS13,
		SessionTicketsDisabled: true,
		ClientAuth:             tls.NoClientCert,
	}
}

// LoadOrCreateDeviceKey loads the identity under dir, generating it on a first
// run. dir is the lola home directory (config.Home()); it is created 0700 when
// absent.
//
// An existing directory is NOT re-permissioned — that is the operator's
// directory and silently tightening it is a surprise — but a group- or
// world-accessible one is reported through logf so the condition is
// discoverable rather than invisible. The key file itself is 0600 either way,
// which is what actually protects it.
//
// logf may be nil.
func LoadOrCreateDeviceKey(dir string, logf func(string, ...any)) (*DeviceKey, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if dir == "" {
		return nil, errors.New("remote: device key directory is empty")
	}
	keyPath := filepath.Join(dir, DeviceKeyFile)
	certPath := filepath.Join(dir, DeviceCertFile)

	if err := ensureDir(dir, logf); err != nil {
		return nil, err
	}

	keyPEM, keyErr := os.ReadFile(keyPath)
	certPEM, certErr := os.ReadFile(certPath)
	switch {
	case keyErr == nil && certErr == nil:
		dk, err := loadDeviceKey(keyPEM, certPEM)
		if err != nil {
			return nil, fmt.Errorf("%w: %s and %s do not form a usable identity: %v (delete both to re-generate, which invalidates every paired device)",
				ErrDeviceKeyIncomplete, keyPath, certPath, err)
		}
		dk.KeyPath, dk.CertPath = keyPath, certPath
		return dk, nil

	case errors.Is(keyErr, os.ErrNotExist) && errors.Is(certErr, os.ErrNotExist):
		// First run. Both halves are written before either is used.
		dk, err := generateDeviceKey()
		if err != nil {
			return nil, err
		}
		if err := writeSecret(keyPath, dk.keyPEM); err != nil {
			return nil, err
		}
		if err := writeSecret(certPath, dk.certPEM); err != nil {
			return nil, err
		}
		dk.KeyPath, dk.CertPath = keyPath, certPath
		dk.Created = true
		return dk.DeviceKey, nil

	case errors.Is(keyErr, os.ErrNotExist):
		return nil, fmt.Errorf("%w: %s exists but %s does not", ErrDeviceKeyIncomplete, certPath, keyPath)
	case errors.Is(certErr, os.ErrNotExist):
		return nil, fmt.Errorf("%w: %s exists but %s does not", ErrDeviceKeyIncomplete, keyPath, certPath)
	}
	if keyErr != nil {
		return nil, keyErr
	}
	return nil, certErr
}

// ensureDir creates dir 0700 when absent and reports an over-permissive
// existing one.
func ensureDir(dir string, logf func(string, ...any)) error {
	fi, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.MkdirAll(dir, 0o700)
	case err != nil:
		return err
	case !fi.IsDir():
		return fmt.Errorf("remote: %s is not a directory", dir)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		logf("remote: %s is mode %04o; 0700 is expected for the directory holding %s", dir, mode, DeviceKeyFile)
	}
	return nil
}

// generated bundles the parsed identity with the bytes to persist, so the
// caller writes exactly what it just validated.
type generated struct {
	*DeviceKey
	keyPEM  []byte
	certPEM []byte
}

func generateDeviceKey() (*generated, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("remote: generate device key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("remote: device certificate serial: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "lola"},
		NotBefore:             now.Add(-deviceCertBackdate),
		NotAfter:              now.Add(deviceCertLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		// The names are a courtesy for a client that verifies the hostname
		// instead of the pin; the pin is what actually identifies this daemon.
		DNSNames:    []string{"lola"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("remote: create device certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("remote: marshal device key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	dk, err := loadDeviceKey(keyPEM, certPEM)
	if err != nil {
		return nil, err
	}
	return &generated{DeviceKey: dk, keyPEM: keyPEM, certPEM: certPEM}, nil
}

// loadDeviceKey parses the pair and proves they belong together — tls.X509KeyPair
// compares the certificate's public key against the private key, so a mismatched
// pair fails here rather than at the first handshake.
func loadDeviceKey(keyPEM, certPEM []byte) (*DeviceKey, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	cert.Leaf = leaf
	return &DeviceKey{Certificate: cert, Leaf: leaf}, nil
}

// writeSecret writes b to path atomically at 0600: a temp file in the
// DESTINATION directory (so the rename cannot cross filesystems), chmod, write,
// sync, rename. The same shape as session.Store.Save, and for the same reason —
// a torn write here would look exactly like a corrupted identity.
func writeSecret(path string, b []byte) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() {
		if tmp != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	name := tmp.Name()
	tmp = nil // written and closed; disarm the cleanup deferral
	return os.Rename(name, path)
}
