package goalwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// CanRetryIntegration permits abandonment only BEFORE any ref-update intent.
// A journal, even one that appears incomplete, must recover using its original
// proof. Unknown filesystem/ref state holds rather than losing a partial stack.
func (m *Manager) CanRetryIntegration(ctx context.Context, goal, operation string, base map[string]string) error {
	dir, err := m.path(goal, "integrations")
	if err != nil {
		return err
	}
	if !segment.MatchString(operation) {
		return errors.New("invalid integration identity")
	}
	if _, err := os.Lstat(filepath.Join(dir, operation+".json")); err == nil {
		return errors.New("integration already has a ref-update intent; recover the original operation")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for member, sha := range base {
		cdir, err := m.path(goal, "candidates", operation, member)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(cdir, "integration.json")); err == nil {
			return errors.New("member has a ref-update intent; recover the original operation")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if tip, err := m.Tip(ctx, goal, member); err != nil || tip != sha {
			return ErrStale
		}
	}
	return nil
}
