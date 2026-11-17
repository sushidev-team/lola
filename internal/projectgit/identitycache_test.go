package projectgit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func avatarPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: shade, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func authorResponse(login, url string) []byte {
	body := map[string]any{"data": map[string]any{"repository": map[string]any{"c0": map[string]any{"author": map[string]any{"user": map[string]string{"login": login, "avatarUrl": url}}}}}}
	b, _ := json.Marshal(body)
	return b
}
func TestPersistentIdentitiesCacheImagesAndReuseEmailWithoutNetwork(t *testing.T) {
	dir := t.TempDir()
	sha := strings.Repeat("a", 40)
	next := strings.Repeat("b", 40)
	url := "https://avatars.githubusercontent.com/u/1?s=40"
	email := "Developer@example.test"
	emails := map[string]string{sha: email, next: strings.ToLower(email)}
	png := avatarPNG(t, 80)
	queries, downloads := 0, 0
	c := PersistentIdentityClient{resolver: IdentityClient{run: func(context.Context, []byte) ([]byte, error) { queries++; return authorResponse("developer", url), nil }}, fetch: func(context.Context, string) ([]byte, error) { downloads++; return png, nil }}
	result := c.Cached(dir, "team/repo", []string{sha}, emails, nil)
	if len(result.Authors) != 0 || queries != 0 || downloads != 0 {
		t.Fatal("cache read performed network work")
	}
	result = c.Refresh(context.Background(), dir, "team/repo", []string{sha}, emails, nil)
	if queries != 1 || downloads != 1 || !strings.HasPrefix(result.Authors[sha].AvatarURL, "data:image/png;base64,") {
		t.Fatalf("result=%+v queries=%d downloads=%d", result, queries, downloads)
	}
	local := c.Cached(dir, "team/repo", []string{next}, emails, nil)
	if local.Authors[next] != result.Authors[sha] {
		t.Fatalf("email mapping unavailable: %+v", local)
	}
	c.Refresh(context.Background(), dir, "team/repo", []string{sha, next}, emails, nil)
	if queries != 1 || downloads != 1 {
		t.Fatalf("same-startup refetch: queries=%d downloads=%d", queries, downloads)
	}
	if got := c.Cached(dir, "other/repo", []string{next}, emails, nil); len(got.Authors) != 0 {
		t.Fatal("email identity escaped repository scope")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(email)) || bytes.Contains(raw, []byte(strings.ToLower(email))) {
		t.Fatal("raw email persisted")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.image"))
	if len(files) != 1 {
		t.Fatalf("image files=%v", files)
	}
	info, _ := os.Stat(files[0])
	if info.Mode().Perm() != 0600 {
		t.Fatalf("image mode=%v", info.Mode())
	}
	restarted := PersistentIdentityClient{resolver: IdentityClient{run: func(context.Context, []byte) ([]byte, error) { queries++; return nil, errors.New("offline") }}, fetch: func(context.Context, string) ([]byte, error) { downloads++; return nil, errors.New("offline") }}
	cached := restarted.Cached(dir, "team/repo", []string{next}, emails, nil)
	if cached.Authors[next] != result.Authors[sha] {
		t.Fatal("persisted email/avatar missing after restart")
	}
	if queries != 1 || downloads != 1 {
		t.Fatal("startup cache read used network")
	}
	stale := restarted.Refresh(context.Background(), dir, "team/repo", []string{next}, emails, nil)
	if stale.Authors[next] != cached.Authors[next] || queries != 2 || downloads != 2 {
		t.Fatalf("stale not retained: %+v", stale)
	}
	restarted.Refresh(context.Background(), dir, "team/repo", []string{next}, emails, nil)
	if queries != 2 || downloads != 2 {
		t.Fatal("failed startup refresh repeated")
	}
}
func TestCachedIdentitiesReturnWhileRefreshIsPendingAndReplaceWhenReady(t *testing.T) {
	dir := t.TempDir()
	sha := strings.Repeat("a", 40)
	url := "https://avatars.githubusercontent.com/u/1"
	email := map[string]string{sha: "a@example.test"}
	old := avatarPNG(t, 1)
	newImage := avatarPNG(t, 2)
	first := PersistentIdentityClient{resolver: IdentityClient{run: func(context.Context, []byte) ([]byte, error) { return authorResponse("developer", url), nil }}, fetch: func(context.Context, string) ([]byte, error) { return old, nil }}
	previous := first.Refresh(context.Background(), dir, "team/repo", []string{sha}, email, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	next := PersistentIdentityClient{resolver: IdentityClient{run: func(context.Context, []byte) ([]byte, error) { return authorResponse("developer", url), nil }}, fetch: func(context.Context, string) ([]byte, error) { close(started); <-release; return newImage, nil }}
	done := make(chan Identities, 1)
	go func() { done <- next.Refresh(context.Background(), dir, "team/repo", []string{sha}, email, nil) }()
	<-started
	cached := make(chan Identities, 1)
	go func() { cached <- next.Cached(dir, "team/repo", []string{sha}, email, nil) }()
	select {
	case got := <-cached:
		if got.Authors[sha] != previous.Authors[sha] {
			t.Fatal("stale avatar disappeared")
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("cache read blocked on refresh")
	}
	close(release)
	fresh := <-done
	if fresh.Authors[sha].AvatarURL == previous.Authors[sha].AvatarURL {
		t.Fatal("refreshed image not delivered")
	}
}
func TestRemoteAvatarWithoutCommitsAndSharedDownload(t *testing.T) {
	dir := t.TempDir()
	var requests atomic.Int32
	png := avatarPNG(t, 20)
	remotes := []Remote{remoteMetadata("origin", "git@github.com:team/repo.git"), remoteMetadata("upstream", "git@github.com:team/other.git")}
	c := PersistentIdentityClient{fetch: func(context.Context, string) ([]byte, error) { requests.Add(1); return png, nil }}
	result := c.Refresh(context.Background(), dir, "team/repo", nil, nil, remotes)
	if requests.Load() != 1 || result.RemoteAvatars["origin"] == "" || result.RemoteAvatars["origin"] != result.RemoteAvatars["upstream"] {
		t.Fatalf("result=%+v requests=%d", result, requests.Load())
	}
	c.Refresh(context.Background(), dir, "team/repo", nil, nil, remotes)
	if requests.Load() != 1 {
		t.Fatal("remote image refetched")
	}
	next := PersistentIdentityClient{}
	if got := next.Cached(dir, "team/repo", nil, nil, remotes); got.RemoteAvatars["origin"] != result.RemoteAvatars["origin"] {
		t.Fatal("remote image not persisted")
	}
}
func TestAvatarValidationAndCorruptDiskFailClosed(t *testing.T) {
	for _, raw := range []string{"http://avatars.githubusercontent.com/u/1", "https://avatars.githubusercontent.com.evil/u/1", "https://user@avatars.githubusercontent.com/u/1", "https://avatars.githubusercontent.com:444/u/1", "https://github.com/team/repo", "https://github.com/team.png#fragment"} {
		if safeImageURL(raw) != "" {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, b := range [][]byte{[]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), bytes.Repeat([]byte("a"), maxAvatarBytes+1)} {
		if _, err := avatarData(b); err == nil {
			t.Fatal("accepted unsafe raster")
		}
	}
	oversized := image.NewRGBA(image.Rect(0, 0, 513, 1))
	var out bytes.Buffer
	png.Encode(&out, oversized)
	if _, err := avatarData(out.Bytes()); err == nil {
		t.Fatal("accepted oversized dimensions")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "identities.json"), []byte("corrupt"), 0600)
	c := PersistentIdentityClient{}
	if got := c.Cached(dir, "team/repo", []string{strings.Repeat("a", 40)}, nil, nil); len(got.Authors) != 0 {
		t.Fatal("corrupt cache yielded identity")
	}
}
func TestAvatarURLChangeKeepsStaleImageAcrossFailedRefreshAndRestart(t *testing.T) {
	dir := t.TempDir()
	sha := strings.Repeat("a", 40)
	emails := map[string]string{sha: "a@example.test"}
	oldURL := "https://avatars.githubusercontent.com/u/1?v=1"
	newURL := "https://avatars.githubusercontent.com/u/1?v=2"
	png := avatarPNG(t, 40)
	first := PersistentIdentityClient{resolver: IdentityClient{run: func(context.Context, []byte) ([]byte, error) { return authorResponse("developer", oldURL), nil }}, fetch: func(context.Context, string) ([]byte, error) { return png, nil }}
	original := first.Refresh(context.Background(), dir, "team/repo", []string{sha}, emails, nil)
	second := PersistentIdentityClient{resolver: IdentityClient{run: func(context.Context, []byte) ([]byte, error) { return authorResponse("developer", newURL), nil }}, fetch: func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }}
	result := second.Refresh(context.Background(), dir, "team/repo", []string{sha}, emails, nil)
	if result.Authors[sha].AvatarURL != original.Authors[sha].AvatarURL {
		t.Fatal("URL change dropped stale image")
	}
	third := PersistentIdentityClient{}
	if result = third.Cached(dir, "team/repo", []string{sha}, emails, nil); result.Authors[sha].AvatarURL != original.Authors[sha].AvatarURL {
		t.Fatal("URL change lost stale image on restart")
	}
}
func TestAuthorEmailsReadExactLocalCommits(t *testing.T) {
	dir := repo(t)
	testGit(t, dir, "commit", "--allow-empty", "-m", "first")
	sha := testGit(t, dir, "rev-parse", "HEAD")
	got := (Client{}).AuthorEmails(context.Background(), dir, []string{sha})
	if got[sha] != "workspace@example.test" {
		t.Fatalf("emails=%v", got)
	}
}

func TestFailedUncachedAvatarAttemptDoesNotRepeatThisStartup(t *testing.T) {
	dir := t.TempDir()
	sha := strings.Repeat("a", 40)
	queries, images := 0, 0
	c := PersistentIdentityClient{resolver: IdentityClient{run: func(context.Context, []byte) ([]byte, error) {
		queries++
		return authorResponse("developer", "https://avatars.githubusercontent.com/u/1"), nil
	}}, fetch: func(context.Context, string) ([]byte, error) { images++; return nil, errors.New("offline") }}
	for i := 0; i < 3; i++ {
		result := c.Refresh(context.Background(), dir, "team/repo", []string{sha}, map[string]string{sha: "dev@example.test"}, nil)
		if result.Authors[sha].Login != "developer" || result.Authors[sha].AvatarURL != "" {
			t.Fatalf("bad fallback=%+v", result)
		}
	}
	if queries != 1 || images != 1 {
		t.Fatalf("failed image repeated queries=%d images=%d", queries, images)
	}
}

type avatarRoundTrip func(*http.Request) (*http.Response, error)

func (f avatarRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestAvatarHTTPRefusesRedirectHostWrongMIMEAndOversize(t *testing.T) {
	raster := avatarPNG(t, 100)
	for _, tc := range []struct {
		name   string
		header http.Header
		status int
		body   []byte
	}{
		{"redirect", http.Header{"Location": []string{"https://tracker.example/avatar"}}, 302, nil},
		{"svg", http.Header{"Content-Type": []string{"image/svg+xml"}}, 200, []byte("<svg/>")},
		{"mismatch", http.Header{"Content-Type": []string{"image/jpeg"}}, 200, raster},
		{"oversize", http.Header{"Content-Type": []string{"image/png"}}, 200, bytes.Repeat([]byte("x"), maxAvatarBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			transport := avatarRoundTrip(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Host != "avatars.githubusercontent.com" {
					t.Fatalf("contacted %s", req.URL.Host)
				}
				return &http.Response{StatusCode: tc.status, Header: tc.header, Body: io.NopCloser(bytes.NewReader(tc.body)), ContentLength: int64(len(tc.body)), Request: req}, nil
			})
			if _, err := fetchAvatarTransport(context.Background(), "https://avatars.githubusercontent.com/u/1", transport); err == nil {
				t.Fatal("unsafe response accepted")
			}
			if calls != 1 {
				t.Fatalf("requests=%d", calls)
			}
		})
	}
}
