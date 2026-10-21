package goalwork

import (
	"errors"
	"os"
	"path/filepath"
)

// HasIntegrationIntent distinguishes an operation that must finish using its
// captured inputs from one which may still hold before writing the first ref.
// The manifest intent always precedes the per-member intents in AdvanceManifest.
func (m *Manager) HasIntegrationIntent(goal, operation string) (bool, error) {
	dir, err := m.path(goal, "integrations")
	if err != nil {
		return false, err
	}
	if !segment.MatchString(operation) {
		return false, errors.New("invalid integration identity")
	}
	info, err := os.Lstat(filepath.Join(dir, operation+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("integration intent is not a regular receipt")
	}
	return true, nil
}
