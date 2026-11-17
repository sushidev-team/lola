package toolchain

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// maxFile bounds every file Detect reads.
const maxFile = 1 << 20

// scanDepth is how far below the repository root Detect looks for markers:
// monorepos keep their app in desktop/frontend/ or apps/web/.
const scanDepth = 2

// skipDirs are never descended into: dependencies and build output.
var skipDirs = []string{".git", "node_modules", "vendor", "target", "dist", "build", ".lola", ".venv", "venv", "__pycache__"}

// Detect reads the checkout at dir and names the toolchains its files ask
// for. Bounded (scanDepth, maxFile) and fail-open: anything unreadable is
// simply not detected. Node is reported only when it needs more than the
// default image (another major, or pnpm/yarn/bun).
func Detect(dir string) Spec {
	var s Spec
	files := markers(dir)
	seen := map[Kind]bool{}
	addTC := func(k Kind, v string) {
		if !seen[k] {
			seen[k] = true
			s.Toolchains = append(s.Toolchains, Toolchain{Kind: k, Version: v})
		}
	}
	for _, f := range files {
		base := filepath.Base(f)
		d := filepath.Dir(f)
		switch base {
		case "package.json":
			if seen[Node] {
				continue
			}
			v, tools := detectNode(d, f)
			s.NodeTools = append(s.NodeTools, tools...)
			if (v != "" && v != BaseNodeMajor) || len(tools) > 0 {
				addTC(Node, v)
			} else {
				seen[Node] = true
			}
		case "composer.json":
			if seen[PHP] {
				continue
			}
			v, exts := detectComposer(f)
			s.PHPExtensions = exts
			addTC(PHP, v)
		case "go.mod":
			addTC(Go, detectGoMod(f))
		case "Cargo.toml":
			addTC(Rust, detectRust(d))
		case "pyproject.toml", "requirements.txt", "uv.lock", "Pipfile":
			addTC(Python, "")
		case "Gemfile":
			addTC(Ruby, "")
		}
	}
	s.NodeTools = slices.Compact(s.NodeTools)
	slices.SortStableFunc(s.Toolchains, func(a, b Toolchain) int {
		return slices.Index(Kinds, a.Kind) - slices.Index(Kinds, b.Kind)
	})
	return s
}

// markers lists the marker files under dir, shallowest first.
func markers(dir string) []string {
	names := []string{"package.json", "composer.json", "go.mod", "Cargo.toml", "pyproject.toml", "requirements.txt", "uv.lock", "Pipfile", "Gemfile"}
	var out []string
	level := []string{dir}
	for depth := 0; depth <= scanDepth && len(level) > 0; depth++ {
		var next []string
		for _, d := range level {
			entries, err := os.ReadDir(d)
			if err != nil {
				continue
			}
			for _, e := range entries {
				p := filepath.Join(d, e.Name())
				if e.IsDir() {
					if !slices.Contains(skipDirs, e.Name()) && !strings.HasPrefix(e.Name(), ".") {
						next = append(next, p)
					}
					continue
				}
				if e.Type().IsRegular() && slices.Contains(names, e.Name()) {
					out = append(out, p)
				}
			}
		}
		level = next
	}
	return out
}

func readSmall(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, maxFile))
	return b
}

func exists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// detectNode reads the node major (.nvmrc, .node-version, engines.node) and
// the package manager a project uses beyond npm.
func detectNode(dir, pkgPath string) (string, []string) {
	var pkg struct {
		Engines        map[string]string `json:"engines"`
		PackageManager string            `json:"packageManager"`
	}
	_ = json.Unmarshal(readSmall(pkgPath), &pkg)
	major := ""
	for _, f := range []string{".nvmrc", ".node-version"} {
		if v := strings.TrimSpace(string(readSmall(filepath.Join(dir, f)))); v != "" {
			if m := nodeMajor(v); m != "" {
				major = m
				break
			}
		}
	}
	if major == "" {
		// A range like ">=20" is satisfied by the default; only a pinned
		// major (^22, ~22, 22.x, 22) asks for another one.
		if e := strings.TrimSpace(pkg.Engines["node"]); e != "" && !strings.ContainsAny(e, "><*|") {
			major = nodeMajor(e)
		}
	}
	var tools []string
	pm := strings.SplitN(pkg.PackageManager, "+", 2)[0]
	switch {
	case strings.HasPrefix(pm, "pnpm@"):
		tools = append(tools, pm)
	case strings.HasPrefix(pm, "yarn@"):
		tools = append(tools, "yarn@1") // yarn 1 runs a repo's own yarn 2+ release
	case strings.HasPrefix(pm, "bun@"):
		tools = append(tools, pm)
	case exists(filepath.Join(dir, "pnpm-lock.yaml")):
		tools = append(tools, "pnpm")
	case exists(filepath.Join(dir, "yarn.lock")):
		tools = append(tools, "yarn@1")
	case exists(filepath.Join(dir, "bun.lockb")) || exists(filepath.Join(dir, "bun.lock")):
		tools = append(tools, "bun")
	}
	if !toolsValid(tools) {
		tools = nil
	}
	return major, tools
}

func toolsValid(tools []string) bool {
	for _, t := range tools {
		if !toolRe.MatchString(t) {
			return false
		}
	}
	return true
}

// detectComposer reads the PHP version (config.platform.php, else
// require.php) and the ext-* extensions composer.json requires.
func detectComposer(path string) (string, []string) {
	var c struct {
		Require    map[string]string `json:"require"`
		RequireDev map[string]string `json:"require-dev"`
		Config     struct {
			Platform map[string]string `json:"platform"`
		} `json:"config"`
	}
	_ = json.Unmarshal(readSmall(path), &c)
	v := c.Config.Platform["php"]
	if v == "" {
		v = c.Require["php"]
	}
	var exts []string
	for _, m := range []map[string]string{c.Require, c.RequireDev} {
		for k := range m {
			if e, ok := strings.CutPrefix(strings.ToLower(k), "ext-"); ok && phpExtRe.MatchString(e) {
				exts = append(exts, e)
			}
		}
	}
	slices.Sort(exts)
	exts = slices.Compact(exts)
	if v == "" {
		return "", exts
	}
	return PHPVersion(v), exts
}

var goLineRe = regexp.MustCompile(`^\s*(go|toolchain)\s+(?:go)?(\d+\.\d+(?:\.\d+)?)`)

// detectGoMod reads the go (or newer toolchain) line of go.mod.
func detectGoMod(path string) string {
	v := ""
	sc := bufio.NewScanner(strings.NewReader(string(readSmall(path))))
	for sc.Scan() {
		if m := goLineRe.FindStringSubmatch(sc.Text()); m != nil {
			if m[1] == "toolchain" || v == "" {
				v = m[2]
			}
		}
	}
	if v == "" {
		return ""
	}
	return goVersion(v)
}

var channelRe = regexp.MustCompile(`(?m)^\s*channel\s*=\s*"([^"]+)"`)

// detectRust reads rust-toolchain.toml / rust-toolchain beside Cargo.toml.
func detectRust(dir string) string {
	if m := channelRe.FindStringSubmatch(string(readSmall(filepath.Join(dir, "rust-toolchain.toml")))); m != nil {
		return rustChannel(m[1])
	}
	return rustChannel(strings.TrimSpace(string(readSmall(filepath.Join(dir, "rust-toolchain")))))
}
