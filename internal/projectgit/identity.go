package projectgit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/gitrepo"
)

// Remote contains display metadata only. A configured URL may contain a token;
// it must never cross the desktop binding or appear in an error.
type Remote struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	AvatarURL string `json:"avatarUrl"`
}

var githubOwner = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
var githubRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var remoteHost = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
var identitySHA = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func remoteMetadata(name, raw string) Remote {
	r := Remote{Name: name}
	raw = strings.TrimSpace(raw)
	var host string
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil {
			host = u.Hostname()
		}
	} else if before, _, ok := strings.Cut(raw, ":"); ok {
		_, host, ok = strings.Cut(before, "@")
		if !ok {
			host = before
		}
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if remoteHost.MatchString(host) {
		r.Host = host
	}
	if r.Host != "github.com" {
		return r
	}
	repo := gitrepo.ParseRemoteURL(raw)
	owner, namePart, ok := strings.Cut(repo, "/")
	if !ok || !githubOwner.MatchString(owner) || !githubRepo.MatchString(namePart) || namePart == "." || namePart == ".." {
		return r
	}
	r.Owner, r.Repo = owner, repo
	r.AvatarURL = "https://github.com/" + owner + ".png?size=40"
	return r
}

// Remotes reads fetch URLs locally, then discards the URLs after extracting
// validated display fields. Missing/unsupported remotes are not a Git error.
func (c Client) Remotes(ctx context.Context, dir string) []Remote {
	b, err := c.git(ctx, dir, "", "config", "--null", "--get-regexp", `^remote\..*\.url$`)
	result := []Remote{}
	if err != nil {
		return result
	}
	seen := map[string]bool{}
	for _, record := range strings.Split(string(b), "\x00") {
		key, value, ok := strings.Cut(record, "\n")
		if !ok || !strings.HasPrefix(key, "remote.") || !strings.HasSuffix(key, ".url") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, remoteMetadata(name, value))
	}
	return result
}

type AuthorIdentity struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
}
type Identities struct {
	Authors       map[string]AuthorIdentity `json:"authors"`
	RemoteAvatars map[string]string         `json:"remoteAvatars"`
}
type cachedIdentity struct {
	author  AuthorIdentity
	expires time.Time
}

// IdentityClient owns a small in-memory cache. Lookup failures have a shorter
// TTL so auth recovery is picked up, without retrying on every history refresh.
// No author names or email addresses are sent: GitHub resolves commit objects.
type IdentityClient struct {
	mu    sync.Mutex
	cache map[string]cachedIdentity
	run   func(context.Context, []byte) ([]byte, error)
	now   func() time.Time
}

func ValidIdentitySHAs(shas []string) bool {
	if len(shas) > 200 {
		return false
	}
	for _, sha := range shas {
		if !identitySHA.MatchString(sha) {
			return false
		}
	}
	return true
}

func validGitHubRepo(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	return ok && githubOwner.MatchString(owner) && githubRepo.MatchString(name) && name != "." && name != ".."
}

func safeAvatar(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "avatars.githubusercontent.com" || u.User != nil || u.Fragment != "" {
		return ""
	}
	return u.String()
}

func (c *IdentityClient) Lookup(ctx context.Context, repo string, shas []string) Identities {
	result := Identities{Authors: map[string]AuthorIdentity{}}
	if !validGitHubRepo(repo) || !ValidIdentitySHAs(shas) {
		return result
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	if c.cache == nil {
		c.cache = map[string]cachedIdentity{}
	}
	// Bound cache growth across many projects without keeping disk identity data.
	if len(c.cache) > 4096 {
		c.cache = map[string]cachedIdentity{}
	}
	missing := []string{}
	seen := map[string]bool{}
	for _, raw := range shas {
		sha := strings.ToLower(raw)
		if seen[sha] {
			continue
		}
		seen[sha] = true
		if cached, ok := c.cache[repo+"/"+sha]; ok && now.Before(cached.expires) {
			if cached.author.Login != "" || cached.author.AvatarURL != "" {
				result.Authors[sha] = cached.author
			}
		} else {
			missing = append(missing, sha)
		}
	}
	run := c.run
	if run == nil {
		run = runIdentityQuery
	}
	// A single request has at most 100 aliases, including null local-only commits.
	for start := 0; start < len(missing); start += 100 {
		end := min(start+100, len(missing))
		batch := missing[start:end]
		body := identityQuery(repo, batch)
		raw, err := run(ctx, body)
		// GraphQL can return useful data alongside an unresolved local commit.
		// gh exits nonzero for partial errors; keep its successfully resolved authors.
		values := parseIdentities(raw, batch)
		for _, sha := range batch {
			author := values[sha]
			ttl := 2 * time.Minute
			if author.Login != "" || author.AvatarURL != "" {
				ttl = 24 * time.Hour
				result.Authors[sha] = author
			}
			c.cache[repo+"/"+sha] = cachedIdentity{author: author, expires: now.Add(ttl)}
		}
		if err != nil || ctx.Err() != nil {
			// Cache the rest of this batch set too; a failed/offline gh should execute
			// once, not once per 100 missing commits on every render.
			for _, sha := range missing[end:] {
				c.cache[repo+"/"+sha] = cachedIdentity{expires: now.Add(2 * time.Minute)}
			}
			break
		}
	}
	return result
}

func identityQuery(repo string, shas []string) []byte {
	owner, name, _ := strings.Cut(repo, "/")
	var query strings.Builder
	query.WriteString("query($owner:String!,$name:String!){repository(owner:$owner,name:$name){")
	for i, sha := range shas {
		fmt.Fprintf(&query, "c%d:object(oid:%q){... on Commit{author{avatarUrl(size:40) user{login avatarUrl(size:40)}}}}", i, sha)
	}
	query.WriteString("}}")
	body, _ := json.Marshal(map[string]any{"query": query.String(), "variables": map[string]string{"owner": owner, "name": name}})
	return body
}

func parseIdentities(raw []byte, shas []string) map[string]AuthorIdentity {
	var response struct {
		Data struct {
			Repository map[string]*struct {
				Author *struct {
					AvatarURL string `json:"avatarUrl"`
					User      *struct {
						Login     string `json:"login"`
						AvatarURL string `json:"avatarUrl"`
					} `json:"user"`
				} `json:"author"`
			} `json:"repository"`
		} `json:"data"`
	}
	result := map[string]AuthorIdentity{}
	if json.Unmarshal(raw, &response) != nil {
		return result
	}
	for i, sha := range shas {
		commit := response.Data.Repository[fmt.Sprintf("c%d", i)]
		if commit == nil || commit.Author == nil {
			continue
		}
		author := AuthorIdentity{AvatarURL: safeAvatar(commit.Author.AvatarURL)}
		if user := commit.Author.User; user != nil {
			// GitHub bot names have a [bot] suffix; keep those identities too.
			login := strings.TrimSuffix(user.Login, "[bot]")
			if githubOwner.MatchString(login) {
				author.Login = user.Login
			}
			if avatar := safeAvatar(user.AvatarURL); avatar != "" {
				author.AvatarURL = avatar
			}
		}
		if author.Login != "" || author.AvatarURL != "" {
			result[sha] = author
		}
	}
	return result
}

func runIdentityQuery(ctx context.Context, body []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "api", "--hostname", "github.com", "graphql", "--input", "-")
	cmd.Stdin = bytes.NewReader(body)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_PAGER=cat")
	cmd.WaitDelay = 2 * time.Second
	var out boundedBuffer
	cmd.Stdout = &out
	// gh may emit repo/auth information; these are optional presentation details,
	// so errors never leave this call or replace the user's working Git state.
	err := cmd.Run()
	return out.Bytes(), err
}
