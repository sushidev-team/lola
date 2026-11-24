// Package devlogin keeps named local test accounts outside session worktrees.
// Each profile points to its own Keychain item. Listing accounts needs no secret read.
package devlogin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/preview"
	"github.com/sushidev-team/lola/internal/secrets"
)

type Profile struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Agent    bool   `json:"agent"`
}

// AutoLogin is the single automatic account for this project, scoped to the
// scheme and hostname where the user enabled it. A missing selection is the default.
type AutoLogin struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
}

// Old selections stored a full origin including the dev server's port.
func (a AutoLogin) forHost() (AutoLogin, error) {
	u, err := preview.Allowed(a.Origin)
	if err != nil {
		return AutoLogin{}, err
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	a.Origin = u.Scheme + "://" + host
	return a, nil
}

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type record struct {
	Profile
	Version string `json:"version"`
}

type Store struct {
	dir string
}

var (
	lookup = secrets.Lookup
	store  = secrets.Store
	remove = secrets.Delete
	writes sync.Mutex // the desktop is the writer; CLI readers see atomic records
)

func ForProject(project string) (Store, error) {
	if project == "" {
		return Store{}, errors.New("test logins need a project")
	}
	home, err := config.Home()
	if err != nil {
		return Store{}, err
	}
	return Store{filepath.Join(home, "dev-logins", digest(project))}, nil
}

func digest(s string) string            { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
func (s Store) path(name string) string { return filepath.Join(s.dir, digest(name)+".json") }
func (s Store) service(r record) string {
	return "lola-dev-login-" + digest(s.path(r.Name)+"\x00"+r.Version)
}

func (s Store) read(name string) (record, error) {
	var r record
	data, err := os.ReadFile(s.path(name))
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil || r.Name != name || r.Version == "" {
		return r, errors.New("invalid saved test login")
	}
	return r, nil
}

func (s Store) List(agentOnly bool) ([]Profile, error) {
	out := []Profile{}
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var p Profile
		if json.Unmarshal(data, &p) != nil || filepath.Base(s.path(p.Name)) != e.Name() {
			return nil, errors.New("invalid saved test login")
		}
		if !agentOnly || p.Agent {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b Profile) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Save uses a fresh item when rotating a password. A failed metadata write leaves the
// old login intact. Empty password on edit keeps the existing item.
func (s Store) Save(p Profile, password string, editing bool) error {
	writes.Lock()
	defer writes.Unlock()
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 100 || strings.ContainsAny(p.Name, "\r\n\x00") || strings.TrimSpace(p.Username) == "" || strings.ContainsAny(p.Username+password, "\x00") {
		return errors.New("enter a login name and username")
	}
	old, err := s.read(p.Name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if editing && err != nil {
		return errors.New("this login no longer exists")
	}
	if !editing && err == nil {
		return errors.New("a login with that name already exists")
	}
	if !editing && password == "" {
		return errors.New("enter a password")
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	r := record{Profile: p, Version: old.Version}
	if password != "" {
		r.Version = rand.Text()
		// security(1) reads one line on stdin; encoding also preserves whitespace.
		if err := store(s.service(r), base64.StdEncoding.EncodeToString([]byte(password))); err != nil {
			return err
		}
	}
	if err := s.write(s.path(r.Name), r); err != nil {
		if password != "" {
			_ = remove(s.service(r))
		}
		return err
	}
	if old.Version != "" && old.Version != r.Version {
		return remove(s.service(old))
	}
	return nil
}

func (s Store) write(path string, value any) error {
	f, err := os.CreateTemp(s.dir, ".login-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(value)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (s Store) AutoLogin() (AutoLogin, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "auto-login"))
	if errors.Is(err, os.ErrNotExist) {
		return AutoLogin{}, nil
	}
	if err != nil {
		return AutoLogin{}, err
	}
	var choice AutoLogin
	if err := json.Unmarshal(data, &choice); err != nil {
		return AutoLogin{}, err
	}
	return choice.forHost()
}

func (s Store) SetAutoLogin(choice AutoLogin, enabled bool) error {
	writes.Lock()
	defer writes.Unlock()
	if !enabled {
		return s.clearAutoLogin(choice.Name)
	}
	if _, err := s.read(choice.Name); err != nil {
		return err
	}
	choice, err := choice.forHost()
	if err != nil {
		return err
	}
	return s.write(filepath.Join(s.dir, "auto-login"), choice)
}

// Called under writes. An old picker unchecking its row cannot clear a newer choice.
func (s Store) clearAutoLogin(name string) error {
	choice, err := s.AutoLogin()
	if err != nil || choice.Name == "" || choice.Name != name {
		return err
	}
	return os.Remove(filepath.Join(s.dir, "auto-login"))
}

func (s Store) AutoCredentials(choice AutoLogin) (*Credentials, error) {
	writes.Lock()
	defer writes.Unlock()
	choice, err := choice.forHost()
	if err != nil {
		return nil, err
	}
	current, err := s.AutoLogin()
	if err != nil || choice != current || choice.Name == "" {
		return nil, err
	}
	c, err := s.Credentials(choice.Name)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Credentials retrieves the one account explicitly selected in the desktop picker.
func (s Store) Credentials(name string) (Credentials, error) { return s.credentials(name, false) }

// Run supplies an opted-in account only to this test process and its children.
func (s Store) Run(name string, cmd *exec.Cmd) error {
	account, err := s.credentials(name, true)
	if err != nil {
		return err
	}
	cmd.Env = append(cmd.Environ(), "LOLA_TEST_USERNAME="+account.Username, "LOLA_TEST_PASSWORD="+account.Password)
	return cmd.Run()
}

func (s Store) credentials(name string, agentOnly bool) (Credentials, error) {
	r, err := s.read(name)
	if err != nil || (agentOnly && !r.Agent) {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Credentials{}, err
		}
		return Credentials{}, errors.New("test login unavailable")
	}
	raw, ok, err := lookup(s.service(r))
	if err != nil {
		return Credentials{}, err
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if !ok || err != nil || len(data) == 0 {
		return Credentials{}, errors.New("test password unavailable in Keychain")
	}
	return Credentials{r.Username, string(data)}, nil
}

func (s Store) Delete(name string) error {
	writes.Lock()
	defer writes.Unlock()
	r, err := s.read(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.clearAutoLogin(name); err != nil {
		return err
	}
	if err := remove(s.service(r)); err != nil {
		return err
	}
	return os.Remove(s.path(name))
}
