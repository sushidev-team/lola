package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// openURLTimeout bounds the opener exec so a wedged handler never hangs.
const openURLTimeout = 5 * time.Second

// handleOpenURL opens a URL in the user's default browser on the DAEMON side
// (cmd=openURL), so the socket client (TUI) stays exec-free. Only http(s) URLs
// are accepted — the value reaches a platform opener, so anything else (a
// file:// path, a shell-ish string) is refused rather than handed to `open`.
func (d *Daemon) handleOpenURL(ctx context.Context, a protocol.OpenURLArgs) error {
	url := strings.TrimSpace(a.URL)
	if url == "" {
		return errors.New("openURL: url required")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("openURL: refusing to open non-http(s) URL %q", url)
	}

	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{url}
	default:
		name, args = "xdg-open", []string{url}
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("openURL: %s not found on PATH", name)
	}

	cctx, cancel := context.WithTimeout(ctx, openURLTimeout)
	defer cancel()
	if err := exec.CommandContext(cctx, bin, args...).Run(); err != nil {
		return fmt.Errorf("openURL: %s: %w", name, err)
	}
	d.logf("", "openURL: opened %s", url)
	return nil
}
