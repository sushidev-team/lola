package goalcheck

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/goalwork"
)

func containerArgs(w goalwork.ValidationWorkspace, operation string, spec Spec) ([]string, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if strings.ContainsAny(w.Directory, "\x00\r\n") {
		return nil, errors.New("invalid validation mount path")
	}
	// --mount parses CSV; quote an entire source field to preserve commas.
	var b bytes.Buffer
	csvw := csv.NewWriter(&b)
	if err := csvw.Write([]string{"type=bind", "source=" + w.Directory, "target=/workspace"}); err != nil {
		return nil, err
	}
	csvw.Flush()
	args := []string{"run", "--rm", "--init", "--name", containerName(w.Goal, w.ID),
		"--label", "dev.sushi.lola.goal=" + w.Goal, "--label", "dev.sushi.lola.check=" + w.ID,
		"--pull", "never", "--network", "none", "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--pids-limit", fmt.Sprint(PIDLimit), "--memory", fmt.Sprintf("%dm", spec.MemoryLimitBytes()>>20), "--cpus", fmt.Sprint(CPULimit),
		"--tmpfs", fmt.Sprintf("/tmp:rw,nosuid,size=%d", ScratchLimitBytes), "--mount", strings.TrimSuffix(b.String(), "\n"),
		"--workdir", path.Join("/workspace", spec.Member), "--env", "HOME=/tmp/home",
		"--env", "LOLA_GOAL=" + w.Goal, "--env", "LOLA_CHECK=" + spec.ID, "--env", "LOLA_INTEGRATION=" + operation,
		"--entrypoint", "sh", spec.Image, "-c", "mkdir -p /tmp/home && " + spec.Command}
	return args, nil
}

func (r *Runner) command(ctx context.Context, args []string) (string, error) {
	bin := r.Docker
	if bin == "" {
		bin = "docker"
	}
	if r.Run != nil {
		return r.Run(ctx, bin, args)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 2 * time.Second
	out := &boundedOutput{}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), ctx.Err()
	}
	if out.truncated {
		return out.String(), errors.New("check output exceeded 1 MiB; result is incomplete")
	}
	if err != nil {
		return out.String(), fmt.Errorf("docker %s: %w: %s", args[0], err, out.String())
	}
	return out.String(), nil
}

type boundedOutput struct {
	bytes.Buffer
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := (1 << 20) - b.Len()
	if n > left {
		b.truncated = true
		p = p[:max(0, left)]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
