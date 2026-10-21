package gitrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
)

// CodeIdentity is metadata only: no source content, filters, hooks, fetches or
// working-tree files. A directory identity covers its complete committed tree.
type CodeIdentity struct {
	Commit, Digest string
	Exists         bool
}
type CodeReader struct {
	Run func(context.Context, string, ...string) ([]byte, error)
}

var objectID = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

func (r CodeReader) Read(ctx context.Context, dir, branch, file string) (CodeIdentity, error) {
	var out CodeIdentity
	if dir == "" || branch == "" || strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, "refs/") || strings.ContainsAny(branch, "\x00\r\n~^:?*[") || strings.Contains(branch, "..") || file == "" || path.IsAbs(file) || path.Clean(file) != file || file == ".." || strings.HasPrefix(file, "../") || strings.ContainsAny(file, "\\\x00\r\n\t") {
		return out, errors.New("invalid committed-code reference")
	}
	run := r.Run
	if run == nil {
		run = readCodeGit
	}
	body, err := run(ctx, dir, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return out, errors.New("configured local branch is unreadable")
	}
	out.Commit = strings.TrimSpace(string(body))
	if !objectID.MatchString(out.Commit) {
		return CodeIdentity{}, errors.New("invalid commit identity")
	}
	if file == "." {
		body, err = run(ctx, dir, "rev-parse", "--verify", out.Commit+"^{tree}")
		if err != nil || !objectID.MatchString(strings.TrimSpace(string(body))) {
			return out, errors.New("committed tree is unreadable")
		}
		body = []byte(strings.TrimSpace(string(body)))
	} else {
		body, err = run(ctx, dir, "ls-tree", "-z", out.Commit, "--", file)
		if err != nil {
			return out, errors.New("committed path is unreadable")
		}
		if len(body) == 0 {
			return out, nil
		}
		entries := strings.Split(string(body), "\x00")
		if len(entries) != 2 || entries[1] != "" {
			return out, errors.New("ambiguous committed path")
		}
		metadata, name, ok := strings.Cut(entries[0], "\t")
		fields := strings.Fields(metadata)
		if !ok || name != file || len(fields) != 3 || !objectID.MatchString(fields[2]) {
			return out, errors.New("invalid committed path identity")
		}
	}
	sum := sha256.Sum256(body)
	out.Digest = hex.EncodeToString(sum[:])
	out.Exists = true
	return out, nil
}
func readCodeGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	argv := []string{"--no-optional-locks", "--literal-pathspecs", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", dir}
	cmd := exec.CommandContext(ctx, "git", append(argv, args...)...)
	// Remove inherited repository/config redirection and forbid lazy promisor IO.
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "GIT_TERMINAL_PROMPT=0")
	return cmd.Output()
}
