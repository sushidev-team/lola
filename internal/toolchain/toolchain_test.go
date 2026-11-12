package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectLaravelApp(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"composer.json":   `{"require":{"php":"^8.2","ext-redis":"*","ext-pdo_mysql":"*","laravel/framework":"^12"},"require-dev":{"ext-pcntl":"*"}}`,
		"package.json":    `{"packageManager":"pnpm@9.15.0+sha512.abc"}`,
		"vendor/x/go.mod": "module x\n\ngo 1.22\n", // dependencies are not the project
	})
	s := Detect(dir)
	if got := s.Summary(); got != "node, php@8.5 (pcntl, pdo_mysql, redis), pnpm@9.15.0" {
		t.Fatalf("summary = %q", got)
	}
	df := Dockerfile("base:1", s)
	for _, must := range []string{"FROM base:1\nUSER root\n", "php8.5-cli", "php8.5-mysql", "php8.5-redis", "composer:2", "'pnpm@9.15.0'", "USER lola\n"} {
		if !strings.Contains(df, must) {
			t.Errorf("dockerfile lacks %q:\n%s", must, df)
		}
	}
	if strings.Contains(df, "php8.5-pcntl") || strings.Contains(df, "php8.5-pdo_mysql") || strings.Contains(df, "node:") {
		t.Errorf("built-in extension, raw pdo name or a needless node copy:\n%s", df)
	}
}

func TestDetectMonorepoAndVersions(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"go.mod":                        "module m\n\ngo 1.24.0\n\ntoolchain go1.25.3\n",
		"desktop/frontend/package.json": `{"engines":{"node":">=20"}}`,
		"crates/a/Cargo.toml":           "[package]\nname='a'\n",
		"crates/a/rust-toolchain.toml":  "[toolchain]\nchannel = \"1.83.0\"\n",
		"api/.nvmrc":                    "v22.11.0\n",
		"api/package.json":              `{}`,
	})
	s := Detect(dir)
	got := s.Summary()
	if !strings.Contains(got, "go@1.25") || !strings.Contains(got, "rust@1.83.0") {
		t.Fatalf("summary = %q", got)
	}
	df := Dockerfile("b", s)
	if !strings.Contains(df, "golang:1.25-trixie") || !strings.Contains(df, "rustup toolchain install 1.83.0") {
		t.Fatalf("dockerfile:\n%s", df)
	}
}

func TestNothingToAdd(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"package.json": `{"engines":{"node":"^24"}}`})
	s := Detect(dir)
	if !s.Empty() || Dockerfile("b", s) != "" {
		t.Fatalf("plain npm project on the base node needs no image: %+v", s)
	}
}

func TestResolveConfiguredWinsButKeepsDetails(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"composer.json": `{"require":{"php":"8.3.*","ext-gmp":"*"}}`, "Gemfile": ""})
	s := Resolve([]string{"php", "python"}, []string{"ffmpeg"}, dir)
	if got := s.Summary(); got != "php@8.3 (gmp), python, +ffmpeg" {
		t.Fatalf("summary = %q", got)
	}
	if s := Resolve([]string{None}, nil, dir); !s.Empty() {
		t.Fatalf("none still adds: %+v", s)
	}
	if s := Resolve(nil, nil, dir); s.Summary() != "php@8.3 (gmp), ruby" {
		t.Fatalf("detected = %q", s.Summary())
	}
}

func TestPHPVersion(t *testing.T) {
	for in, want := range map[string]string{"^8.2": "8.5", ">=8.1": "8.5", "8.3.*": "8.3", "~8.3.0": "8.3", "8.4": "8.4", "^8.2|^8.3": "8.5", "": "8.5", "8.3.10": "8.3"} {
		if got := PHPVersion(in); got != want {
			t.Errorf("PHPVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	if errs := Validate([]string{"php@8.4", "node", "rust@1.83"}, []string{"ffmpeg", "libvips-dev"}); len(errs) != 0 {
		t.Fatal(errs)
	}
	for _, bad := range [][]string{{"java"}, {"php@;rm"}, {"none", "php"}} {
		if len(Validate(bad, nil)) == 0 {
			t.Errorf("accepted %v", bad)
		}
	}
	if len(Validate(nil, []string{"Bad Name", "x;y"})) != 2 {
		t.Error("accepted bad packages")
	}
}

func TestTagChangesWithBase(t *testing.T) {
	if Tag("FROM a", "id1") == Tag("FROM a", "id2") || !strings.HasPrefix(Tag("x", "y"), "lola-sandbox-project:") {
		t.Fatal("tag not content-addressed")
	}
}
