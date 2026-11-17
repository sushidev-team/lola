package projectgit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxAvatarBytes = 256 << 10
const maxIdentityEntries = 4096
const maxCachedAvatars = 512

type diskIdentity struct {
	Author    AuthorIdentity `json:"author"`
	Updated   int64          `json:"updated"`
	Ambiguous bool           `json:"ambiguous,omitempty"`
}
type identityDisk struct {
	Version int                     `json:"version"`
	Commits map[string]diskIdentity `json:"commits"`
	Emails  map[string]diskIdentity `json:"emails"`
	Images  map[string]int64        `json:"images"`
}

// PersistentIdentityClient stores verified GitHub identities and bounded raster
// files outside repositories. Cached never waits for network work. Each identity
// and image is refreshed at most once in this instance (the app's lifetime).
// Email keys are hashed and scoped to github.com/repository; email is never sent.
type PersistentIdentityClient struct {
	mu        sync.Mutex
	refreshMu sync.Mutex
	resolver  IdentityClient
	fetch     func(context.Context, string) ([]byte, error)
	dir       string
	disk      identityDisk
	refreshed map[string]bool
	fetched   map[string]bool
	images    map[string]string
}

func identityHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func identityKeys(repo, sha, email string) (string, string) {
	scope := "github.com/" + strings.ToLower(repo) + "\x00"
	email = strings.ToLower(strings.TrimSpace(email))
	emailKey := ""
	if email != "" && len(email) <= 320 {
		emailKey = identityHash(scope + "email\x00" + email)
	}
	return identityHash(scope + "commit\x00" + strings.ToLower(sha)), emailKey
}
func emptyIdentities() Identities {
	return Identities{Authors: map[string]AuthorIdentity{}, RemoteAvatars: map[string]string{}}
}

func (c *PersistentIdentityClient) initLocked(dir string) {
	if c.refreshed != nil {
		return
	}
	c.dir = dir
	c.refreshed = map[string]bool{}
	c.fetched = map[string]bool{}
	c.images = map[string]string{}
	c.disk = identityDisk{Version: 1, Commits: map[string]diskIdentity{}, Emails: map[string]diskIdentity{}, Images: map[string]int64{}}
	if dir == "" {
		return
	}
	b, err := readBounded(filepath.Join(dir, "identities.json"), 4<<20)
	if err != nil {
		return
	}
	var disk identityDisk
	if json.Unmarshal(b, &disk) != nil || disk.Version != 1 || len(disk.Commits) > maxIdentityEntries || len(disk.Emails) > maxIdentityEntries || len(disk.Images) > maxCachedAvatars {
		return
	}
	if disk.Commits != nil {
		c.disk.Commits = disk.Commits
	}
	if disk.Emails != nil {
		c.disk.Emails = disk.Emails
	}
	if disk.Images != nil {
		c.disk.Images = disk.Images
	}
}
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("cache file too large")
	}
	return b, nil
}
func validCachedAuthor(a AuthorIdentity) AuthorIdentity {
	if !githubOwner.MatchString(strings.TrimSuffix(a.Login, "[bot]")) {
		a.Login = ""
	}
	a.AvatarURL = safeAvatar(a.AvatarURL)
	return a
}
func (c *PersistentIdentityClient) identityLocked(repo, sha, email string) AuthorIdentity {
	key, emailKey := identityKeys(repo, sha, email)
	if entry, ok := c.disk.Commits[key]; ok {
		return validCachedAuthor(entry.Author)
	}
	if entry, ok := c.disk.Emails[emailKey]; ok && !entry.Ambiguous {
		return validCachedAuthor(entry.Author)
	}
	return AuthorIdentity{}
}
func (c *PersistentIdentityClient) imageLocked(raw string) string {
	if safeImageURL(raw) == "" {
		return ""
	}
	key := identityHash(raw)
	if value, ok := c.images[key]; ok {
		return value
	}
	value := ""
	if c.dir != "" {
		if b, err := readBounded(filepath.Join(c.dir, key+".image"), maxAvatarBytes); err == nil {
			value, _ = avatarData(b)
		}
	}
	c.images[key] = value
	return value
}
func (c *PersistentIdentityClient) cachedLocked(repo string, shas []string, emails map[string]string, remotes []Remote) Identities {
	result := emptyIdentities()
	if validGitHubRepo(repo) {
		for _, raw := range shas {
			sha := strings.ToLower(raw)
			author := c.identityLocked(repo, sha, emails[sha])
			if author.Login != "" || author.AvatarURL != "" {
				author.AvatarURL = c.imageLocked(author.AvatarURL)
				result.Authors[sha] = author
			}
		}
	}
	for _, remote := range remotes {
		if remote.Host == "github.com" && githubOwner.MatchString(remote.Owner) {
			result.RemoteAvatars[remote.Name] = c.imageLocked(remote.AvatarURL)
		}
	}
	return result
}
func (c *PersistentIdentityClient) Cached(dir, repo string, shas []string, emails map[string]string, remotes []Remote) Identities {
	if !ValidIdentitySHAs(shas) {
		return emptyIdentities()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked(dir)
	return c.cachedLocked(repo, shas, emails, remotes)
}

func (c *PersistentIdentityClient) Refresh(ctx context.Context, dir, repo string, shas []string, emails map[string]string, remotes []Remote) Identities {
	if !ValidIdentitySHAs(shas) {
		return emptyIdentities()
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.mu.Lock()
	c.initLocked(dir)
	query := []string{}
	knownEmails := map[string]bool{}
	if validGitHubRepo(repo) {
		for _, raw := range shas {
			sha := strings.ToLower(raw)
			key, emailKey := identityKeys(repo, sha, emails[sha])
			author := c.identityLocked(repo, sha, emails[sha])
			refreshKey := key
			// Once an email has a verified identity, one representative commit refreshes
			// it; additional local commits reuse it immediately without more API calls.
			if emailKey != "" && (author.Login != "" || author.AvatarURL != "") {
				refreshKey = emailKey
				if knownEmails[emailKey] {
					continue
				}
				knownEmails[emailKey] = true
			}
			if !c.refreshed[refreshKey] {
				c.refreshed[refreshKey] = true
				query = append(query, sha)
			}
		}
	}
	c.mu.Unlock()
	values := c.resolver.Lookup(ctx, repo, query)
	c.mu.Lock()
	now := time.Now().Unix()
	for sha, author := range values.Authors {
		key, emailKey := identityKeys(repo, sha, emails[sha])
		entry := diskIdentity{Author: author, Updated: now}
		// An avatar URL can change while the old image is still useful. Seed
		// the replacement cache key with the previous raster so failed refreshes
		// remain stale-while-revalidate even after the next process restart.
		previousAuthor := c.identityLocked(repo, sha, emails[sha])
		if author.AvatarURL != "" && previousAuthor.AvatarURL != author.AvatarURL && c.imageLocked(author.AvatarURL) == "" {
			stale := c.imageLocked(previousAuthor.AvatarURL)
			if _, encoded, ok := strings.Cut(stale, ";base64,"); ok {
				if b, err := base64.StdEncoding.DecodeString(encoded); err == nil {
					imageKey := identityHash(author.AvatarURL)
					c.images[imageKey] = stale
					if c.dir != "" && atomicCacheWrite(c.dir, imageKey+".image", b) == nil {
						c.disk.Images[imageKey] = now
					}
				}
			}
		}
		c.disk.Commits[key] = entry
		if emailKey != "" {
			previous, exists := c.disk.Emails[emailKey]
			if exists && (previous.Ambiguous || (previous.Author.Login != "" && author.Login != "" && previous.Author.Login != author.Login)) {
				entry.Ambiguous = true
			}
			c.disk.Emails[emailKey] = entry
			c.refreshed[emailKey] = true
		}
	}
	urls := map[string]bool{}
	for _, raw := range shas {
		author := c.identityLocked(repo, strings.ToLower(raw), emails[strings.ToLower(raw)])
		if author.AvatarURL != "" {
			urls[author.AvatarURL] = true
		}
	}
	for _, remote := range remotes {
		if remote.Host == "github.com" && githubOwner.MatchString(remote.Owner) && safeImageURL(remote.AvatarURL) != "" {
			urls[remote.AvatarURL] = true
		}
	}
	pending := []string{}
	for raw := range urls {
		key := identityHash(raw)
		if !c.fetched[key] {
			c.fetched[key] = true
			pending = append(pending, raw)
		}
	}
	c.saveLocked()
	c.mu.Unlock()
	// Download up to four small images concurrently; the enclosing request has
	// its own deadline, so a large author list cannot monopolize the app.
	jobs := make(chan string)
	var workers sync.WaitGroup
	for i := 0; i < min(4, len(pending)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for raw := range jobs {
				if ctx.Err() != nil {
					continue
				}
				fetch := c.fetch
				if fetch == nil {
					fetch = fetchAvatar
				}
				b, err := fetch(ctx, raw)
				if err != nil {
					continue
				}
				data, err := avatarData(b)
				if err != nil {
					continue
				}
				c.mu.Lock()
				key := identityHash(raw)
				c.images[key] = data
				if c.dir != "" && atomicCacheWrite(c.dir, key+".image", b) == nil {
					c.disk.Images[key] = time.Now().Unix()
				}
				c.mu.Unlock()
			}
		}()
	}
	for _, raw := range pending {
		jobs <- raw
	}
	close(jobs)
	workers.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	c.saveLocked()
	return c.cachedLocked(repo, shas, emails, remotes)
}
func (c *PersistentIdentityClient) pruneLocked() {
	prune := func(entries map[string]diskIdentity) {
		if len(entries) <= maxIdentityEntries {
			return
		}
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return entries[keys[i]].Updated < entries[keys[j]].Updated })
		for _, k := range keys[:len(keys)-maxIdentityEntries] {
			delete(entries, k)
		}
	}
	prune(c.disk.Commits)
	prune(c.disk.Emails)
	if len(c.disk.Images) > maxCachedAvatars {
		keys := make([]string, 0, len(c.disk.Images))
		for k := range c.disk.Images {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return c.disk.Images[keys[i]] < c.disk.Images[keys[j]] })
		for _, k := range keys[:len(keys)-maxCachedAvatars] {
			if len(k) == 64 {
				if _, err := hex.DecodeString(k); err == nil {
					_ = os.Remove(filepath.Join(c.dir, k+".image"))
				}
			}
			delete(c.disk.Images, k)
			delete(c.images, k)
		}
	}
}
func (c *PersistentIdentityClient) saveLocked() {
	if c.dir == "" {
		return
	}
	c.pruneLocked()
	b, err := json.Marshal(c.disk)
	if err == nil {
		_ = atomicCacheWrite(c.dir, "identities.json", b)
	}
}
func atomicCacheWrite(dir, name string, b []byte) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".avatar-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}
func safeImageURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return ""
	}
	switch u.Host {
	case "avatars.githubusercontent.com":
		return u.String()
	case "github.com":
		owner := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".png")
		if u.Path == "/"+owner+".png" && githubOwner.MatchString(owner) {
			return u.String()
		}
	}
	return ""
}
func avatarData(b []byte) (string, error) {
	if len(b) == 0 || len(b) > maxAvatarBytes {
		return "", errors.New("invalid avatar size")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 512 || cfg.Height > 512 {
		return "", errors.New("invalid avatar dimensions")
	}
	if _, _, err := image.Decode(bytes.NewReader(b)); err != nil {
		return "", errors.New("invalid avatar raster")
	}
	contentType := ""
	switch format {
	case "png":
		contentType = "image/png"
	case "jpeg":
		contentType = "image/jpeg"
	case "gif":
		contentType = "image/gif"
	default:
		return "", errors.New("unsupported avatar format")
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}
func fetchAvatar(ctx context.Context, raw string) ([]byte, error) {
	return fetchAvatarTransport(ctx, raw, nil)
}
func fetchAvatarTransport(ctx context.Context, raw string, transport http.RoundTripper) ([]byte, error) {
	if safeImageURL(raw) == "" {
		return nil, errors.New("unsupported avatar host")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client := http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || safeImageURL(req.URL.String()) == "" {
			return errors.New("unsupported avatar redirect")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/png, image/jpeg, image/gif")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("avatar unavailable")
	}
	kind, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || (kind != "image/png" && kind != "image/jpeg" && kind != "image/gif") {
		return nil, errors.New("unsupported avatar content")
	}
	if resp.ContentLength > maxAvatarBytes {
		return nil, errors.New("avatar too large")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes+1))
	if err != nil {
		return nil, err
	}
	data, err := avatarData(b)
	if err != nil || !strings.HasPrefix(data, "data:"+kind+";") {
		return nil, errors.New("avatar content does not match its type")
	}
	return b, nil
}

// AuthorEmails reads only local commit metadata; addresses never reach gh.
func (c Client) AuthorEmails(ctx context.Context, dir string, shas []string) map[string]string {
	emails := map[string]string{}
	if len(shas) == 0 || !ValidIdentitySHAs(shas) {
		return emails
	}
	args := []string{"log", "--no-walk=unsorted", "-z", "--format=%H%x00%ae"}
	args = append(args, shas...)
	args = append(args, "--")
	b, err := c.git(ctx, dir, "", args...)
	if err != nil {
		return emails
	}
	fields := strings.Split(string(b), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		if identitySHA.MatchString(fields[i]) {
			emails[strings.ToLower(fields[i])] = fields[i+1]
		}
	}
	return emails
}
