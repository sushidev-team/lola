package hub

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/hubproto"
)

// ErrRefusedItem is returned by Briefing for an item that does not validate.
var ErrRefusedItem = errors.New("hub: refusing to brief an item that does not validate")

// Briefing renders the agent's task for a hub chore from lola's OWN template
// for its kind. The hub never writes a prompt: the item is validated first
// (hubproto.Validate — every field matched against a strict pattern, so none
// can carry a newline, a markdown fence or an instruction), and only its typed
// fields are interpolated. An item that does not validate is refused.
func Briefing(item hubproto.Item) (string, error) {
	if err := hubproto.Validate(item); err != nil {
		return "", fmt.Errorf("%w: %v", ErrRefusedItem, err)
	}
	switch item.Kind {
	case hubproto.KindDepAdvisory:
		return depAdvisoryBriefing(*item.DepAdvisory), nil
	case hubproto.KindRedBumpPR:
		return redBumpBriefing(*item.RedBumpPR), nil
	}
	return "", ErrRefusedItem // unreachable: Validate admits only known kinds
}

func depAdvisoryBriefing(d hubproto.DepAdvisory) string {
	target := "the latest version that is not affected (no fixed version is published, check the advisory)"
	if d.Fixed != "" {
		target = "version " + d.Fixed + " or later"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Security chore: %s in %s\n\n", d.Advisory, d.Package)
	fmt.Fprintf(&b, "Repository %s depends on the %s package `%s`, which is affected by advisory %s (severity: %s). Affected versions: `%s`.\n\n",
		d.Repo, d.Ecosystem, d.Package, d.Advisory, d.Severity, d.Vulnerable)
	b.WriteString("You are on a new branch. Do this:\n\n")
	fmt.Fprintf(&b, "1. Upgrade `%s` to %s, through the project's own package manager so the lockfile stays consistent.\n", d.Package, target)
	b.WriteString("2. Adapt the code to any breaking change of the upgrade. Keep the change minimal: no unrelated upgrades or refactors.\n")
	b.WriteString("3. Run the project's tests and linters and make them pass.\n")
	fmt.Fprintf(&b, "4. Commit, push and open a pull request. Its body must name the advisory id %s and the old and new version.\n\n", d.Advisory)
	b.WriteString("If the package is no longer used, or the upgrade is impossible without a decision a person must make, stop and say so instead of guessing.\n")
	return b.String()
}

func redBumpBriefing(r hubproto.RedBumpPR) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Dependency chore: fix the failing checks of %s#%d\n\n", r.Repo, r.PR)
	fmt.Fprintf(&b, "Pull request #%d in %s was opened by %s to bump `%s`, and its checks fail.\n\n", r.PR, r.Repo, r.Author, r.Package)
	fmt.Fprintf(&b, "You are on that pull request's branch `%s`. Do this:\n\n", r.Branch)
	b.WriteString("1. Find out why the checks fail (run the tests and linters locally).\n")
	b.WriteString("2. Fix what the bump broke: adapt the code, tests or configuration. Keep the version the bot chose and keep the change minimal.\n")
	fmt.Fprintf(&b, "3. Commit and push to the same branch `%s`. Do not open a new pull request and do not merge.\n\n", r.Branch)
	b.WriteString("If the bump cannot work without a decision a person must make, stop and say so instead of guessing.\n")
	return b.String()
}

// Slug is a short, path- and ref-safe name for an item ([a-z0-9-], at most 60
// characters), for a branch or session suffix. It is derived from validated
// fields only and never from a lola session id.
func Slug(item hubproto.Item) string {
	if hubproto.Validate(item) != nil {
		return ""
	}
	var raw string
	switch item.Kind {
	case hubproto.KindDepAdvisory:
		raw = item.DepAdvisory.Advisory + "-" + item.DepAdvisory.Package
	case hubproto.KindRedBumpPR:
		raw = "bump-pr-" + strconv.Itoa(item.RedBumpPR.PR)
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(raw) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
