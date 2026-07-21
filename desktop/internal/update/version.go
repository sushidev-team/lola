// Package update implements the desktop app's in-app self-update path: it reads
// the GitHub Releases of the (public) source repo, downloads the matching macOS
// DMG with progress, and swaps the running .app bundle in place. It is a leaf
// package — stdlib only, no Wails/config imports — so it stays unit-testable and
// the Wails service (desktop/updatesvc.go) is the only thing that wires it to
// the app, events, and ~/.lola.
package update

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. Prerelease is compared lexically, which
// is enough for the "-beta.1 < release" ordering the checker needs; build
// metadata is not modelled.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
	Raw        string
}

// ParseVersion parses "1.2.3", "v1.2.3" or "1.2.3-beta.1". A leading v/V and a
// missing patch (X.Y) are tolerated; anything else is an error.
func ParseVersion(v string) (*Version, error) {
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")

	var prerelease string
	if idx := strings.Index(v, "-"); idx != -1 {
		prerelease = v[idx+1:]
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return nil, fmt.Errorf("invalid version format: %s", v)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid major version: %s", parts[0])
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid minor version: %s", parts[1])
	}
	var patch int
	if len(parts) == 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return nil, fmt.Errorf("invalid patch version: %s", parts[2])
		}
	}

	return &Version{Major: major, Minor: minor, Patch: patch, Prerelease: prerelease, Raw: v}, nil
}

// String renders the canonical X.Y.Z[-prerelease] form.
func (v *Version) String() string {
	base := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		return base + "-" + v.Prerelease
	}
	return base
}

// Compare returns -1, 0 or 1. A version without a prerelease outranks the same
// numbers with one (1.0.0 > 1.0.0-rc.1).
func (v *Version) Compare(other *Version) int {
	switch {
	case v.Major != other.Major:
		return sign(v.Major - other.Major)
	case v.Minor != other.Minor:
		return sign(v.Minor - other.Minor)
	case v.Patch != other.Patch:
		return sign(v.Patch - other.Patch)
	}
	// Equal numbers: the presence of a prerelease is the tiebreaker.
	if v.Prerelease == "" && other.Prerelease != "" {
		return 1
	}
	if v.Prerelease != "" && other.Prerelease == "" {
		return -1
	}
	switch {
	case v.Prerelease < other.Prerelease:
		return -1
	case v.Prerelease > other.Prerelease:
		return 1
	}
	return 0
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	if n > 0 {
		return 1
	}
	return 0
}

func (v *Version) LessThan(other *Version) bool    { return v.Compare(other) < 0 }
func (v *Version) GreaterThan(other *Version) bool { return v.Compare(other) > 0 }
func (v *Version) Equal(other *Version) bool       { return v.Compare(other) == 0 }

// IsNewerThan reports whether latest > current. A parse failure on either side
// is an error, never a silent "no update".
func IsNewerThan(current, latest string) (bool, error) {
	currentVer, err := ParseVersion(current)
	if err != nil {
		return false, fmt.Errorf("failed to parse current version: %w", err)
	}
	latestVer, err := ParseVersion(latest)
	if err != nil {
		return false, fmt.Errorf("failed to parse latest version: %w", err)
	}
	return latestVer.GreaterThan(currentVer), nil
}

// CleanVersion strips a leading v/V and surrounding whitespace.
func CleanVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}

var semverRe = regexp.MustCompile(`^\d+\.\d+(\.\d+)?(-[a-zA-Z0-9.-]+)?$`)

// IsValidVersion reports whether s is a semantic version once cleaned. The dev
// sentinel ("dev") is deliberately NOT valid: the checker treats a non-semver
// current version as "always offer the update".
func IsValidVersion(s string) bool {
	return semverRe.MatchString(CleanVersion(s))
}
