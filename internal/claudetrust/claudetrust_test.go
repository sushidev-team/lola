package claudetrust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, b)
	}
	return doc
}

func entry(t *testing.T, doc map[string]any, key string) (map[string]any, bool) {
	t.Helper()
	projects, _ := doc["projects"].(map[string]any)
	e, ok := projects[key].(map[string]any)
	return e, ok
}

// realDir returns a temp directory by its symlink-resolved path, so the
// tests see exactly one key per dir (macOS's TempDir lives under /var →
// /private/var, which Keys deliberately writes twice).
func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTrustCreatesMissingFile(t *testing.T) {
	root := realDir(t)
	cfg := filepath.Join(root, ".claude.json")
	wt := filepath.Join(root, "wt")
	if err := Trust(cfg, wt); err != nil {
		t.Fatal(err)
	}
	e, ok := entry(t, readDoc(t, cfg), wt)
	if !ok || e[trustKey] != true {
		t.Fatalf("entry = %v, want %s=true", e, trustKey)
	}
	st, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestTrustPreservesEverythingElse(t *testing.T) {
	root := realDir(t)
	cfg := filepath.Join(root, ".claude.json")
	wt := filepath.Join(root, "wt")
	orig := `{
  "numStartups": 42,
  "oauthAccount": {"emailAddress": "x@example.com"},
  "projects": {
    "/elsewhere": {"hasTrustDialogAccepted": true, "allowedTools": ["Bash"]},
    "` + wt + `": {"allowedTools": ["Read"], "mcpServers": {"a": {"command": "b"}}}
  }
}`
	if err := os.WriteFile(cfg, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Trust(cfg, wt); err != nil {
		t.Fatal(err)
	}
	doc := readDoc(t, cfg)
	if doc["numStartups"] != float64(42) {
		t.Errorf("numStartups lost: %v", doc["numStartups"])
	}
	if acct, _ := doc["oauthAccount"].(map[string]any); acct["emailAddress"] != "x@example.com" {
		t.Errorf("oauthAccount lost: %v", doc["oauthAccount"])
	}
	if e, _ := entry(t, doc, "/elsewhere"); e[trustKey] != true {
		t.Errorf("other project lost: %v", e)
	}
	e, _ := entry(t, doc, wt)
	if e[trustKey] != true || e["allowedTools"] == nil || e["mcpServers"] == nil {
		t.Errorf("entry = %v, want trust added and existing fields kept", e)
	}
	if st, _ := os.Stat(cfg); st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want the original 0644 kept", st.Mode().Perm())
	}
}

func TestUntrustRemovesOnlyTheFlag(t *testing.T) {
	root := realDir(t)
	cfg := filepath.Join(root, ".claude.json")
	bare, rich := filepath.Join(root, "bare"), filepath.Join(root, "rich")
	orig := `{"projects": {
  "` + bare + `": {"hasTrustDialogAccepted": true},
  "` + rich + `": {"hasTrustDialogAccepted": true, "allowedTools": ["Read"]}
}}`
	if err := os.WriteFile(cfg, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Untrust(cfg, bare); err != nil {
		t.Fatal(err)
	}
	if err := Untrust(cfg, rich); err != nil {
		t.Fatal(err)
	}
	doc := readDoc(t, cfg)
	if _, ok := entry(t, doc, bare); ok {
		t.Error("an entry holding only the flag must be removed whole")
	}
	e, ok := entry(t, doc, rich)
	if !ok || e["allowedTools"] == nil {
		t.Fatalf("an entry with other state must be kept: %v", e)
	}
	if _, has := e[trustKey]; has {
		t.Error("trust flag must be gone")
	}
}

func TestNoOpsDoNotWrite(t *testing.T) {
	root := realDir(t)
	cfg := filepath.Join(root, ".claude.json")
	wt := filepath.Join(root, "wt")

	// Untrust with no file creates nothing.
	if err := Untrust(cfg, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Fatalf("untrust on a missing file must not create it (err=%v)", err)
	}

	// Already trusted / never trusted: byte-identical file.
	orig := `{"projects":{"` + wt + `":{"hasTrustDialogAccepted":true}},"x":1}`
	if err := os.WriteFile(cfg, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Trust(cfg, wt); err != nil {
		t.Fatal(err)
	}
	if err := Untrust(cfg, filepath.Join(root, "other")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != orig {
		t.Errorf("no-op rewrote the file:\n%s", b)
	}
}

func TestUnparseableFileIsLeftAlone(t *testing.T) {
	root := realDir(t)
	cfg := filepath.Join(root, ".claude.json")
	for _, bad := range []string{`{"projects": {`, `{"projects": []}`, `{"projects": {"` + root + `/wt": "nope"}}`} {
		if err := os.WriteFile(cfg, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Trust(cfg, filepath.Join(root, "wt")); err == nil {
			t.Errorf("Trust on %q: want an error", bad)
		}
		if b, _ := os.ReadFile(cfg); string(b) != bad {
			t.Errorf("Trust on %q rewrote it to %q", bad, b)
		}
	}
}

func TestSymlinkedConfigKeepsTheLink(t *testing.T) {
	root := realDir(t)
	real := filepath.Join(root, "dotfiles", "claude.json")
	if err := os.MkdirAll(filepath.Dir(real), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".claude.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "wt")
	if err := Trust(link, wt); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config symlink was replaced (err=%v)", err)
	}
	if e, _ := entry(t, readDoc(t, real), wt); e[trustKey] != true {
		t.Errorf("trust not written through the link: %v", e)
	}
}

func TestKeysIncludeResolvedPath(t *testing.T) {
	root := realDir(t)
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	keys := Keys(alias)
	if len(keys) != 2 || keys[0] != alias || keys[1] != target {
		t.Errorf("Keys(%s) = %v, want [alias target]", alias, keys)
	}
	if got := Keys(target); len(got) != 1 {
		t.Errorf("Keys of a real path = %v, want one key", got)
	}
}

func TestRejectsRelativeDir(t *testing.T) {
	cfg := filepath.Join(realDir(t), ".claude.json")
	if err := Trust(cfg, "relative/wt"); err == nil {
		t.Error("a relative dir must be refused")
	}
	if err := Trust("", "/abs"); err == nil {
		t.Error("an empty config path must be refused")
	}
}

func TestConfigPathHonorsClaudeConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/cc")
	if got := ConfigPath(); got != "/tmp/cc/.claude.json" {
		t.Errorf("ConfigPath = %q", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "/tmp/h")
	if got := ConfigPath(); got != "/tmp/h/.claude.json" {
		t.Errorf("ConfigPath = %q", got)
	}
}
