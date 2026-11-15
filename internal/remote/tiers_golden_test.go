package remote

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// tiersGolden is the tier table the phone client reads (mobile/src/wire/tiers.ts),
// so the client never spells a tier by hand.
const tiersGolden = "../../mobile/src/wire/testdata/tiers.json"

// TestTierGolden compares CommandTiers() to mobile/src/wire/testdata/tiers.json.
// LOLA_WRITE_VECTORS=1 rewrites the file.
func TestTierGolden(t *testing.T) {
	// A map marshals with sorted keys, so the file is stable.
	b, err := json.MarshalIndent(CommandTiers(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if os.Getenv("LOLA_WRITE_VECTORS") == "1" {
		if err := os.MkdirAll(filepath.Dir(tiersGolden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tiersGolden, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(tiersGolden)
	if err != nil {
		t.Fatalf("%v (write it with LOLA_WRITE_VECTORS=1 go test ./internal/remote -run TestTierGolden)", err)
	}
	if !bytes.Equal(got, b) {
		t.Fatal("mobile/src/wire/testdata/tiers.json differs from remote.CommandTiers(); rewrite it with LOLA_WRITE_VECTORS=1 go test ./internal/remote -run TestTierGolden")
	}
}
