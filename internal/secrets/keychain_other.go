//go:build !darwin

package secrets

// keychainLookup reports not-found on non-darwin platforms so LinearAPIKey
// falls through to the environment variable.
func keychainLookup(string) (string, error) {
	return "", errKeychainNotFound
}
