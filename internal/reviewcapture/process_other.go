//go:build !unix

package reviewcapture

import "os/exec"

func containProcess(cmd *exec.Cmd) {}
