package goalpublish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type pullRequest struct {
	Number   int     `json:"number"`
	URL      string  `json:"html_url"`
	Body     string  `json:"body"`
	State    string  `json:"state"`
	Draft    bool    `json:"draft"`
	Merged   bool    `json:"merged"`
	MergedAt *string `json:"merged_at"`
	Head     struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
}

type refs struct{ Default, Head, Base string }

func (p *Publisher) refs(ctx context.Context, repo, branch, target string) (refs, error) {
	owner, name, _ := strings.Cut(repo, "/")
	query := `query($owner:String!,$name:String!,$head:String!,$base:String!){repository(owner:$owner,name:$name){nameWithOwner defaultBranchRef{name} head:ref(qualifiedName:$head){target{oid}} base:ref(qualifiedName:$base){target{oid}}}}`
	body, _ := json.Marshal(map[string]any{"query": query, "variables": map[string]string{"owner": owner, "name": name, "head": "refs/heads/" + branch, "base": "refs/heads/" + target}})
	b, err := p.gh(ctx, []string{"api", "--hostname", "github.com", "graphql", "--input", "-"}, body)
	if err != nil {
		return refs{}, err
	}
	type ref struct {
		Name   string `json:"name"`
		Target struct {
			OID string `json:"oid"`
		} `json:"target"`
	}
	var result struct {
		Errors []json.RawMessage `json:"errors"`
		Data   struct {
			Repository *struct {
				Name    string `json:"nameWithOwner"`
				Default *ref   `json:"defaultBranchRef"`
				Head    *ref   `json:"head"`
				Base    *ref   `json:"base"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return refs{}, err
	}
	r := result.Data.Repository
	if len(result.Errors) > 0 || r == nil || !strings.EqualFold(r.Name, repo) || r.Default == nil || r.Default.Name == "" || r.Base == nil || r.Base.Target.OID == "" {
		return refs{}, errors.New("cannot establish GitHub repository and delivery target")
	}
	out := refs{Default: r.Default.Name, Base: r.Base.Target.OID}
	if r.Head != nil {
		out.Head = r.Head.Target.OID
		if out.Head == "" {
			return out, errors.New("remote goal ref has no commit")
		}
	}
	return out, nil
}

func (p *Publisher) pullRequests(ctx context.Context, repo, branch string) ([]pullRequest, error) {
	owner, _, _ := strings.Cut(repo, "/")
	b, err := p.gh(ctx, []string{"api", "--hostname", "github.com", "--method", "GET", "repos/" + repo + "/pulls", "-f", "state=all", "-f", "head=" + owner + ":" + branch, "-f", "per_page=100"}, nil)
	if err != nil {
		return nil, err
	}
	var prs []pullRequest
	if err := json.Unmarshal(b, &prs); err != nil {
		return nil, err
	}
	if len(prs) >= 100 {
		return nil, errors.New("goal PR result is truncated")
	}
	for i := range prs {
		prs[i].Merged = prs[i].MergedAt != nil && *prs[i].MergedAt != ""
		if prs[i].Merged {
			if _, err := time.Parse(time.RFC3339, *prs[i].MergedAt); err != nil || prs[i].State != "closed" {
				return nil, errors.New("inconsistent GitHub merge observation")
			}
		}
	}
	return prs, nil
}

func (p *Publisher) createPR(ctx context.Context, repo, head, base, title, body string) ([]byte, error) {
	b, _ := json.Marshal(map[string]any{"title": title, "head": head, "base": base, "body": body, "draft": true, "maintainer_can_modify": false})
	return p.gh(ctx, []string{"api", "--hostname", "github.com", "--method", "POST", "repos/" + repo + "/pulls", "--input", "-"}, b)
}

func (p *Publisher) gh(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
	bin := p.GhBin
	if bin == "" {
		bin = "gh"
	}
	return p.command(ctx, "", bin, args, stdin)
}

func (p *Publisher) push(ctx context.Context, dir, repo, head, branch string) error {
	args := append(p.httpsGitArgs(), "-c", "push.followTags=false", "push", "--porcelain", "--no-follow-tags", "--recurse-submodules=no", "--", "https://github.com/"+repo+".git", head+":refs/heads/"+branch)
	bin := p.GitBin
	if bin == "" {
		bin = "git"
	}
	_, err := p.command(ctx, dir, bin, args, nil)
	return err
}

func (p *Publisher) httpsGitArgs() []string {
	gh := p.GhBin
	if gh == "" {
		gh = "gh"
	}
	// Git evaluates credential helper shell snippets. Quote the binary as a
	// literal path; no credentials, URLs or source text enter this snippet.
	helper := "!'" + strings.ReplaceAll(gh, "'", "'\\''") + "' auth git-credential"
	return []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "credential.helper=", "-c", "credential.helper=" + helper, "-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "http.followRedirects=false"}
}

func (p *Publisher) command(ctx context.Context, dir, bin string, args []string, stdin []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if p.Run != nil {
		return p.Run(ctx, dir, bin, args, stdin)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.WaitDelay = 2 * time.Second
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "GIT_") || key == "GH_HOST" || key == "SSH_AUTH_SOCK" {
			continue
		}
		cmd.Env = append(cmd.Env, e)
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GH_PROMPT_DISABLED=1")
	var out, stderr cappedOutput
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("goal publication command failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if out.overflow || stderr.overflow {
		return nil, errors.New("goal publication response exceeded 1 MiB")
	}
	return out.Bytes(), nil
}

type cappedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := (1 << 20) - b.Len()
	if len(p) > left {
		b.overflow = true
		p = p[:max(0, left)]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
