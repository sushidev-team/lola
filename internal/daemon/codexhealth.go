package daemon

import (
	"context"
	"os"
	"sync"

	"github.com/sushidev-team/lola/internal/doctor"
)

// Only successful capability checks are cached. Replacing/upgrading the binary
// invalidates the cache; failures retry so a repaired installation recovers.
var codexCapability struct {
	sync.Mutex
	path string
	info os.FileInfo
}

func checkCodexCapability(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	codexCapability.Lock()
	defer codexCapability.Unlock()
	if codexCapability.path == path && codexCapability.info != nil &&
		os.SameFile(codexCapability.info, info) &&
		codexCapability.info.ModTime() == info.ModTime() && codexCapability.info.Size() == info.Size() {
		return nil
	}
	if err := doctor.CheckCodexAutoApproval(context.Background(), path); err != nil {
		return err
	}
	codexCapability.path, codexCapability.info = path, info
	return nil
}
