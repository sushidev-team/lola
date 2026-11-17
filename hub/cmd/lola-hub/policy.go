package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hubproto"
)

// A policy key file holds the ed25519 private key (64 bytes) as one line of
// base64url. It belongs on an admin's machine, never on the hub.

func (c *cli) policy(args []string) error {
	if len(args) == 0 {
		return usagef("policy needs init, keygen or sign")
	}
	switch args[0] {
	case "init":
		return c.policyInit(args[1:])
	case "keygen":
		return c.policyKeygen(args[1:])
	case "sign":
		return c.policySign(args[1:])
	}
	return usagef("unknown policy command %q", args[0])
}

func (c *cli) policyKeygen(args []string) error {
	fs := c.newFlags("policy keygen")
	out := fs.String("out", "", "private key file to create, 0600 (required)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	if *out == "" {
		return usagef("--out is required")
	}
	pub, priv, err := hubproto.GenerateKey()
	if err != nil {
		return err
	}
	if err := writeKeyFile(*out, priv); err != nil {
		return err
	}
	kid := hubproto.KeyIDFor(pub)
	fmt.Fprintf(c.stdout, "key_id:     %s\npublic_key: %s\ntrusted:    %s\n", kid, hubproto.EncodePublicKey(pub), hubproto.FormatTrustedKey(kid, pub))
	fmt.Fprintf(c.stderr, "private key written to %s; keep it off the hub.\n", *out)
	return nil
}

// writeKeyFile writes priv to a NEW file, 0600.
func writeKeyFile(path string, priv ed25519.PrivateKey) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create key file (it must not exist yet): %w", err)
	}
	if _, err := fmt.Fprintln(f, base64.RawURLEncoding.EncodeToString(priv)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Files policy init writes into its --out directory.
const (
	policyKeyFile    = "policy.key"
	policyDraftFile  = "policy.json"
	policySignedFile = "policy.signed.json"
)

// policyInit sets up policy signing on the ADMIN's machine in one step: a
// fresh key, a starter policy and that policy signed. Nothing here touches a
// hub; the private key never leaves this directory.
func (c *cli) policyInit(args []string) error {
	fs := c.newFlags("policy init")
	org := fs.String("org", "", "org the policy is for (required)")
	out := fs.String("out", "", "directory to create the key and policy files in (required)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	if *org == "" || *out == "" {
		return usagef("--org and --out are required")
	}
	if !hubproto.ValidOrg(*org) {
		return errors.New("org name must match ^[a-z0-9][a-z0-9_-]{0,63}$")
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	for _, name := range []string{policyKeyFile, policyDraftFile, policySignedFile} {
		if _, err := os.Lstat(filepath.Join(*out, name)); err == nil {
			return fmt.Errorf("%s already exists in %s; policy init never overwrites", name, *out)
		}
	}
	pub, priv, err := hubproto.GenerateKey()
	if err != nil {
		return err
	}
	kid := hubproto.KeyIDFor(pub)
	// The starter locks only escalate_after, which only the hub reads: it
	// changes nothing on a member's machine until the admin adds a key.
	draft := hubproto.Policy{Org: *org, Version: 1, IssuedAt: time.Now().UTC().Truncate(time.Second),
		Settings: hubproto.Settings{EscalateAfter: lease.DefaultEscalateAfter}}
	signed, err := hubproto.SignPolicy(priv, kid, draft)
	if err != nil {
		return err
	}
	if err := writeKeyFile(filepath.Join(*out, policyKeyFile), priv); err != nil {
		return err
	}
	for name, v := range map[string]hubproto.Policy{policyDraftFile: draft, policySignedFile: signed} {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(*out, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	trusted := hubproto.FormatTrustedKey(kid, pub)
	fmt.Fprintf(c.stdout, "trusted:    %s\n", trusted)
	fmt.Fprintf(c.stderr, `wrote %[1]s/%[2]s (private, 0600; keep it off the hub),
      %[1]s/%[3]s (edit, raise "version", then: lola-hub policy sign --key %[1]s/%[2]s %[1]s/%[3]s)
      %[1]s/%[4]s (v1, signed)
On the hub host:  lola-hub admin policy publish --register-key %[5]s %[4]s
Members pin the trusted line above in [hub].trusted_policy_keys (lola-hub invite --trusted does it for them).
`, *out, policyKeyFile, policyDraftFile, policySignedFile, trusted)
	return nil
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, errors.New("key file is not a base64url ed25519 private key")
	}
	return ed25519.PrivateKey(b), nil
}

func (c *cli) policySign(args []string) error {
	fs := c.newFlags("policy sign")
	keyPath := fs.String("key", "", "private key file from policy keygen (required)")
	keyID := fs.String("key-id", "", "key id (default: derived from the key)")
	out := fs.String("out", "", "write the signed policy here (default stdout)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 1, "<unsigned-policy.json>"); err != nil {
		return err
	}
	if *keyPath == "" {
		return usagef("--key is required")
	}
	priv, err := readPrivateKey(*keyPath)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(pos[0])
	if err != nil {
		return err
	}
	// Strict: an unknown key — post_create or any other executing key —
	// fails here, before anything is signed.
	p, err := hubproto.DecodePolicy(raw)
	if err != nil {
		return err
	}
	kid := *keyID
	if kid == "" {
		kid = hubproto.KeyIDFor(priv.Public().(ed25519.PublicKey))
	}
	if p.IssuedAt.IsZero() {
		p.IssuedAt = time.Now().UTC().Truncate(time.Second)
	}
	signed, err := hubproto.SignPolicy(priv, kid, p)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if *out == "" {
		_, err = c.stdout.Write(b)
		return err
	}
	return os.WriteFile(*out, b, 0o644)
}
