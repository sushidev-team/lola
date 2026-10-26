// First-run setup wizard (P0/P6): a bubbletea flow that collects the Linear
// API key (validated live and stored in the Keychain) and optional workspace
// defaults, then writes config.toml (0600) via
// config.Save. The key value is NEVER logged, written to config.toml, or
// placed in an error string — it goes to the Keychain (or, on failure, the
// user is told to export an env var), and the on-screen field masks all but
// its last 4 characters.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/agent"
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

// Wizard steps, in order. stepConfirm writes the config.
const (
	stepKey = iota
	stepAgent
	stepTheme
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
	agent       string
	theme       string
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
}

func newSetupModel() *setupModel {
	return &setupModel{
		step:        stepKey,
		endpoint:    config.DefaultEndpoint,
		agent:       config.DefaultAgent,
		theme:       config.DefaultUITheme,
		concurrency: "2",
		globalCap:   "4",
		interval:    "60s",
		height:      24,
		validateKey: func(ctx context.Context, endpoint, key string) error {
			_, err := linear.New(endpoint, key).Viewer(ctx)
			return err
		},
		storeKey: secrets.StoreLinearAPIKey,
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
		fmt.Println("setup complete — run `lola` and press n to add a project")
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
			m.fieldErr, m.keyErr = "", ""
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
		// Only the key is required. Focus Finish; optional defaults remain
		// editable with the arrow keys without walking every field first.
		m.key = ""
		m.step = stepConfirm
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
		return m, tea.Quit
	case "enter":
		return m.advance()
	case "up", "shift+tab":
		if m.keySource != "" && m.step > stepAgent {
			m.step--
		}
		return m, nil
	case "down", "tab":
		if m.keySource != "" && m.step < stepConfirm {
			m.step++
		}
		return m, nil
	case "left", "right", "space":
		if m.step == stepAgent || m.step == stepTheme {
			choices, value := config.UIThemes, &m.theme
			if m.step == stepAgent {
				choices = nil
				for _, kind := range agent.Kinds {
					choices = append(choices, string(kind))
				}
				value = &m.agent
			}
			direction := 1
			if k.String() == "left" {
				direction = -1
			}
			for i, choice := range choices {
				if choice == *value {
					*value = choices[(i+direction+len(choices))%len(choices)]
					break
				}
			}
			return m, nil
		}
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
		m.key = strings.TrimSpace(m.key)
		m.validating, m.keyErr = true, ""
		return m, m.validateCmd()
	case stepAgent, stepTheme:
		m.step++
	case stepConcurrency:
		if strings.TrimSpace(m.concurrency) != "" && !positiveInt(m.concurrency) {
			m.fieldErr = "Agents per project must be a positive whole number."
			return m, nil
		}
		m.step, m.fieldErr = stepGlobalCap, ""
	case stepGlobalCap:
		if strings.TrimSpace(m.globalCap) != "" && !positiveInt(m.globalCap) {
			m.fieldErr = "Total running agents must be a positive whole number."
			return m, nil
		}
		m.step, m.fieldErr = stepInterval, ""
	case stepInterval:
		if value := strings.TrimSpace(m.interval); value != "" {
			duration, err := time.ParseDuration(value)
			if err != nil || duration < config.MinPollInterval {
				m.fieldErr = "poll interval must be a duration of at least 30s"
				return m, nil
			}
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
	if m.keySource != "keychain" && m.keySource != "env" {
		return fmt.Errorf("validate your Linear API key first")
	}
	interval := config.DefaultPollInterval
	if value := strings.TrimSpace(m.interval); value != "" {
		interval, err = time.ParseDuration(value)
		if err != nil || interval < config.MinPollInterval {
			return fmt.Errorf("poll interval must be a duration of at least 30s")
		}
	}
	conc, gcap := 2, 4
	for _, field := range []struct {
		text  string
		value *int
	}{{m.concurrency, &conc}, {m.globalCap, &gcap}} {
		if value := strings.TrimSpace(field.text); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return fmt.Errorf("agent limits must be positive whole numbers")
			}
			*field.value = n
		}
	}
	// Preserve existing repositories and unrelated settings on an explicit rerun.
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg.Linear.Endpoint = m.endpoint
	cfg.Linear.APIKeyKeychain, cfg.Linear.APIKeyEnv = "", ""
	if m.keySource == "keychain" {
		cfg.Linear.APIKeyKeychain = setupKeychainService
	} else {
		cfg.Linear.APIKeyEnv = setupEnvVar
	}
	cfg.Defaults.ConcurrencyCap = conc
	cfg.Defaults.GlobalCap = gcap
	cfg.Defaults.PollInterval = interval
	cfg.Defaults.Agent = m.agent
	cfg.UI.Theme = m.theme

	if err := cfg.Validate(); err != nil {
		return firstError(err)
	}
	return cfg.Save(path)
}

// ---- view ----

func (m *setupModel) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	return v
}

func (m *setupModel) viewString() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	wrap := func(text string) []string { return strings.Split(lipgloss.NewStyle().Width(w).Render(text), "\n") }
	var body []string
	var rowStarts, rowEnds []int
	focus := 0
	for _, row := range []struct {
		label string
		step  int
	}{
		{"Linear API key", stepKey}, {"Default agent", stepAgent}, {"Theme", stepTheme},
		{"Agents per project", stepConcurrency}, {"Total running agents", stepGlobalCap}, {"Poll interval", stepInterval},
	} {
		if row.step == stepAgent {
			body = append(body, "", faintText.Render("General settings (optional)"))
		}
		rowStarts = append(rowStarts, len(body))
		marker := "  "
		if row.step == m.step {
			marker = "> "
		}
		line := marker + fmt.Sprintf("%-22s", row.label) + m.fieldView(row.step)
		if w < 60 {
			line = marker + row.label + "\n  " + m.fieldView(row.step)
		}
		if row.step == m.step {
			line = selStyle.Render(line)
		}
		body = append(body, wrap(line)...)
		rowEnds = append(rowEnds, len(body))
		if row.step == m.step {
			focus = len(body) - 1
		}
	}
	finish := "  [ Start Lola ]"
	if m.step == stepConfirm {
		finish = selStyle.Render("> [ Start Lola ]")
	}
	body = append(body, "", finish)
	rowStarts = append(rowStarts, len(body)-1)
	rowEnds = append(rowEnds, len(body))
	if m.step == stepConfirm {
		focus = len(body) - 1
	}

	var footer []string
	if m.validating {
		footer = append(footer, wrap("Validating key with Linear…")...)
	}
	if m.keySource == "keychain" {
		footer = append(footer, wrap(goodText.Render("Linear connected · key in Keychain"))...)
	}
	if m.keySource == "env" {
		footer = append(footer, wrap(warnText.Render("Keychain unavailable — export "+setupEnvVar+" before running Lola."))...)
	}
	if m.keyErr != "" {
		footer = append(footer, wrap(badText.Render(m.keyErr))...)
	}
	if m.fieldErr != "" {
		footer = append(footer, wrap(badText.Render(m.fieldErr))...)
	}
	hint := "enter connect · esc cancel"
	if m.keySource != "" {
		hint = "↑/↓ field · ←/→ choice · enter next/start · esc cancel"
	}
	footer = append(footer, wrap(faintText.Render(hint))...)
	header := []string{titleStyle.Render("Lola setup"), ""}
	if m.height > 0 {
		available := max(1, m.height-len(header)-len(footer)-1)
		start := max(0, focus-available+1)
		// Keep field labels with their values when compact layouts scroll.
		if start > 0 {
			for _, row := range rowStarts {
				if row >= start && row <= focus {
					start = row
					break
				}
			}
		}
		end := min(len(body), start+available)
		limit := end
		for _, row := range rowEnds {
			if row > focus && row <= limit {
				end = row
			}
		}
		body = body[start:end]
	}
	lines := append(header, body...)
	lines = append(lines, "")
	return strings.Join(append(lines, footer...), "\n")
}

func (m *setupModel) fieldView(step int) string {
	if step == stepKey {
		if m.keySource != "" {
			return "Connected"
		}
		if m.key == "" {
			return "(paste your Linear API key)"
		}
		return maskKey(m.key)
	}
	switch step {
	case stepAgent:
		return m.agent
	case stepTheme:
		return m.theme
	case stepConcurrency:
		if strings.TrimSpace(m.concurrency) == "" {
			return "2 (default)"
		}
		return m.concurrency
	case stepGlobalCap:
		if strings.TrimSpace(m.globalCap) == "" {
			return "4 (default)"
		}
		return m.globalCap
	case stepInterval:
		if strings.TrimSpace(m.interval) == "" {
			return "60s (default)"
		}
		return m.interval
	}
	return ""
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
