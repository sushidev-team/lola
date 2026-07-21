package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// GitHubAPIURL is the REST API base.
	GitHubAPIURL = "https://api.github.com"

	// RepoOwner / RepoName point at the SOURCE repo. This works only because the
	// repo is PUBLIC: /releases/latest and asset downloads are unauthenticated,
	// so the app needs no token. (rize-reporting keeps a separate *-releases repo
	// precisely because its source repo is private; lola does not need that.) If
	// the repo were made private, getLatestRelease would start returning 404 and
	// this const pair would have to point at a public mirror instead.
	RepoOwner = "sushidev-team"
	RepoName  = "lola"

	// CacheDuration bounds how often we hit the API within one app run.
	CacheDuration = 1 * time.Hour

	userAgent = "lola-desktop-updater"
)

// Checker fetches releases from GitHub with a small in-process cache. Safe for
// concurrent use.
type Checker struct {
	httpClient     *http.Client
	owner          string
	repo           string
	cachedRelease  *Release
	cacheTime      time.Time
	cachedReleases []Release
	cacheAllTime   time.Time
	mu             sync.RWMutex
}

// NewChecker returns a Checker aimed at the lola source repo.
func NewChecker() *Checker {
	return &Checker{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		owner:      RepoOwner,
		repo:       RepoName,
	}
}

// CheckForUpdates compares currentVersion against the latest release and, if a
// newer one exists, resolves the DMG to download and the intermediate changelog.
//
// A non-semver currentVersion (e.g. the "dev" default of an un-tagged build) is
// treated as "older than everything": IsNewerThan errors, so we fall back to
// offering the latest release rather than hiding it.
func (c *Checker) CheckForUpdates(currentVersion string) (*UpdateInfo, error) {
	release, err := c.getLatestRelease()
	if err != nil {
		return nil, fmt.Errorf("failed to get latest release: %w", err)
	}

	currentClean := CleanVersion(currentVersion)
	latestClean := CleanVersion(release.TagName)

	isNewer := false
	if IsValidVersion(currentClean) {
		isNewer, err = IsNewerThan(currentClean, latestClean)
		if err != nil {
			return nil, fmt.Errorf("failed to compare versions: %w", err)
		}
	} else {
		// Unknown running version → always offer the published release.
		isNewer = true
	}

	info := &UpdateInfo{
		Available:      isNewer,
		CurrentVersion: currentClean,
		LatestVersion:  latestClean,
		ReleaseNotes:   release.Body,
		PublishedAt:    release.PublishedAt,
		BrowserURL:     release.HTMLURL,
	}
	if asset := pickDMG(release.Assets); asset != nil {
		info.DownloadURL = asset.BrowserDownloadURL
		info.AssetName = asset.Name
		info.AssetSize = asset.Size
	}

	if isNewer && IsValidVersion(currentClean) {
		if releases, err := c.getReleasesBetween(currentClean, latestClean); err == nil {
			info.Releases = releases
		}
		// Non-fatal: the latest notes still populate ReleaseNotes above.
	}

	return info, nil
}

// pickDMG selects the DMG asset for this machine: the arch-specific one if
// present, else a universal DMG, else any .dmg at all. lola ships universal
// only, so the middle branch normally wins.
func pickDMG(assets []Asset) *Asset {
	var arch = GetArchitecture()
	var universal, any *Asset
	for i := range assets {
		a := &assets[i]
		if !strings.HasSuffix(strings.ToLower(a.Name), ".dmg") {
			continue
		}
		lower := strings.ToLower(a.Name)
		if strings.Contains(lower, arch) {
			return a
		}
		if strings.Contains(lower, "universal") {
			universal = a
		}
		if any == nil {
			any = a
		}
	}
	if universal != nil {
		return universal
	}
	return any
}

func (c *Checker) getLatestRelease() (*Release, error) {
	c.mu.RLock()
	if c.cachedRelease != nil && time.Since(c.cacheTime) < CacheDuration {
		release := c.cachedRelease
		c.mu.RUnlock()
		return release, nil
	}
	c.mu.RUnlock()

	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", GitHubAPIURL, c.owner, c.repo)
	var release Release
	if err := c.getJSON(url, &release); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cachedRelease = &release
	c.cacheTime = time.Now()
	c.mu.Unlock()
	return &release, nil
}

func (c *Checker) getAllReleases() ([]Release, error) {
	c.mu.RLock()
	if c.cachedReleases != nil && time.Since(c.cacheAllTime) < CacheDuration {
		releases := c.cachedReleases
		c.mu.RUnlock()
		return releases, nil
	}
	c.mu.RUnlock()

	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=50", GitHubAPIURL, c.owner, c.repo)
	var releases []Release
	if err := c.getJSON(url, &releases); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cachedReleases = releases
	c.cacheAllTime = time.Now()
	c.mu.Unlock()
	return releases, nil
}

// getJSON performs a GET and decodes into out, turning the GitHub error statuses
// the update path actually hits into readable messages.
func (c *Checker) getJSON(url string, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach GitHub: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusNotFound:
		return fmt.Errorf("no releases found (is the repository public and published?)")
	case http.StatusForbidden:
		return fmt.Errorf("rate limited by GitHub API, try again later")
	case http.StatusUnauthorized:
		return fmt.Errorf("authentication required (repository may be private)")
	default:
		return fmt.Errorf("GitHub API error (status %d)", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}

// getReleasesBetween returns every published release r with current < r <=
// latest, newest first.
func (c *Checker) getReleasesBetween(currentVersion, latestVersion string) ([]ReleaseEntry, error) {
	allReleases, err := c.getAllReleases()
	if err != nil {
		return nil, err
	}
	currentVer, err := ParseVersion(currentVersion)
	if err != nil {
		return nil, err
	}
	latestVer, err := ParseVersion(latestVersion)
	if err != nil {
		return nil, err
	}

	var entries []ReleaseEntry
	for _, r := range allReleases {
		if r.Draft || r.Prerelease {
			continue
		}
		tagClean := CleanVersion(r.TagName)
		rv, err := ParseVersion(tagClean)
		if err != nil {
			continue
		}
		if rv.GreaterThan(currentVer) && (rv.LessThan(latestVer) || rv.Equal(latestVer)) {
			entries = append(entries, ReleaseEntry{
				Version:      tagClean,
				ReleaseNotes: r.Body,
				PublishedAt:  r.PublishedAt,
			})
		}
	}
	return entries, nil
}

// GetRecentReleases returns the N newest published releases for a changelog view.
func (c *Checker) GetRecentReleases(count int) ([]ReleaseEntry, error) {
	allReleases, err := c.getAllReleases()
	if err != nil {
		return nil, err
	}
	var entries []ReleaseEntry
	for _, r := range allReleases {
		if r.Draft || r.Prerelease {
			continue
		}
		entries = append(entries, ReleaseEntry{
			Version:      CleanVersion(r.TagName),
			ReleaseNotes: r.Body,
			PublishedAt:  r.PublishedAt,
		})
		if len(entries) >= count {
			break
		}
	}
	return entries, nil
}

// DownloadUpdate streams the DMG at url into destDir, reporting progress on the
// returned channel and closing it when done. The final message carries either
// Status "complete" (with FilePath) or "error".
func (c *Checker) DownloadUpdate(url, destDir string) (<-chan DownloadProgress, error) {
	progressChan := make(chan DownloadProgress, 100)

	go func() {
		defer close(progressChan)

		parts := strings.Split(url, "/")
		filename := parts[len(parts)-1]
		destPath := filepath.Join(destDir, filename)

		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			progressChan <- DownloadProgress{Status: "error", Error: err.Error()}
			return
		}
		req.Header.Set("User-Agent", userAgent)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			progressChan <- DownloadProgress{Status: "error", Error: err.Error()}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			progressChan <- DownloadProgress{Status: "error", Error: fmt.Sprintf("download failed with status: %d", resp.StatusCode)}
			return
		}

		out, err := os.Create(destPath)
		if err != nil {
			progressChan <- DownloadProgress{Status: "error", Error: err.Error()}
			return
		}
		defer out.Close()

		totalSize := resp.ContentLength
		var downloaded int64
		buf := make([]byte, 32*1024)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := out.Write(buf[:n]); werr != nil {
					progressChan <- DownloadProgress{Status: "error", Error: werr.Error()}
					return
				}
				downloaded += int64(n)
				var pct float64
				if totalSize > 0 {
					pct = float64(downloaded) / float64(totalSize) * 100
				}
				progressChan <- DownloadProgress{
					TotalBytes:      totalSize,
					DownloadedBytes: downloaded,
					Percentage:      pct,
					Status:          "downloading",
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				progressChan <- DownloadProgress{Status: "error", Error: rerr.Error()}
				return
			}
		}

		progressChan <- DownloadProgress{
			TotalBytes:      totalSize,
			DownloadedBytes: downloaded,
			Percentage:      100,
			Status:          "complete",
			FilePath:        destPath,
		}
	}()

	return progressChan, nil
}

// ClearCache drops the cached release data so the next check re-fetches.
func (c *Checker) ClearCache() {
	c.mu.Lock()
	c.cachedRelease = nil
	c.cacheTime = time.Time{}
	c.cachedReleases = nil
	c.cacheAllTime = time.Time{}
	c.mu.Unlock()
}
