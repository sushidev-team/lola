package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sushidev-team/lola/desktop/internal/update"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// evtUpdateProgress carries download progress to the frontend during a DMG
// fetch. Registered in main.go's init alongside the daemon events.
const evtUpdateProgress = "update:download-progress"

// UpdateService is the bound bridge between the pure internal/update package and
// the frontend: it checks the public repo's GitHub Releases, streams the DMG
// download as events, and swaps the running .app. It holds the app so it can
// emit progress and quit for the install; SetApp wires it after the app exists.
type UpdateService struct {
	app     *application.App
	checker *update.Checker
}

// NewUpdateService builds the service with a fresh checker.
func NewUpdateService() *UpdateService {
	return &UpdateService{checker: update.NewChecker()}
}

// SetApp gives the service the emitter/quit handle (called once in main).
func (s *UpdateService) SetApp(app *application.App) { s.app = app }

// prefsPath resolves ~/.lola/desktop-update.json (honouring $LOLA_HOME). An
// empty return means "could not resolve home" — load/save then run in-memory so
// the UI still works, just without persistence.
func (s *UpdateService) prefsPath() string {
	home, err := config.Home()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "desktop-update.json")
}

func (s *UpdateService) loadPrefs() update.Prefs {
	p := s.prefsPath()
	if p == "" {
		return update.DefaultPrefs()
	}
	return update.LoadPrefs(p)
}

func (s *UpdateService) savePrefs(p update.Prefs) error {
	path := s.prefsPath()
	if path == "" {
		return nil // no home; nothing to persist to
	}
	return p.Save(path)
}

// --- DTOs (string timestamps so the generated TS bindings stay simple) -------

type ReleaseEntryDTO struct {
	Version      string `json:"version"`
	ReleaseNotes string `json:"releaseNotes"`
	PublishedAt  string `json:"publishedAt"`
}

type UpdateInfoDTO struct {
	Available      bool              `json:"available"`
	CurrentVersion string            `json:"currentVersion"`
	LatestVersion  string            `json:"latestVersion"`
	ReleaseNotes   string            `json:"releaseNotes"`
	PublishedAt    string            `json:"publishedAt"`
	DownloadURL    string            `json:"downloadURL"`
	BrowserURL     string            `json:"browserURL"`
	AssetName      string            `json:"assetName"`
	AssetSize      int64             `json:"assetSize"`
	Releases       []ReleaseEntryDTO `json:"releases"`
}

type UpdateSettingsDTO struct {
	AutoCheck          bool   `json:"autoCheck"`
	LastCheckTime      string `json:"lastCheckTime"`
	SkippedVersion     string `json:"skippedVersion,omitempty"`
	CheckIntervalHours int    `json:"checkIntervalHours"`
}

// UpdateProgressDTO is the payload of evtUpdateProgress.
type UpdateProgressDTO struct {
	TotalBytes      int64   `json:"totalBytes"`
	DownloadedBytes int64   `json:"downloadedBytes"`
	Percentage      float64 `json:"percentage"`
	Status          string  `json:"status"`
	Error           string  `json:"error,omitempty"`
	FilePath        string  `json:"filePath,omitempty"`
}

// GetVersion returns the compiled-in app version (the ldflag-injected
// main.version; "dev" for un-tagged local builds).
func (s *UpdateService) GetVersion() string { return version }

// CheckForUpdates queries the latest release and records the check time. It
// carries a skipped flag out via the DTO's Available field being left true —
// the frontend decides whether to surface it against the skipped version.
func (s *UpdateService) CheckForUpdates() (UpdateInfoDTO, error) {
	info, err := s.checker.CheckForUpdates(version)
	if err != nil {
		return UpdateInfoDTO{}, fmt.Errorf("failed to check for updates: %w", err)
	}

	prefs := s.loadPrefs()
	prefs.LastCheckTime = time.Now()
	_ = s.savePrefs(prefs) // best-effort; a failed persist must not fail the check

	releases := make([]ReleaseEntryDTO, 0, len(info.Releases))
	for _, r := range info.Releases {
		releases = append(releases, ReleaseEntryDTO{
			Version:      r.Version,
			ReleaseNotes: r.ReleaseNotes,
			PublishedAt:  r.PublishedAt.Format(time.RFC3339),
		})
	}

	return UpdateInfoDTO{
		Available:      info.Available,
		CurrentVersion: info.CurrentVersion,
		LatestVersion:  info.LatestVersion,
		ReleaseNotes:   info.ReleaseNotes,
		PublishedAt:    info.PublishedAt.Format(time.RFC3339),
		DownloadURL:    info.DownloadURL,
		BrowserURL:     info.BrowserURL,
		AssetName:      info.AssetName,
		AssetSize:      info.AssetSize,
		Releases:       releases,
	}, nil
}

// ShouldAutoCheck reports whether the startup auto-check is due (respects the
// AutoCheck toggle and the interval).
func (s *UpdateService) ShouldAutoCheck() bool {
	return s.loadPrefs().ShouldAutoCheck(time.Now())
}

// GetUpdateSettings returns the persisted preferences.
func (s *UpdateService) GetUpdateSettings() UpdateSettingsDTO {
	p := s.loadPrefs()
	last := ""
	if !p.LastCheckTime.IsZero() {
		last = p.LastCheckTime.Format(time.RFC3339)
	}
	return UpdateSettingsDTO{
		AutoCheck:          p.AutoCheck,
		LastCheckTime:      last,
		SkippedVersion:     p.SkippedVersion,
		CheckIntervalHours: p.CheckIntervalHours,
	}
}

// SetUpdateSettings persists the AutoCheck toggle and interval (last-check and
// skipped-version are owned by the check/skip paths, so they are preserved).
func (s *UpdateService) SetUpdateSettings(in UpdateSettingsDTO) error {
	p := s.loadPrefs()
	p.AutoCheck = in.AutoCheck
	if in.CheckIntervalHours > 0 {
		p.CheckIntervalHours = in.CheckIntervalHours
	}
	return s.savePrefs(p)
}

// SkipVersion records a version the user chose not to be nagged about.
func (s *UpdateService) SkipVersion(v string) error {
	p := s.loadPrefs()
	p.SkippedVersion = update.CleanVersion(v)
	return s.savePrefs(p)
}

// IsVersionSkipped reports whether v is the currently skipped version.
func (s *UpdateService) IsVersionSkipped(v string) bool {
	return s.loadPrefs().SkippedVersion == update.CleanVersion(v)
}

// GetRecentReleases returns the newest published releases for a changelog view.
func (s *UpdateService) GetRecentReleases(count int) ([]ReleaseEntryDTO, error) {
	entries, err := s.checker.GetRecentReleases(count)
	if err != nil {
		return nil, err
	}
	out := make([]ReleaseEntryDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, ReleaseEntryDTO{
			Version:      e.Version,
			ReleaseNotes: e.ReleaseNotes,
			PublishedAt:  e.PublishedAt.Format(time.RFC3339),
		})
	}
	return out, nil
}

// DownloadUpdate fetches the DMG at url into the user's Downloads folder,
// emitting evtUpdateProgress as it goes, and returns the saved path. It blocks
// until the download completes (the frontend drives it off progress events).
func (s *UpdateService) DownloadUpdate(url string) (string, error) {
	dest, err := downloadsFolder()
	if err != nil {
		return "", fmt.Errorf("failed to resolve downloads folder: %w", err)
	}

	progressChan, err := s.checker.DownloadUpdate(url, dest)
	if err != nil {
		return "", fmt.Errorf("failed to start download: %w", err)
	}

	var last update.DownloadProgress
	for p := range progressChan {
		last = p
		if s.app != nil {
			s.app.Event.Emit(evtUpdateProgress, UpdateProgressDTO{
				TotalBytes:      p.TotalBytes,
				DownloadedBytes: p.DownloadedBytes,
				Percentage:      p.Percentage,
				Status:          p.Status,
				Error:           p.Error,
				FilePath:        p.FilePath,
			})
		}
	}
	if last.Status == "error" {
		return "", fmt.Errorf("download failed: %s", last.Error)
	}
	return last.FilePath, nil
}

// InstallAndRestart mounts the DMG, stages the new bundle, launches the detached
// swap script, then quits so the script can replace and relaunch the app.
func (s *UpdateService) InstallAndRestart(dmgPath string) error {
	if err := update.InstallUpdate(dmgPath); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}
	if s.app != nil {
		s.app.Quit()
	}
	return nil
}

// downloadsFolder returns ~/Downloads, falling back to the OS temp dir.
func downloadsFolder() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir(), nil
	}
	return filepath.Join(home, "Downloads"), nil
}
