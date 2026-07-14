//go:build !darwin

package secrets

import "errors"

// keychainLookup reports not-found on non-darwin platforms so LinearAPIKey
// falls through to the environment variable.
func keychainLookup(string) (string, error) {
	return "", errKeychainNotFound
}

// StoreLinearAPIKey is unsupported off macOS; callers must configure an
// environment variable and export the key instead.
func StoreLinearAPIKey(string, string) error {
	return errors.New("keychain storage is only supported on macOS; set api_key_env and export the key instead")
}
