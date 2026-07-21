package update

import (
	"runtime"
	"time"
)

// ReleaseEntry is one version's changelog line, surfaced so the overlay can show
// every release between the running version and the latest (not just the newest
// notes).
type ReleaseEntry struct {
	Version      string    `json:"version"`
	ReleaseNotes string    `json:"releaseNotes"`
	PublishedAt  time.Time `json:"publishedAt"`
}

// UpdateInfo is the result of a check: whether a newer release exists, the
// download to fetch, and the intermediate changelog.
type UpdateInfo struct {
	Available      bool           `json:"available"`
	CurrentVersion string         `json:"currentVersion"`
	LatestVersion  string         `json:"latestVersion"`
	ReleaseNotes   string         `json:"releaseNotes"`
	PublishedAt    time.Time      `json:"publishedAt"`
	DownloadURL    string         `json:"downloadURL"`
	BrowserURL     string         `json:"browserURL"`
	AssetName      string         `json:"assetName"`
	AssetSize      int64          `json:"assetSize"`
	Releases       []ReleaseEntry `json:"releases"` // current < v <= latest, newest first
}

// Release mirrors the fields the GitHub Releases API returns that we care about.
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []Asset   `json:"assets"`
}

// Asset is one uploaded file on a release (we only ever consume the .dmg).
type Asset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ContentType        string `json:"content_type"`
}

// DownloadProgress is streamed over the update channel and re-emitted to the
// frontend as it downloads.
type DownloadProgress struct {
	TotalBytes      int64   `json:"totalBytes"`
	DownloadedBytes int64   `json:"downloadedBytes"`
	Percentage      float64 `json:"percentage"`
	Status          string  `json:"status"` // "downloading" | "complete" | "error"
	Error           string  `json:"error,omitempty"`
	FilePath        string  `json:"filePath,omitempty"`
}

// GetArchitecture maps GOARCH to the token used in DMG asset names. lola-desktop
// ships a single universal DMG today, so this is a fallback discriminator kept
// for the day per-arch DMGs return — universal always matches (see pickDMG).
func GetArchitecture() string {
	switch runtime.GOARCH {
	case "amd64":
		return "intel"
	case "arm64":
		return "apple-silicon"
	default:
		return "universal"
	}
}
