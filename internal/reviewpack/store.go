// Package reviewpack retains agent-provided review material independently of worktrees.
// It never executes tools or promotes a claim to verification evidence.
package reviewpack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	MaxManifest     = 64 << 10
	MaxImage        = 4 << 20
	MaxPackages     = 20
	MaxSessionBytes = 128 << 20
)

var digestRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
var commitRE = regexp.MustCompile(`^([a-f0-9]{40}|[a-f0-9]{64})$`)

type Manifest struct {
	Version int      `json:"version"`
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	Base    string   `json:"base"`
	Head    string   `json:"head"`
	Screens []Screen `json:"screens"`
}

// Before and After are checkout-relative paths on input, retained image IDs on output.
type Screen struct {
	Title    string `json:"title"`
	Caption  string `json:"caption,omitempty"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after"`
	URL      string `json:"url,omitempty"`
	Viewport string `json:"viewport,omitempty"`
	Selector string `json:"selector,omitempty"`
}

type Artifact struct {
	ID     string `json:"id"`
	MIME   string `json:"mime"`
	Bytes  int    `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type Package struct {
	Manifest
	ID        string     `json:"id"`
	Session   string     `json:"session"`
	Repo      string     `json:"repo"`
	PR        int        `json:"pr"`
	CreatedAt time.Time  `json:"createdAt"`
	Artifacts []Artifact `json:"artifacts"`
}

func Decode(raw []byte) (Manifest, error) {
	var m Manifest
	if len(raw) > MaxManifest {
		return m, errors.New("review manifest exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	if d.Decode(new(any)) != io.EOF {
		return m, errors.New("expected one review manifest")
	}
	return m, Validate(m)
}

func Validate(m Manifest) error {
	raw, err := json.Marshal(m)
	if err != nil || len(raw) > MaxManifest {
		return errors.New("review manifest exceeds 64 KiB")
	}
	if m.Version != 1 || strings.TrimSpace(m.Title) == "" || len(m.Title) > 200 || strings.TrimSpace(m.Body) == "" {
		return errors.New("review requires version 1, title (at most 200 bytes), and Markdown body")
	}
	if !commitRE.MatchString(m.Base) || !commitRE.MatchString(m.Head) {
		return errors.New("base and head must be full lowercase commit hashes")
	}
	if len(m.Screens) > 4 {
		return errors.New("at most four screenshot pairs per package")
	}
	for _, s := range m.Screens {
		if strings.TrimSpace(s.Title) == "" || len(s.Title) > 200 || len(s.Caption) > 2000 || len(s.URL) > 2048 || len(s.Viewport) > 100 || len(s.Selector) > 500 || s.After == "" {
			return errors.New("each screen requires a title and after image; metadata must be bounded")
		}
		for _, path := range []string{s.Before, s.After} {
			if path != "" && (!filepath.IsLocal(path) || filepath.Clean(path) != path) {
				return errors.New("image paths must be clean checkout-relative paths")
			}
		}
	}
	return nil
}

func hash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func sessionDir(home, session string) string {
	base, _, _ := strings.Cut(session, "~")
	return filepath.Join(home, "review-packages", hash([]byte(base)))
}

// Publish is serialized by the owning daemon. Exact retries return the original receipt.
func Publish(home, session, workspace, repo string, pr int, m Manifest) (Package, error) {
	var p Package
	if session == "" {
		return p, errors.New("review requires a session")
	}
	if err := Validate(m); err != nil {
		return p, err
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return p, err
	}
	defer root.Close()
	p = Package{Manifest: m, Session: session, Repo: repo, PR: pr, Artifacts: []Artifact{}}
	p.Screens = append([]Screen{}, m.Screens...)
	images := map[string][]byte{}
	for i := range p.Screens {
		for _, path := range []*string{&p.Screens[i].Before, &p.Screens[i].After} {
			if *path == "" {
				continue
			}
			data, err := readRegular(root, *path, MaxImage)
			if err != nil {
				return Package{}, fmt.Errorf("image %q: %w", *path, err)
			}
			cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil || (format != "png" && format != "jpeg") || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 20_000_000 {
				return Package{}, errors.New("screenshots must be PNG or JPEG, at most 20 megapixels and 4 MiB")
			}
			if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
				return Package{}, fmt.Errorf("invalid screenshot: %w", err)
			}
			id := hash(data)
			if _, exists := images[id]; !exists {
				images[id] = data
				p.Artifacts = append(p.Artifacts, Artifact{ID: id, MIME: "image/" + format, Bytes: len(data), Width: cfg.Width, Height: cfg.Height})
			}
			*path = id
		}
	}
	identity, _ := json.Marshal(p)
	p.ID = hash(identity)
	if old, err := Read(home, session, p.ID); err == nil {
		return old, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Package{}, err
	}
	baseSession, _, _ := strings.Cut(session, "~")
	prior, err := List(home, baseSession)
	if err != nil {
		return Package{}, err
	}
	total := 0
	for _, old := range prior {
		for _, a := range old.Artifacts {
			total += a.Bytes
		}
	}
	for _, data := range images {
		total += len(data)
	}
	if len(prior) >= MaxPackages || total > MaxSessionBytes {
		return Package{}, errors.New("retained review limit reached (20 packages or 128 MiB per session)")
	}
	dir := sessionDir(home, session)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Package{}, err
	}
	tmp, err := os.MkdirTemp(dir, ".publish-")
	if err != nil {
		return Package{}, err
	}
	defer os.RemoveAll(tmp)
	for id, data := range images {
		if err := os.WriteFile(filepath.Join(tmp, id), data, 0600); err != nil {
			return Package{}, err
		}
	}
	p.CreatedAt = time.Now().UTC()
	raw, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), raw, 0600); err != nil {
		return Package{}, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, p.ID)); err != nil {
		return Package{}, err
	}
	return p, nil
}

func readRegular(root *os.Root, path string, limit int) ([]byte, error) {
	if !filepath.IsLocal(path) || filepath.Clean(path) != path {
		return nil, errors.New("invalid relative path")
	}
	var prefix string
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		info, err := root.Lstat(prefix)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("symlinks are not accepted")
		}
	}
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > int64(limit) {
		return nil, errors.New("expected a nonempty bounded regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if len(b) > limit {
		return nil, errors.New("file exceeds size limit")
	}
	return b, err
}

func Read(home, session, id string) (Package, error) {
	var p Package
	if session == "" || !digestRE.MatchString(id) {
		return p, errors.New("invalid review identity")
	}
	root, err := os.OpenRoot(sessionDir(home, session))
	if err != nil {
		return p, err
	}
	defer root.Close()
	raw, err := readRegular(root, filepath.Join(id, "package.json"), MaxManifest+8192)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	withinSession := p.Session == session || (!strings.Contains(session, "~") && strings.HasPrefix(p.Session, session+"~"))
	if p.ID != id || !withinSession {
		return Package{}, errors.New("review identity mismatch")
	}
	identity := p
	identity.ID, identity.CreatedAt = "", time.Time{}
	encoded, _ := json.Marshal(identity)
	if hash(encoded) != id {
		return Package{}, errors.New("retained review metadata changed")
	}
	return p, nil
}

func List(home, session string) ([]Package, error) {
	result := []Package{}
	if session == "" {
		return result, errors.New("review requires a session")
	}
	entries, err := os.ReadDir(sessionDir(home, session))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !digestRE.MatchString(entry.Name()) {
			continue
		}
		base, _, _ := strings.Cut(session, "~")
		p, err := Read(home, base, entry.Name())
		if err != nil {
			return nil, err
		}
		if session == base || p.Session == session {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func Image(home, session, id, artifact string) ([]byte, string, error) {
	p, err := Read(home, session, id)
	if err != nil {
		return nil, "", err
	}
	for _, a := range p.Artifacts {
		if a.ID != artifact || !digestRE.MatchString(artifact) {
			continue
		}
		root, err := os.OpenRoot(sessionDir(home, session))
		if err != nil {
			return nil, "", err
		}
		defer root.Close()
		data, err := readRegular(root, filepath.Join(id, artifact), MaxImage)
		if err != nil {
			return nil, "", err
		}
		if hash(data) != artifact || len(data) != a.Bytes {
			return nil, "", errors.New("retained screenshot changed")
		}
		return data, a.MIME, nil
	}
	return nil, "", errors.New("screenshot is not part of this review package")
}
