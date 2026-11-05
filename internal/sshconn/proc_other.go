//go:build !unix

package sshconn

import (
	"io/fs"
	"os/exec"
	"time"
)

func setGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd, _ time.Duration) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func ownedByMe(fs.FileInfo) bool { return false }
