// Package toolchain decides which language toolchains a sandboxed project's
// container needs and renders the Dockerfile of the derived image that adds
// them to lola's default sandbox image.
//
// lola's default image carries the agents, git, gh, ripgrep and node. A
// project needs more — PHP and composer for a Laravel app, cargo for a Rust
// crate — so the runtime builds one image per project on top of the default
// one: FROM the default image, plus the toolchains this package names. The
// toolchains come from the project's config ([[project]].sandbox_toolchains)
// or, when that is unset, from the repository's own files (Detect).
//
// A stdlib leaf: it reads a few small files and renders text. It never execs
// docker; internal/runtime does. Detection fails open — an unreadable or
// malformed file means "not detected", never an error.
package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Kind is one toolchain lola knows how to install.
type Kind string

const (
	Node   Kind = "node"
	Go     Kind = "go"
	Rust   Kind = "rust"
	PHP    Kind = "php"
	Python Kind = "python"
	Ruby   Kind = "ruby"
)

// Kinds lists every toolchain in display order (the UIs build their pickers
// from it).
var Kinds = []Kind{Node, PHP, Python, Go, Rust, Ruby}

// BaseNodeMajor is the node major the default image already carries. A node
// toolchain of this major (or with no version) adds nothing.
const BaseNodeMajor = "24"

// Debian is the Debian release the default image is built on; the official
// images toolchains are copied from must match it.
const Debian = "trixie"

// PHPVersions are the PHP versions the derived image can install, newest
// first (packages.sury.org builds them for Debian).
var PHPVersions = []string{"8.5", "8.4", "8.3", "8.2", "8.1"}

// Toolchain is one toolchain and its version ("" = the recipe's default).
type Toolchain struct {
	Kind    Kind   `json:"kind"`
	Version string `json:"version"`
}

func (t Toolchain) String() string {
	if t.Version == "" {
		return string(t.Kind)
	}
	return string(t.Kind) + "@" + t.Version
}

// Spec is everything the derived image adds.
type Spec struct {
	Toolchains []Toolchain `json:"toolchains"`
	// PHPExtensions are extension names (composer's ext-* without the prefix).
	PHPExtensions []string `json:"phpExtensions"`
	// NodeTools are global npm packages a lockfile or packageManager asks for
	// ("pnpm@9.15.0", "yarn@1", "bun").
	NodeTools []string `json:"nodeTools"`
	// Packages are extra Debian packages ([[project]].sandbox_packages).
	Packages []string `json:"packages"`
}

// Has reports whether s includes kind.
func (s Spec) Has(k Kind) bool {
	return slices.ContainsFunc(s.Toolchains, func(t Toolchain) bool { return t.Kind == k })
}

// Empty reports whether s adds nothing to the default image.
func (s Spec) Empty() bool {
	return Dockerfile("base", s) == ""
}

var (
	entryRe   = regexp.MustCompile(`^(node|go|rust|php|python|ruby)(?:@([0-9A-Za-z][0-9A-Za-z._-]*))?$`)
	packageRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*$`)
	toolRe    = regexp.MustCompile(`^(pnpm|yarn|bun)(?:@[0-9A-Za-z.+-]+)?$`)
	phpExtRe  = regexp.MustCompile(`^[a-z0-9_]+$`)
)

// None is the sandbox_toolchains entry that turns detection off and adds no
// toolchain.
const None = "none"

// ParseEntry parses one sandbox_toolchains entry ("php", "node@22").
func ParseEntry(s string) (Toolchain, error) {
	m := entryRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Toolchain{}, fmt.Errorf("%q is not a toolchain (one of node, php, python, go, rust, ruby, optionally @version)", s)
	}
	return Toolchain{Kind: Kind(m[1]), Version: m[2]}, nil
}

// ValidPackage reports whether name is a Debian package name.
func ValidPackage(name string) bool { return packageRe.MatchString(name) }

// Validate checks config values statically (config.Validate calls it).
func Validate(entries, packages []string) []error {
	var errs []error
	for _, e := range entries {
		if e == None {
			if len(entries) > 1 {
				errs = append(errs, fmt.Errorf("sandbox_toolchains: %q cannot be combined with other entries", None))
			}
			continue
		}
		if _, err := ParseEntry(e); err != nil {
			errs = append(errs, fmt.Errorf("sandbox_toolchains: %w", err))
		}
	}
	for _, p := range packages {
		if !ValidPackage(p) {
			errs = append(errs, fmt.Errorf("sandbox_packages: %q is not a Debian package name", p))
		}
	}
	return errs
}

// Resolve is the Spec a project's container needs: entries (sandbox_toolchains)
// when set, else what Detect finds in the checkout at dir. The detected
// details a configured toolchain does not spell out (PHP extensions, pnpm)
// still come from the repository.
func Resolve(entries, packages []string, dir string) Spec {
	found := Detect(dir)
	s := Spec{Packages: slices.Clone(packages)}
	switch {
	case len(entries) == 0:
		s.Toolchains = found.Toolchains
	case len(entries) == 1 && entries[0] == None:
		// nothing
	default:
		for _, e := range entries {
			t, err := ParseEntry(e)
			if err != nil {
				continue // Validate reports it
			}
			if t.Version == "" {
				t.Version = found.version(t.Kind)
			}
			s.Toolchains = append(s.Toolchains, t)
		}
	}
	if s.Has(PHP) {
		s.PHPExtensions = found.PHPExtensions
	}
	if s.Has(Node) || len(found.NodeTools) > 0 {
		s.NodeTools = found.NodeTools
	}
	return s
}

func (s Spec) version(k Kind) string {
	for _, t := range s.Toolchains {
		if t.Kind == k {
			return t.Version
		}
	}
	return ""
}

// Tag is the local image tag of the derived image for dockerfile on a base
// whose image ID is baseID. Content-addressed: a changed toolchain, version
// or base image is a new tag, so a stale image is never reused.
func Tag(dockerfile, baseID string) string {
	sum := sha256.Sum256([]byte(baseID + "\n" + dockerfile))
	return "lola-sandbox-project:" + hex.EncodeToString(sum[:])[:16]
}

// LabelSpec is the image label recording what a derived image adds.
const LabelSpec = "dev.sushi.lola.toolchains"

// Summary is s in one line ("node, php@8.5 (intl, redis), +ffmpeg").
func (s Spec) Summary() string {
	var parts []string
	for _, t := range s.Toolchains {
		p := t.String()
		if t.Kind == PHP && len(s.PHPExtensions) > 0 {
			p += " (" + strings.Join(s.PHPExtensions, ", ") + ")"
		}
		parts = append(parts, p)
	}
	parts = append(parts, s.NodeTools...)
	for _, p := range s.Packages {
		parts = append(parts, "+"+p)
	}
	return strings.Join(parts, ", ")
}

// Dockerfile renders the derived image: FROM base plus every toolchain in s.
// "" when s adds nothing (the project runs base as it is).
func Dockerfile(base string, s Spec) string {
	apt := []string{}
	var steps []string
	env := map[string]string{}
	add := func(p ...string) { apt = append(apt, p...) }

	for _, t := range s.Toolchains {
		switch t.Kind {
		case Node:
			if major := nodeMajor(t.Version); major != "" && major != BaseNodeMajor {
				img := "node:" + major + "-" + Debian + "-slim"
				steps = append(steps,
					"COPY --from="+img+" /usr/local/bin/node /usr/local/bin/node",
					"COPY --from="+img+" /usr/local/lib/node_modules /usr/local/lib/node_modules")
			}
		case Go:
			v := goVersion(t.Version)
			steps = append(steps, "COPY --from=golang:"+v+"-"+Debian+" /usr/local/go /usr/local/go")
			add("gcc", "libc6-dev")
		case Rust:
			// rustup finds itself through CARGO_HOME, so rust keeps the image's
			// own; the downloaded crates live in the per-project cache volume.
			steps = append(steps,
				"COPY --from=rust:1-slim-"+Debian+" --chown=lola:lola /usr/local/rustup /usr/local/rustup",
				"COPY --from=rust:1-slim-"+Debian+" --chown=lola:lola /usr/local/cargo /usr/local/cargo")
			install := ""
			if ch := rustChannel(t.Version); ch != "" {
				install = "RUSTUP_HOME=/usr/local/rustup CARGO_HOME=/usr/local/cargo rustup toolchain install " + ch + " --profile minimal --component clippy,rustfmt && "
			}
			steps = append(steps, "RUN "+install+"ln -s /home/lola/.cache/cargo-registry /usr/local/cargo/registry && ln -s /home/lola/.cache/cargo-git /usr/local/cargo/git && chown -R lola:lola /usr/local/rustup /usr/local/cargo")
			env["RUSTUP_HOME"] = "/usr/local/rustup"
			env["CARGO_HOME"] = "/usr/local/cargo"
			add("build-essential", "pkg-config", "libssl-dev")
		case PHP:
			v := PHPVersion(t.Version)
			pkgs := []string{"php" + v + "-cli"}
			for _, e := range phpBaseline {
				pkgs = append(pkgs, "php"+v+"-"+e)
			}
			var extra []string
			for _, e := range s.PHPExtensions {
				if pk := phpExtPackage(e); pk != "" && !slices.Contains(phpBaseline, pk) {
					extra = append(extra, "php"+v+"-"+pk)
				}
			}
			sort.Strings(extra)
			extra = slices.Compact(extra)
			steps = append(steps,
				"RUN curl -fsSLo /usr/share/keyrings/sury-php.gpg https://packages.sury.org/php/apt.gpg \\\n"+
					" && echo \"deb [signed-by=/usr/share/keyrings/sury-php.gpg] https://packages.sury.org/php/ "+Debian+" main\" > /etc/apt/sources.list.d/sury-php.list \\\n"+
					" && apt-get update && apt-get install -y --no-install-recommends "+strings.Join(pkgs, " ")+" \\\n"+
					phpExtraLoop(extra)+
					" && rm -rf /var/lib/apt/lists/*",
				"COPY --from=composer:2 /usr/bin/composer /usr/local/bin/composer")
		case Python:
			add("python3", "python3-venv", "python3-pip")
			steps = append(steps, "COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /usr/local/bin/")
			// uv keeps the Pythons it downloads in the per-project cache volume.
			env["UV_PYTHON_INSTALL_DIR"] = "/home/lola/.cache/uv-python"
		case Ruby:
			add("ruby-full", "ruby-bundler", "build-essential")
		}
	}
	var tools []string
	for _, t := range s.NodeTools {
		if toolRe.MatchString(t) {
			tools = append(tools, t)
		}
	}
	if len(tools) > 0 {
		steps = append(steps, "RUN npm install -g --no-fund --no-audit "+strings.Join(quoteAll(tools), " ")+" && npm cache clean --force")
	}
	for _, p := range s.Packages {
		if ValidPackage(p) {
			add(p)
		}
	}
	sort.Strings(apt)
	apt = slices.Compact(apt)
	if len(apt) == 0 && len(steps) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\nUSER root\n", base)
	if len(apt) > 0 {
		fmt.Fprintf(&b, "RUN apt-get update && apt-get install -y --no-install-recommends %s && rm -rf /var/lib/apt/lists/*\n", strings.Join(apt, " "))
	}
	for _, st := range steps {
		b.WriteString(st + "\n")
	}
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("ENV")
		for _, k := range keys {
			fmt.Fprintf(&b, " %s=%s", k, env[k])
		}
		b.WriteString("\n")
	}
	// Steps run as root may have created cache directories; a fresh cache
	// volume copies the image's ownership, so hand them back to the agent.
	b.WriteString("RUN chown -R lola:lola /home/lola/.cache\n")
	fmt.Fprintf(&b, "LABEL %s=%q\nUSER lola\n", LabelSpec, s.Summary())
	return b.String()
}

// phpBaseline is installed with every PHP: what a typical framework app
// (Laravel, Symfony) needs to install and run its tests.
var phpBaseline = []string{"bcmath", "curl", "gd", "intl", "mbstring", "mysql", "pgsql", "sqlite3", "xml", "zip"}

// phpBuiltin are extensions php-cli/php-common already provide.
var phpBuiltin = []string{"calendar", "ctype", "exif", "ffi", "fileinfo", "filter", "ftp", "gettext", "hash", "iconv", "json", "openssl", "pcntl", "pcre", "pdo", "phar", "posix", "readline", "reflection", "session", "shmop", "sockets", "sodium", "spl", "standard", "sysvmsg", "sysvsem", "sysvshm", "tokenizer", "zlib", "opcache"}

// phpExtPackage maps a composer ext-* name to its sury package suffix ("" =
// built in).
func phpExtPackage(ext string) string {
	ext = strings.ToLower(ext)
	if !phpExtRe.MatchString(ext) || slices.Contains(phpBuiltin, ext) {
		return ""
	}
	switch ext {
	case "pdo_mysql", "mysqli", "mysqlnd":
		return "mysql"
	case "pdo_pgsql":
		return "pgsql"
	case "pdo_sqlite":
		return "sqlite3"
	case "dom", "simplexml", "xmlreader", "xmlwriter", "xsl", "libxml":
		return "xml"
	}
	return ext
}

// phpExtraLoop installs extensions one by one, so an extension sury does not
// package skips with a note instead of failing the whole image.
func phpExtraLoop(pkgs []string) string {
	if len(pkgs) == 0 {
		return ""
	}
	return " && for p in " + strings.Join(pkgs, " ") + "; do apt-get install -y --no-install-recommends \"$p\" || echo \"lola: no package $p, skipped\"; done \\\n"
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = "'" + s + "'"
	}
	return out
}

var majorRe = regexp.MustCompile(`(\d+)`)

// nodeMajor is the node major of a version or constraint ("" = default).
func nodeMajor(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	m := majorRe.FindString(v)
	return m
}

var goVerRe = regexp.MustCompile(`^(\d+)\.(\d+)`)

// goVersion is the golang image tag for a go.mod version ("1.25.3" → "1.25").
func goVersion(v string) string {
	if m := goVerRe.FindStringSubmatch(strings.TrimPrefix(v, "go")); m != nil {
		return m[1] + "." + m[2]
	}
	return "1"
}

var rustChannelRe = regexp.MustCompile(`^(stable|beta|nightly(-\d{4}-\d{2}-\d{2})?|\d+\.\d+(\.\d+)?)$`)

// rustChannel is the toolchain to install beside the image's stable ("" =
// stable is enough).
func rustChannel(v string) string {
	if v == "" || v == "stable" || v == "1" || !rustChannelRe.MatchString(v) {
		return ""
	}
	return v
}

var phpVerRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// PHPVersion picks the PHP to install for a version or composer constraint:
// an exact minor ("8.3", "8.3.*", "~8.3.0") is used as is; a range ("^8.2",
// ">=8.1", "^8.2|^8.3") gets the newest known version it allows.
func PHPVersion(v string) string {
	v = strings.TrimSpace(v)
	m := phpVerRe.FindStringSubmatch(v)
	if m == nil {
		return PHPVersions[0]
	}
	exact := m[1] + "." + m[2]
	if !strings.ContainsAny(v, "^>|") || strings.HasPrefix(v, "~") && strings.Count(v, ".") >= 2 {
		return exact
	}
	for _, known := range PHPVersions {
		if strings.HasPrefix(known, m[1]+".") && compareMinor(known, exact) >= 0 {
			return known
		}
	}
	return exact
}

func compareMinor(a, b string) int {
	var am, an, bm, bn int
	fmt.Sscanf(a, "%d.%d", &am, &an)
	fmt.Sscanf(b, "%d.%d", &bm, &bn)
	if am != bm {
		return am - bm
	}
	return an - bn
}
