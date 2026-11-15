package devices

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The revocation journal is append-only: one line "<spki std base64> <epoch>"
// per revoked device whose removal could not be written to devices.json. Open
// hides every registry entry whose SPKI is listed with Epoch <= the journal's
// epoch, so a later re-pair (a higher epoch) stays visible. A line that does
// not parse makes the registry broken: guessing at a revocation record could
// resurrect a revoked phone.

// readJournal returns the highest journalled epoch per SPKI. A missing file is
// an empty journal.
func readJournal(path string) (map[[32]byte]uint64, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[[32]byte]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 1024), 4096)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("line %d: want \"<spki> <epoch>\"", n)
		}
		raw, err := base64.StdEncoding.DecodeString(f[0])
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("line %d: spki is not 32 bytes of base64", n)
		}
		ep, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: epoch is not a number", n)
		}
		var k [32]byte
		copy(k[:], raw)
		if cur, ok := out[k]; !ok || ep > cur {
			out[k] = ep
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// appendJournalLine durably appends one revocation record.
func appendJournalLine(path, spki string, epoch uint64) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "%s %d\n", spki, epoch); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// truncateJournal empties the journal once devices.json is authoritative again.
func truncateJournal(path string) error {
	err := os.Truncate(path, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
