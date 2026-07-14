//go:build !darwin

package secrets

import (
	"strings"
	"testing"
)

// TestStoreLinearAPIKeyUnsupported verifies the off-macOS path returns a clean
// unsupported-message that never contains the key value.
func TestStoreLinearAPIKeyUnsupported(t *testing.T) {
	const secret = "lin_api_do_not_leak"
	err := StoreLinearAPIKey("lola-linear", secret)
	if err == nil {
		t.Fatal("want unsupported error off macOS")
	}
	if !strings.Contains(err.Error(), "only supported on macOS") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("error text contains the key")
	}
}
