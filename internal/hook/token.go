package hook

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The sandbox hook bridge's credentials. A containerized agent reaches the
// daemon over loopback TCP, which — unlike the 0600 unix socket — any local
// process can connect to, so every request on it must prove which session it
// speaks for. The proof is TokenFor(secret, session): an HMAC under a
// machine-local secret, so the daemon needs no per-session state to check it
// (a restart, a revive or an agent switch keeps working) and a token minted for
// one session is useless for any other.

// secretLen is the HMAC key size in bytes.
const secretLen = 32

// TokenFor is the bridge token for session under secret (hex HMAC-SHA256).
func TokenFor(secret []byte, session string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(session))
	return hex.EncodeToString(m.Sum(nil))
}

// ValidToken reports, in constant time, whether token is session's token.
func ValidToken(secret []byte, session, token string) bool {
	if len(secret) == 0 || session == "" || token == "" {
		return false
	}
	return hmac.Equal([]byte(TokenFor(secret, session)), []byte(token))
}

// LoadOrCreateSecret reads the bridge secret at path, creating it (0600, 32
// random bytes, parent 0700) on first use. A file of the wrong size is an
// error rather than silently regenerated: replacing it would invalidate every
// live sandboxed session's token.
func LoadOrCreateSecret(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) != secretLen {
			return nil, fmt.Errorf("hook secret %s: unexpected size %d (delete it to regenerate)", path, len(b))
		}
		return b, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	b = make([]byte, secretLen)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) { // lost a creation race: read the winner's
			return LoadOrCreateSecret(path)
		}
		return nil, err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return nil, err
	}
	return b, f.Close()
}
