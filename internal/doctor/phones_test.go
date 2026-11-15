package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/devices"
)

// A registry file that cannot be trusted fails the row with the fix id and
// names the files without quoting their contents. A fresh registry is healthy.
// (A revocation that is unsaved lives only in the daemon's memory; a separate
// process opening the files cannot see it, so only unreadable files are
// reachable here.)
func TestPhonesRowFailsOnBrokenOrUnsavedRegistry(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		r := PhonesResult(t.TempDir())
		if !r.OK || r.Critical || r.Fix != FixNone || r.Name != "phones" {
			t.Fatalf("fresh = %+v", r)
		}
	})
	for _, file := range []string{devices.FileName, devices.RevokedFileName} {
		t.Run(file, func(t *testing.T) {
			home := t.TempDir()
			const secret = "SECRET-CONTENT"
			if err := os.WriteFile(filepath.Join(home, file), []byte(secret), 0o600); err != nil {
				t.Fatal(err)
			}
			r := PhonesResult(home)
			if r.OK || r.Critical || r.Fix != FixPhonesRegistry {
				t.Fatalf("broken = %+v", r)
			}
			if !strings.Contains(r.Detail, devices.FileName) || strings.Contains(r.Detail, secret) {
				t.Fatalf("detail = %q", r.Detail)
			}
		})
	}
	t.Run("fix id registered", func(t *testing.T) {
		found := 0
		for _, id := range AllFixIDs() {
			if id == FixPhonesRegistry || id == FixRelayHubUpgrade {
				found++
			}
		}
		if found != 2 || FixPhonesRegistry != "phones_registry" || FixRelayHubUpgrade != "relay_hub_upgrade" {
			t.Fatalf("fix ids not registered: %d", found)
		}
	})
}
