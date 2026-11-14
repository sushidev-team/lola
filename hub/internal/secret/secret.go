// Package secret seals the per-org secrets the hub has to keep (a Linear API
// key, a digest webhook) before they reach the store.
//
// The master key comes from the environment (EnvKey) only: it is read once,
// removed from the environment, never logged, never written to the store and
// never returned by any endpoint. A copy of the store file alone therefore
// holds no usable secret.
//
// A sealed value is AES-256-GCM under the master key with a random nonce. The
// additional authenticated data names the org AND the field, so a sealed
// value cannot be moved to another org or another field by editing the store.
//
// The package does no I/O.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// EnvKey names the environment variable holding the master key: 32 random
// bytes, base64 (as printed by "lola-hub secret-key").
const EnvKey = "LOLA_HUB_SECRET_KEY"

// Fields a secret is sealed for.
const (
	FieldLinearKey     = "linear_key"
	FieldDigestWebhook = "digest_webhook"
)

// Errors. None carries key material or plaintext.
var (
	// ErrNoKey: the hub runs without a master key, so it stores no secret.
	ErrNoKey = errors.New("secret: no " + EnvKey + " in the hub's environment")
	// ErrBadKey: the master key is not 32 bytes of base64.
	ErrBadKey = errors.New("secret: " + EnvKey + " must be 32 random bytes in base64 (lola-hub secret-key prints one)")
	// ErrOtherKey: the value was sealed under a different master key.
	ErrOtherKey = errors.New("secret: sealed with a different key")
	// ErrCorrupt: the value does not open (damaged, or moved between orgs or
	// fields).
	ErrCorrupt = errors.New("secret: sealed value does not open")
)

const version = "v1"

// Box seals and opens secrets. A nil *Box is a hub without a master key:
// Seal and Open answer ErrNoKey.
type Box struct {
	aead cipher.AEAD
	id   string // which key sealed a value: a hash prefix, not the key
}

// NewKey returns a fresh master key in the form EnvKey takes.
func NewKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// New builds a Box from the EnvKey value. "" returns (nil, nil): no key, no
// secrets. Anything that is not exactly 32 bytes is refused, so a passphrase
// is never used as a key.
func New(encoded string) (*Box, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, nil
	}
	var key []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if key, err = enc.DecodeString(encoded); err == nil {
			break
		}
	}
	if err != nil || len(key) != 32 {
		return nil, ErrBadKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrBadKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrBadKey
	}
	sum := sha256.Sum256(append([]byte("lola-hub secret key id\x00"), key...))
	return &Box{aead: aead, id: hex.EncodeToString(sum[:4])}, nil
}

func aad(org, field string) []byte {
	return []byte("lola-hub/" + version + "\x00" + org + "\x00" + field)
}

// Seal encrypts plain for org's field.
func (b *Box) Seal(org, field, plain string) (string, error) {
	if b == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nonce, nonce, []byte(plain), aad(org, field))
	return version + "." + b.id + "." + base64.RawURLEncoding.EncodeToString(ct), nil
}

// Open decrypts a value Seal produced for the same org and field.
func (b *Box) Open(org, field, sealed string) (string, error) {
	if b == nil {
		return "", ErrNoKey
	}
	parts := strings.Split(sealed, ".")
	if len(parts) != 3 || parts[0] != version {
		return "", ErrCorrupt
	}
	if parts[1] != b.id {
		return "", ErrOtherKey
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", ErrCorrupt
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], aad(org, field))
	if err != nil {
		return "", ErrCorrupt
	}
	return string(plain), nil
}
