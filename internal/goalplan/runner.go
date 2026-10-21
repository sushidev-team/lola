package goalplan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/agentoutput"
	"github.com/sushidev-team/lola/internal/usage"
)

// RunFunc sees explicit environment values separately from argv. Credentials
// must never appear in recorded command lines or proposal records.
type RunFunc func(context.Context, string, []string, []byte, []string) ([]byte, error)
type Runner struct {
	Docker  string
	PassEnv []string
	Run     RunFunc
	OnUsage func(usage.Observation) error
}

func ContainerName(id string) string {
	return fmt.Sprintf("lola-plan-%x", sha256.Sum256([]byte(id)))[:42]
}

func (r Runner) Plan(ctx context.Context, record Record) (out Output, err error) {
	if record.State != "running" || record.InputSHA != Digest(record.Input) || !identity.MatchString(record.ID) || !agent.Valid(record.Agent) || record.Image == "" || strings.HasPrefix(record.Image, "-") || strings.ContainsAny(record.Image, " \t\r\n\x00") {
		return out, errors.New("invalid reserved planning run")
	}
	input, e := record.Input.Bytes()
	if e != nil {
		return out, e
	}
	if e = record.Input.Request.Normalize(); e != nil {
		return out, e
	}
	seconds := record.Input.Request.TimeoutSeconds
	if deadline, ok := ctx.Deadline(); ok {
		remaining := int(time.Until(deadline).Seconds())
		if remaining < 1 {
			return out, context.DeadlineExceeded
		}
		seconds = min(seconds, remaining)
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(seconds+15)*time.Second)
	defer cancel()
	bin := r.Docker
	if bin == "" {
		bin = "docker"
	}
	name := ContainerName(record.ID)
	args := []string{"run", "--rm", "--pull", "never", "--name", name, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "128", "--memory", "1g", "--cpus", "2", "--tmpfs", "/tmp:rw,nosuid,nodev,size=256m", "--workdir", "/tmp", "-i"}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	// The Docker client may need its host socket/context. Nothing from these
	// host settings is mounted or forwarded to the planning container.
	for _, key := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	allowed := []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY", "OPENROUTER_API_KEY"}
	seen := map[string]bool{}
	for _, key := range r.PassEnv {
		if !seen[key] && slices.Contains(allowed, key) {
			seen[key] = true
			if v, ok := os.LookupEnv(key); ok {
				args = append(args, "--env", key)
				env = append(env, key+"="+v)
			}
		}
	}
	for _, setting := range []string{"HOME=/tmp/home", "CODEX_HOME=/tmp/home/codex", "CLAUDE_CONFIG_DIR=/tmp/home/claude", "XDG_CONFIG_HOME=/tmp/home/config", "XDG_DATA_HOME=/tmp/home/data"} {
		args = append(args, "--env", setting)
	}
	kind := agent.Kind(record.Agent)
	instruction := Instruction
	if record.Input.Repair != nil {
		instruction += "\n\n" + RepairInstruction
	}
	argv := append([]string{kind.Binary()}, agent.TelemetryArgs(kind, instruction, record.Input.Request.Model, true)...)
	for i := range argv {
		argv[i] = quote(argv[i])
	}
	inner := "mkdir -p /tmp/home && exec timeout -s TERM -k 10 " + strconv.Itoa(seconds) + " " + strings.Join(argv, " ")
	args = append(args, "--entrypoint", "sh", record.Image, "-c", inner)
	defer func() {
		// CommandContext cannot stop a detached Docker engine container. A lost
		// CLI response is therefore always followed by cleanup of our own name.
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		_, stopErr := r.call(cleanup, bin, []string{"rm", "--force", name}, nil, env)
		if stopErr != nil {
			listing, listErr := r.call(cleanup, bin, []string{"ps", "-aq", "--filter", "name=^/" + name + "$"}, nil, env)
			if listErr != nil || strings.TrimSpace(string(listing)) != "" {
				err = errors.Join(err, errors.New("planner container cleanup unconfirmed; no proposal accepted"))
			}
		}
	}()
	body, e := r.call(cctx, bin, args, input, env)
	text, observed, decodeErr := agentoutput.Decode(record.Agent, body, time.Now().UTC())
	if r.OnUsage != nil {
		if usageErr := r.OnUsage(observed); usageErr != nil {
			return out, usageErr
		}
	}
	if e != nil {
		return out, e
	}
	if decodeErr != nil {
		return out, decodeErr
	}
	if cctx.Err() != nil {
		return out, cctx.Err()
	}
	return Parse(text, record.Input)
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (r Runner) call(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
	if r.Run != nil {
		return r.Run(ctx, bin, args, input, env)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(input)
	cmd.WaitDelay = 2 * time.Second
	out, stderr := &capture{limit: agentoutput.MaxBytes}, &capture{}
	cmd.Stdout = out
	cmd.Stderr = stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.Bytes(), ctx.Err()
	}
	if out.overflow || stderr.overflow {
		return out.Bytes(), errors.New("planner output exceeded bounds")
	}
	// CLI stderr can echo credentials or source snippets; retain only the exit
	// error in the durable record, never raw stderr.
	if err != nil {
		return out.Bytes(), fmt.Errorf("planner command failed: %w", err)
	}
	return out.Bytes(), nil
}

type capture struct {
	bytes.Buffer
	overflow bool
	limit    int
}

func (b *capture) Write(p []byte) (int, error) {
	n := len(p)
	limit := b.limit
	if limit == 0 {
		limit = 64 << 10
	}
	left := limit - b.Len()
	if n > left {
		b.overflow = true
		p = p[:max(0, left)]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
