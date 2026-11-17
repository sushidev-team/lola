package projectgit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Snapshot into a temporary index so comparison includes staged, unstaged and
// untracked files (even paths that existed in the historical base). The real
// index, checkout and refs are untouched. Ignored files stay excluded unless
// already tracked in the index, just as in ordinary Git staging.
func (c Client) workingIndex(ctx context.Context, dir string) (Client, func(), error) {
	file, err := os.CreateTemp("", "lola-git-compare-*")
	if err != nil {
		return c, nil, err
	}
	path := file.Name()
	file.Close()
	cleanup := func() { os.Remove(path); os.Remove(path + ".lock") }
	index, err := c.text(ctx, dir, "rev-parse", "--git-path", "index")
	if err != nil {
		cleanup()
		return c, nil, err
	}
	if !filepath.IsAbs(index) {
		index = filepath.Join(dir, index)
	}
	info, err := os.Stat(index)
	if err == nil {
		if info.Size() > 64<<20 {
			cleanup()
			return c, nil, errors.New("repository index is too large to preview")
		}
		data, readErr := os.ReadFile(index)
		if readErr != nil {
			cleanup()
			return c, nil, readErr
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			cleanup()
			return c, nil, err
		}
	} else if os.IsNotExist(err) {
		os.Remove(path)
	} else {
		cleanup()
		return c, nil, err
	}
	originalRun := c.run
	c.run = func(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
		if originalRun != nil {
			return originalRun(ctx, dir, input, args...)
		}
		return runGitEnvironment(ctx, dir, input, []string{"GIT_INDEX_FILE=" + path}, args...)
	}
	// Read any shared index through the repository, then write the temporary
	// snapshot as a whole index without updating the real split-index files.
	if _, err = c.git(ctx, dir, "", "-c", "core.splitIndex=false", "-c", "core.fsmonitor=false", "add", "--all", "--", "."); err != nil {
		cleanup()
		return c, nil, err
	}
	return c, cleanup, nil
}
