//go:build unix

package sshconn

import (
	"io/fs"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/proctree"
)

func setGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killGroup(cmd *exec.Cmd, grace time.Duration) {
	if cmd.Process == nil {
		return
	}
	// Setpgid makes the child its own group leader, so its pid IS the group.
	_ = proctree.KillGroup(cmd.Process.Pid, grace)
}

func ownedByMe(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
