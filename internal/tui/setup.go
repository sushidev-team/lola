// First-run setup wizard (P0/P6): a bubbletea flow that collects the Linear
// API key (validated live and stored in the Keychain), one [[project]], and
// the [defaults] caps/interval, then writes config.toml (0600) via
// config.Save. The key value is NEVER logged, written to config.toml, or
// placed in an error string — it goes to the Keychain (or, on failure, the
// user is told to export an env var), and the on-screen field masks all but
// its last 4 characters.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/secrets"
)

// The wizard always stores under this service and, on keychain failure, points
// the user at this env var (mirroring the shipped config defaults).
const (
	setupKeychainService = "lola-linear"
	setupEnvVar          = "LINEAR_API_KEY"
)

// setupRepoRe mirrors config's loose "owner/name" check so the repo step can
// give early feedback; config.Validate re-checks it at write time.
var setupRepoRe = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// Wizard steps, in order. stepConfirm writes the config.
const (
	stepKey = iota
	stepProjectPath
	stepRepo
	stepBranch
	stepConcurrency
	stepGlobalCap
	stepInterval
	stepConfirm
)

type keyValidatedMsg struct{ err error }

type setupModel struct {
	step     int
	endpoint string // Linear GraphQL endpoint (default on first run)

	key         string // Linear API key (masked in the view, never persisted)
	validating  bool
	keyErr      string // last validation error (never contains the key)
	keySource   string // "keychain" or "env" once the key is stored
	projectPath string
	repo        string
	branch      string
	concurrency string
	globalCap   string
	interval    string

	fieldErr string // per-step validation error for the non-key fields
	width    int
	height   int
	wrote    bool // a config was written (drives fall-through vs. quit)

	// Injectable seams so the wizard is hermetic in tests.
	validateKey func(ctx context.Context, endpoint, key string) error
	storeKey    func(service, key string) error
	gitToplevel func() string
	gitRemote   func(path string) string
}

func newSetupModel() *setupModel {
	return &setupModel{
		step:        stepKey,
		endpoint:    config.DefaultEndpoint,
		branch:      config.DefaultBranchName,
		concurrency: "2",
		globalCap:   "4",
		interval:    "60s",
		height:      24,
		validateKey: func(ctx context.Context, endpoint, key string) error {
			_, err := linear.New(endpoint, key).Viewer(ctx)
			return err
		},
		storeKey:    secrets.StoreLinearAPIKey,
		gitToplevel: gitToplevelCWD,
		gitRemote:   gitRemoteOrigin,
	}
}

// Setup runs the wizard unconditionally (the `lola setup` command). It prints a
// completion hint when a config was written; an esc-before-write is a silent
// no-op. The key never reaches stdout.
func Setup() error {
	wrote, err := runSetupWizard(newSetupModel())
	if err != nil {
		return err
	}
	if wrote {
		fmt.Println("setup complete — run `lola` to add a poll")
	}
	return nil
}

// runSetupWizard runs the given model to completion and reports whether it
// wrote a config.
func runSetupWizard(m *setupModel) (bool, error) {
	final, err := tea.NewProgram(m).Run() // alt-screen set on the View (bubbletea v2)
	if err != nil {
		return false, err
	}
	return final.(*setupModel).wrote, nil
}

func (m *setupModel) Init() tea.Cmd { return nil }

func (m *setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		return m, nil
	case tea.PasteMsg:
		if m.validating {
			// A key check is in flight; the buffer is locked. Dropping the paste
			// keeps it from mutating the value being validated out from under it.
			return m, nil
		}
		if buf := m.activeBuf(); buf != nil {
			// Every wizard step is single-line; an API key copied out of a
			// browser routinely carries a trailing newline.
			*buf += pasteInline(v.Content)
		}
		return m, nil
	case keyValidatedMsg:
		m.validating = false
		if v.err != nil {
			m.keyErr = v.err.Error() // Linear errors are status-based, never the key
			return m, nil
		}
		// Key is valid. Store it in the Keychain; on failure fall back to an
		// env var (never write the key into config.toml).
		if err := m.storeKey(setupKeychainService, m.key); err != nil {
			m.keySource = "env"
		} else {
			m.keySource = "keychain"
		}
		m.step = stepProjectPath
		if m.projectPath == "" {
			m.projectPath = m.gitToplevel()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(v)
	}
	return m, nil
}

func (m *setupModel) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// While the key is being validated, ignore edits; esc still cancels.
	if m.validating {
		if k.String() == "esc" || k.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	switch k.String() {
	case "esc", "ctrl+c":
		return m, tea.Quit // esc anywhere before the write quits without writing
	case "enter":
		return m.advance()
	}
	if buf := m.activeBuf(); buf != nil {
		editBuf(k, buf)
		m.fieldErr, m.keyErr = "", ""
	}
	return m, nil
}

// activeBuf returns a pointer to the text field the current step edits, or nil
// for the confirm step (which has no input).
func (m *setupModel) activeBuf() *string {
	switch m.step {
	case stepKey:
		return &m.key
	case stepProjectPath:
		return &m.projectPath
	case stepRepo:
		return &m.repo
	case stepBranch:
		return &m.branch
	case stepConcurrency:
		return &m.concurrency
	case stepGlobalCap:
		return &m.globalCap
	case stepInterval:
		return &m.interval
	}
	return nil
}

// editBuf applies one keystroke (printable text including space, or backspace)
// to buf. In bubbletea v2 the produced text is k.Text; a bracketed PASTE is a
// separate tea.PasteMsg and never reaches here — see the PasteMsg case in
// Update, which matters most on the key step (nobody types an API key).
func editBuf(k tea.KeyPressMsg, buf *string) {
	switch {
	case k.Code == tea.KeyBackspace:
		if *buf != "" {
			r := []rune(*buf)
			*buf = string(r[:len(r)-1])
		}
	case k.Text != "":
		*buf += k.Text
	}
}

// advance validates the current step and moves on; the key step kicks off live
// validation instead, and the confirm step writes the config.
func (m *setupModel) advance() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepKey:
		if strings.TrimSpace(m.key) == "" {
			m.keyErr = "enter your Linear API key"
			return m, nil
		}
		m.validating, m.keyErr = true, ""
		return m, m.validateCmd()
	case stepProjectPath:
		if strings.TrimSpace(m.projectPath) == "" {
			m.fieldErr = "project path is required"
			return m, nil
		}
		if m.repo == "" {
			m.repo = m.gitRemote(strings.TrimSpace(m.projectPath))
		}
		m.step, m.fieldErr = stepRepo, ""
	case stepRepo:
		if r := strings.TrimSpace(m.repo); r != "" && !setupRepoRe.MatchString(r) {
			m.fieldErr = `repo must be "owner/name" (e.g. sushidev-team/nori-app)`
			return m, nil
		}
		m.step, m.fieldErr = stepBranch, ""
	case stepBranch:
		if strings.TrimSpace(m.branch) == "" {
			m.branch = config.DefaultBranchName
		}
		m.step, m.fieldErr = stepConcurrency, ""
	case stepConcurrency:
		if !positiveInt(m.concurrency) {
			m.fieldErr = "concurrency_cap must be a positive integer"
			return m, nil
		}
		m.step, m.fieldErr = stepGlobalCap, ""
	case stepGlobalCap:
		if !positiveInt(m.globalCap) {
			m.fieldErr = "global_cap must be a positive integer"
			return m, nil
		}
		m.step, m.fieldErr = stepInterval, ""
	case stepInterval:
		if _, err := time.ParseDuration(strings.TrimSpace(m.interval)); err != nil {
			m.fieldErr = "poll_interval must be a duration (e.g. 60s)"
			return m, nil
		}
		m.step, m.fieldErr = stepConfirm, ""
	case stepConfirm:
		if err := m.write(); err != nil {
			m.fieldErr = err.Error()
			return m, nil
		}
		m.wrote = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *setupModel) validateCmd() tea.Cmd {
	endpoint, key, fn := m.endpoint, m.key, m.validateKey
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return keyValidatedMsg{err: fn(ctx, endpoint, key)}
	}
}

// write assembles the config and saves it 0600. The key is never included: the
// [linear] table records only where to find the key (keychain service or env
// var name), never the value.
func (m *setupModel) write() error {
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	interval, err := time.ParseDuration(strings.TrimSpace(m.interval))
	if err != nil {
		return err
	}
	conc, _ := strconv.Atoi(strings.TrimSpace(m.concurrency))
	gcap, _ := strconv.Atoi(strings.TrimSpace(m.globalCap))

	cfg := &config.Config{}
	cfg.Linear.Endpoint = m.endpoint
	if m.keySource == "keychain" {
		cfg.Linear.APIKeyKeychain = setupKeychainService
	} else {
		cfg.Linear.APIKeyEnv = setupEnvVar
	}
	cfg.Defaults.ConcurrencyCap = conc
	cfg.Defaults.GlobalCap = gcap
	cfg.Defaults.PollInterval = interval
	cfg.Projects = []config.Project{{
		Name:          m.projectName(),
		Path:          strings.TrimSpace(m.projectPath),
		Repo:          strings.TrimSpace(m.repo),
		DefaultBranch: strings.TrimSpace(m.branch),
	}}

	if err := cfg.Validate(); err != nil {
		return firstError(err)
	}
	return cfg.Save(path)
}

// projectName derives a [[project]] name from the repo (its "name" segment),
// falling back to the path's base directory.
func (m *setupModel) projectName() string {
	if r := strings.TrimSpace(m.repo); r != "" {
		if i := strings.LastIndex(r, "/"); i >= 0 && i < len(r)-1 {
			return r[i+1:]
		}
	}
	if p := strings.TrimSpace(m.projectPath); p != "" {
		return filepath.Base(p)
	}
	return "project"
}

// ---- view ----

func (m *setupModel) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	return v
}

func (m *setupModel) viewString() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("lola setup — first run") + "\n\n")

	rows := []struct {
		label string
		step  int
	}{
		{"Linear API key", stepKey},
		{"Project path", stepProjectPath},
		{"GitHub repo", stepRepo},
		{"Default branch", stepBranch},
		{"Concurrency cap", stepConcurrency},
		{"Global cap", stepGlobalCap},
		{"Poll interval", stepInterval},
	}
	for _, r := range rows {
		marker := "  "
		if r.step == m.step {
			marker = "› "
		}
		line := marker + fmt.Sprintf("%-18s", r.label) + m.fieldView(r.step)
		if r.step == m.step {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	confirm := "  [ Write config & finish ]"
	if m.step == stepConfirm {
		confirm = selStyle.Render("› [ Write config & finish ]")
	} else {
		confirm = faintText.Render(confirm)
	}
	b.WriteString("\n" + confirm + "\n\n")

	if m.validating {
		b.WriteString(faintText.Render("validating key with Linear…") + "\n")
	}
	if m.keySource != "" && m.step > stepKey {
		note := "key stored in macOS Keychain (service " + setupKeychainService + ")"
		if m.keySource == "env" {
			note = "keychain unavailable — export " + setupEnvVar + " with your key before running lola"
		}
		b.WriteString(faintText.Render(note) + "\n")
	}
	if m.keyErr != "" {
		b.WriteString(badText.Render("✗ "+m.keyErr) + "\n")
	}
	if m.fieldErr != "" {
		b.WriteString(badText.Render("✗ "+m.fieldErr) + "\n")
	}

	b.WriteString("\n" + faintText.Render("enter next · esc cancel (no config written)") + "\n")
	return b.String()
}

func (m *setupModel) fieldView(step int) string {
	active := step == m.step
	if step == stepKey {
		if m.key == "" {
			if active {
				return faintText.Render("(paste your Linear API key)")
			}
			return faintText.Render("(pending)")
		}
		return maskKey(m.key)
	}
	var raw string
	switch step {
	case stepProjectPath:
		raw = m.projectPath
	case stepRepo:
		raw = m.repo
	case stepBranch:
		raw = m.branch
	case stepConcurrency:
		raw = m.concurrency
	case stepGlobalCap:
		raw = m.globalCap
	case stepInterval:
		raw = m.interval
	}
	switch {
	case raw != "":
		return raw
	case step > m.step:
		return faintText.Render("(pending)")
	default:
		return faintText.Render("(type a value)")
	}
}

// maskKey shows only the last 4 characters of the key; everything before them
// renders as bullets so the secret is never displayed in full.
func maskKey(k string) string {
	r := []rune(k)
	if len(r) <= 4 {
		return string(r)
	}
	return strings.Repeat("•", len(r)-4) + string(r[len(r)-4:])
}

// ---- helpers ----

func positiveInt(s string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return err == nil && n > 0
}

// firstError reduces a (possibly errors.Join'd) error to its first line so the
// wizard shows one concise message.
func firstError(err error) error {
	return fmt.Errorf("%s", strings.SplitN(err.Error(), "\n", 2)[0])
}

// gitToplevelCWD returns the git worktree root of the current directory, or ""
// when the CWD is not inside a repo.
func gitToplevelCWD() string {
	out, err := exec.Command("git", "-C", ".", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitRemoteOrigin returns the "owner/name" parsed from path's origin remote, or
// "" when there is no repo/remote or it cannot be parsed.
func gitRemoteOrigin(path string) string {
	if path == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", path, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return parseGitRemote(strings.TrimSpace(string(out)))
}

// parseGitRemote extracts "owner/name" from the common GitHub remote forms:
//
//	git@github.com:owner/repo.git
//	https://github.com/owner/repo(.git)
//	ssh://git@github.com/owner/repo.git
//
// It returns "" for anything it does not recognize.
func parseGitRemote(url string) string {
	url = strings.TrimSpace(url)
	url = strings.TrimSuffix(url, ".git")
	if url == "" {
		return ""
	}
	// scp-like SSH: [user@]host:owner/repo
	if !strings.Contains(url, "://") {
		if i := strings.LastIndex(url, ":"); i >= 0 {
			return strings.Trim(url[i+1:], "/")
		}
		return ""
	}
	// URL form (https://, ssh://): take the last two path segments.
	rest := url[strings.Index(url, "://")+3:]
	if i := strings.Index(rest, "/"); i >= 0 {
		return strings.Trim(rest[i+1:], "/")
	}
	return ""
}
