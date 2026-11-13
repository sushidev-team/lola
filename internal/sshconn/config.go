package sshconn

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConfigAliases discovers literal Host names without running ssh, Match exec,
// or any connection. Includes are scanned as candidates even when conditional;
// OpenSSH remains responsible for resolving the selected alias.
func ConfigAliases(home string) ([]string, error) {
	seenFiles, names := map[string]bool{}, map[string]bool{}
	var read func(string, int) error
	read = func(path string, depth int) error {
		path = filepath.Clean(path)
		if seenFiles[path] {
			return nil
		}
		if depth > 16 || len(seenFiles) >= 128 {
			return fmt.Errorf("SSH config includes exceed the discovery limit")
		}
		seenFiles[path] = true
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return fmt.Errorf("SSH config must be a regular file under 1 MB: %s", path)
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			i := strings.IndexAny(line, " \t=")
			if i < 0 {
				continue
			}
			key := strings.ToLower(line[:i])
			if key != "host" && key != "include" {
				continue
			}
			args, err := configWords(strings.TrimLeft(line[i:], " \t="))
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			for _, arg := range args {
				if key == "host" {
					if validDestination(arg) {
						names[arg] = true
					}
					continue
				}
				// Host-dependent tokens cannot be expanded while listing all hosts.
				arg = strings.ReplaceAll(arg, "%d", home)
				if strings.Contains(arg, "%") {
					continue
				}
				arg = os.ExpandEnv(arg)
				if strings.HasPrefix(arg, "~/") {
					arg = filepath.Join(home, arg[2:])
				}
				if strings.HasPrefix(arg, "~") {
					continue
				}
				if !filepath.IsAbs(arg) {
					arg = filepath.Join(home, ".ssh", arg)
				}
				matches, err := filepath.Glob(arg)
				if err != nil {
					return err
				}
				for _, match := range matches {
					if err := read(match, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return sc.Err()
	}
	err := read(filepath.Join(home, ".ssh", "config"), 0)
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, err
}

// Only tokenize Host/Include values, never interpret shell commands.
func configWords(s string) ([]string, error) {
	var out []string
	var word strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if word.Len() > 0 {
			out = append(out, word.String())
			word.Reset()
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#':
			flush()
			return out, nil
		case r == ' ' || r == '\t':
			flush()
		default:
			word.WriteRune(r)
		}
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("unfinished quote or escape in SSH config")
	}
	flush()
	return out, nil
}
