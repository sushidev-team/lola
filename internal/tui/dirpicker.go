// Folder browser overlay. Adding a project starts with picking its checkout —
// everything else on the Repo tab (id, label, GitHub repo, default branch) is
// derived from it — so the TUI needs the terminal equivalent of the app's native
// directory chooser rather than a bare "type an absolute path" field.
//
// It is deliberately separate from form.go's `picker`: that one selects an id
// out of a fixed option list loaded from Linear, this one walks a filesystem
// that only ever loads one directory at a time.
package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/config"
)

type dirPickerEvent int

const (
	dirPickerNone dirPickerEvent = iota
	dirPickerCancel
	dirPickerChosen
)

// dirEntry is one subdirectory of the directory being browsed. isRepo marks a
// git checkout, which is what the browser is looking for: enter CHOOSES one
// instead of descending into it, so the common case is a single keystroke.
type dirEntry struct {
	name   string
	isRepo bool
}

type dirPicker struct {
	dir     string // absolute path being browsed
	entries []dirEntry
	filter  string // type-to-narrow, matched case-insensitively
	cursor  int
	err     string
	loading bool
	hidden  bool // show dot-directories (ctrl+t)

	// chosen holds the selected path once update reports dirPickerChosen.
	chosen string

	// want is the entry to put the cursor on once the pending load lands — the
	// directory we just came UP out of, so going back down is symmetrical.
	want string
}

// dirLoadedMsg carries one directory listing back to whichever model owns the
// browser. dir identifies the request, so a slow listing that lands after the
// user has moved on is discarded rather than replacing the current view.
type dirLoadedMsg struct {
	dir     string
	entries []dirEntry
	err     string
}

// newDirPicker opens the browser at start (falling back to $HOME when start is
// unusable) and returns the command that loads it.
func newDirPicker(start string) (*dirPicker, tea.Cmd) {
	dir := strings.TrimSpace(start)
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	p := &dirPicker{dir: dir, loading: true}
	return p, loadDirCmd(dir)
}

// loadDirCmd lists dir's subdirectories off the UI thread — a stale network
// mount must not freeze the form — marking the ones that are git checkouts.
func loadDirCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return dirLoadedMsg{dir: dir, err: err.Error()}
		}
		out := make([]dirEntry, 0, len(ents))
		for _, e := range ents {
			if !isDirEntry(dir, e) {
				continue
			}
			out = append(out, dirEntry{name: e.Name(), isRepo: isCheckout(filepath.Join(dir, e.Name()))})
		}
		sort.Slice(out, func(i, j int) bool {
			return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
		})
		return dirLoadedMsg{dir: dir, entries: out}
	}
}

// isDirEntry reports whether e is a directory, resolving symlinks — a symlinked
// checkout (a very common way to keep code on another volume) reports as a link,
// not as a directory, and skipping it would hide the repository entirely.
func isDirEntry(parent string, e fs.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink == 0 {
		return false
	}
	st, err := os.Stat(filepath.Join(parent, e.Name()))
	return err == nil && st.IsDir()
}

// isCheckout reports whether dir holds a .git entry. It accepts a FILE as well
// as a directory: a git worktree's .git is a file pointing at the real gitdir.
func isCheckout(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// visible applies the hidden-directory rule and the type-to-narrow filter. The
// cursor indexes into THIS list, never into entries.
func (p *dirPicker) visible() []dirEntry {
	f := strings.ToLower(p.filter)
	out := make([]dirEntry, 0, len(p.entries))
	for _, e := range p.entries {
		if !p.hidden && strings.HasPrefix(e.name, ".") && !strings.HasPrefix(f, ".") {
			continue
		}
		if f != "" && !strings.Contains(strings.ToLower(e.name), f) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (p *dirPicker) update(msg tea.Msg) (tea.Cmd, dirPickerEvent) {
	switch v := msg.(type) {
	case dirLoadedMsg:
		if v.dir != p.dir {
			return nil, dirPickerNone // a listing for a directory we already left
		}
		p.loading, p.entries, p.err = false, v.entries, v.err
		p.cursor, p.filter = 0, ""
		if p.want != "" {
			for i, e := range p.visible() {
				if e.name == p.want {
					p.cursor = i
					break
				}
			}
			p.want = ""
		}
	case tea.KeyPressMsg:
		return p.key(v)
	}
	return nil, dirPickerNone
}

func (p *dirPicker) key(k tea.KeyPressMsg) (tea.Cmd, dirPickerEvent) {
	vis := p.visible()
	if p.cursor >= len(vis) {
		p.cursor = len(vis) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	switch k.String() {
	case "esc":
		// A narrowed list is a state worth backing out of on its own — esc only
		// closes the browser once there is no filter left to clear.
		if p.filter != "" {
			p.filter, p.cursor = "", 0
			return nil, dirPickerNone
		}
		return nil, dirPickerCancel
	case "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down":
		if p.cursor < len(vis)-1 {
			p.cursor++
		}
	case "left":
		return p.ascend(), dirPickerNone
	case "right":
		if len(vis) > 0 {
			return p.descend(vis[p.cursor].name), dirPickerNone
		}
	case "enter":
		// A checkout is what the browser is FOR, so enter takes it. Anything else
		// is a container to walk into; right still descends into a repo, so a
		// nested checkout stays reachable.
		if len(vis) == 0 {
			p.chosen = p.dir
			return nil, dirPickerChosen
		}
		if vis[p.cursor].isRepo {
			p.chosen = filepath.Join(p.dir, vis[p.cursor].name)
			return nil, dirPickerChosen
		}
		return p.descend(vis[p.cursor].name), dirPickerNone
	case "ctrl+s":
		// Take the directory being browsed, repo or not: a checkout you have
		// already walked into, or a plain folder you are about to `git init`.
		p.chosen = p.dir
		return nil, dirPickerChosen
	case "ctrl+t":
		// ctrl+t, not the more mnemonic ctrl+h: a terminal that still sends 0x08
		// for backspace reports it AS ctrl+h, which would make deleting a filter
		// character toggle hidden directories instead.
		p.hidden = !p.hidden
		p.cursor = 0
	case "backspace":
		if p.filter != "" {
			p.filter, p.cursor = dropLastRune(p.filter), 0
		} else {
			return p.ascend(), dirPickerNone
		}
	default:
		if k.Text != "" {
			p.filter, p.cursor = p.filter+k.Text, 0
		}
	}
	return nil, dirPickerNone
}

// descend walks into name, remembering nothing: the cursor starts at the top of
// the new listing.
func (p *dirPicker) descend(name string) tea.Cmd {
	p.dir = filepath.Join(p.dir, name)
	p.loading, p.entries, p.err, p.filter, p.cursor = true, nil, "", "", 0
	return loadDirCmd(p.dir)
}

// ascend walks to the parent, putting the cursor back on the directory just
// left. At the filesystem root it is a no-op rather than a dead end.
func (p *dirPicker) ascend() tea.Cmd {
	parent := filepath.Dir(p.dir)
	if parent == p.dir {
		return nil
	}
	p.want = filepath.Base(p.dir)
	p.dir = parent
	p.loading, p.entries, p.err, p.filter, p.cursor = true, nil, "", "", 0
	return loadDirCmd(p.dir)
}

// dirPickerStart picks the directory the browser should open on: the project's
// own path when it has one, else the parent most of the configured projects
// already live in (the next repo is nearly always beside the last), else $HOME.
func dirPickerStart(cfg *config.Config, current string) string {
	if p := strings.TrimSpace(current); p != "" {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
		if d := filepath.Dir(p); d != "" && d != "." {
			return d
		}
	}
	if d := commonProjectParent(cfg); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return home
}

// commonProjectParent returns the directory holding the most configured project
// checkouts, or "" when there are none (or none that still exist).
func commonProjectParent(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	counts := map[string]int{}
	best, bestN := "", 0
	for _, pr := range cfg.Projects {
		p := strings.TrimSpace(pr.Path)
		if p == "" {
			continue
		}
		d := filepath.Dir(p)
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		counts[d]++
		// Ties resolve to the first project in config order, which is stable.
		if counts[d] > bestN {
			best, bestN = d, counts[d]
		}
	}
	return best
}

// ---- view ----

func (p *dirPicker) view(height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Choose the project folder") + "\n\n")
	b.WriteString("  " + faintText.Render(collapseHome(p.dir)) + "\n")
	if p.filter != "" {
		b.WriteString("  " + warnText.Render("/"+p.filter) + "\n")
	}
	b.WriteString("\n")

	vis := p.visible()
	if p.cursor >= len(vis) {
		p.cursor = len(vis) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}

	switch {
	case p.loading:
		b.WriteString(faintText.Render("  reading…") + "\n")
	case p.err != "":
		b.WriteString(badText.Render("  "+p.err) + "\n")
	case len(vis) == 0:
		b.WriteString(faintText.Render("  (no subdirectories) — enter or ctrl-s takes this folder") + "\n")
	}

	win := height - 9
	if win < 5 {
		win = 5
	}
	start := 0
	if len(vis) > win {
		start = p.cursor - win/2
		if start < 0 {
			start = 0
		}
		if start > len(vis)-win {
			start = len(vis) - win
		}
	}
	end := start + win
	if end > len(vis) {
		end = len(vis)
	}
	if start > 0 {
		b.WriteString(faintText.Render("  ↑ more") + "\n")
	}
	for i := start; i < end; i++ {
		e := vis[i]
		marker := "  "
		if i == p.cursor {
			marker = "› "
		}
		// The repo tag is the whole point of the listing: it says which row enter
		// will TAKE rather than walk into.
		tag := ""
		if e.isRepo {
			tag = goodText.Render("  git")
		}
		line := marker + e.name + "/" + tag
		if i == p.cursor {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < len(vis) {
		b.WriteString(faintText.Render("  ↓ more") + "\n")
	}

	b.WriteString("\n" + faintText.Render(
		"↑/↓ move · type to filter · →/enter open · ← up · enter on a git repo takes it · ctrl-s take this folder · ctrl-t hidden · esc cancel") + "\n")
	return b.String()
}

// collapseHome shortens $HOME to "~" so the path line stays readable in a modal.
func collapseHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(p, home) {
		return p
	}
	return "~" + strings.TrimPrefix(p, home)
}
