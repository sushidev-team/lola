// Package hubproto is the wire contract between a lola daemon and a lola hub.
//
// It is a nested, dependency-free module (standard library only) so that anyone
// can read exactly what a daemon will send and accept, even when a hub
// deployment is private. The hub imports it; lola's root module requires it
// through a `replace ./hubproto`.
//
// The one rule the contract exists to keep: a compromised hub must not mean
// code execution on a teammate's machine. Hence:
//
//   - Every request a daemon makes is signed with its own ed25519 device key
//     ([Sign], [Verify]). The hub keeps the replay cache; this package only
//     defines the canonical string and the skew window ([MaxSkew]).
//   - A work item is a closed [Kind] plus typed fields, every one of them
//     validated against a strict pattern by [Validate]. These fields are
//     interpolated into an agent prompt on the daemon, so a field that does not
//     validate makes the WHOLE item invalid, on both sides.
//   - Team settings travel as a [Policy] signed by an admin key the daemon pins
//     locally ([VerifyPolicy]). [Settings] is a closed struct of NON-executing
//     keys only, decoded with unknown fields disallowed, so an executing key
//     such as post_create fails verification instead of being ignored.
//
// Error messages produced here name the offending FIELD, never its value: the
// value is untrusted and may carry control characters into a log line.
package hubproto
