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

// GroupBriefing renders ONE task for several dependency advisories of the
// same repository, fixed together in one pull request. Every item is
// validated like Briefing's; an item of another kind or repository is refused.
func GroupBriefing(items []hubproto.Item) (string, error) {
	if len(items) == 0 {
		return "", ErrRefusedItem
	}
	if len(items) == 1 {
		return Briefing(items[0])
	}
	var repo string
	advs := make([]hubproto.DepAdvisory, 0, len(items))
	for _, it := range items {
		if err := hubproto.Validate(it); err != nil {
			return "", fmt.Errorf("%w: %v", ErrRefusedItem, err)
		}
		if it.Kind != hubproto.KindDepAdvisory {
			return "", fmt.Errorf("%w: only dependency advisories are grouped", ErrRefusedItem)
		}
		if repo == "" {
			repo = it.DepAdvisory.Repo
		}
		if it.DepAdvisory.Repo != repo {
			return "", fmt.Errorf("%w: grouped advisories must share a repository", ErrRefusedItem)
		}
		advs = append(advs, *it.DepAdvisory)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Security chores: %d advisories in %s\n\n", len(advs), repo)
	fmt.Fprintf(&b, "Repository %s has these dependency advisories. Fix all of them in one branch and one pull request:\n\n", repo)
	for _, d := range advs {
		target := "the latest unaffected version (no fixed version is published, check the advisory)"
		if d.Fixed != "" {
			target = d.Fixed + " or later"
		}
		fmt.Fprintf(&b, "- %s (%s): %s package `%s`, affected `%s`, upgrade to %s\n", d.Advisory, d.Severity, d.Ecosystem, d.Package, d.Vulnerable, target)
	}
	b.WriteString("\nYou are on a new branch. Do this:\n\n")
	b.WriteString("1. Upgrade every listed package through the project's own package manager so the lockfiles stay consistent. Several advisories may share a package; one upgrade then fixes them all.\n")
	b.WriteString("2. Adapt the code to any breaking change of the upgrades. Keep the change minimal: no unrelated upgrades or refactors.\n")
	b.WriteString("3. Run the project's tests and linters and make them pass.\n")
	b.WriteString("4. Commit, push and open ONE pull request. Its body must list every advisory id with the package's old and new version.\n\n")
	b.WriteString("If one upgrade is impossible without a decision a person must make, do the others, and name the one you left and why in the pull request body instead of guessing.\n")
	return b.String(), nil
}

// MaxUpdateNote bounds the person's note in an update briefing.
const MaxUpdateNote = 2000

// UpdateBriefing is the section a person's "update all dependencies" adds to a
// hub briefing: which other updates to make, from lola's own template, then
// the person's note verbatim (control characters removed, bounded). The note
// is the person's instruction, never the hub's.
func UpdateBriefing(patch, minor, major bool, note string) string {
	var levels []string
	if patch {
		levels = append(levels, "patch")
	}
	if minor {
		levels = append(levels, "minor")
	}
	if major {
		levels = append(levels, "major")
	}
	var b strings.Builder
	b.WriteString("\n## Also update the other dependencies\n\n")
	if len(levels) == 0 {
		b.WriteString("Update no other dependency beyond the advisories above.\n")
	} else {
		fmt.Fprintf(&b, "In the same branch and pull request, update every other dependency of the repository to its newest %s release, through each package manager the repository uses.\n", strings.Join(levels, ", "))
		if !major {
			b.WriteString("Do not cross a major version, except where an advisory above requires it.\n")
		} else {
			b.WriteString("Major upgrades may break: adapt the code to them, and leave a package on its current major when the change needs a decision a person must make; name it in the pull request body.\n")
		}
		b.WriteString("List the updated packages with their old and new versions in the pull request body, separate from the advisories.\n")
	}
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f {
			return r
		}
		return -1
	}, strings.TrimSpace(note))
	if len([]rune(clean)) > MaxUpdateNote {
		clean = string([]rune(clean)[:MaxUpdateNote])
	}
	if clean != "" {
		b.WriteString("\n## Notes from the person who started this\n\n")
		b.WriteString(clean)
		b.WriteString("\n")
	}
	return b.String()
}

func redBumpBriefing(r hubproto.RedBumpPR) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Dependency chore: fix the failing checks of %s#%d\n\n", r.Repo, r.PR)
	if r.Package == "" {
		// A grouped update (C10): the PR bumps several packages and the item
		// names none. The agent reads which from the branch, never from a
		// title or a hub field.
		fmt.Fprintf(&b, "Pull request #%d in %s was opened by %s to bump a group of dependencies, and its checks fail.\n\n", r.PR, r.Repo, r.Author)
		fmt.Fprintf(&b, "You are on that pull request's branch `%s`. Do this:\n\n", r.Branch)
		b.WriteString("1. Find which packages the branch changes (compare its manifests and lockfiles with the base branch), then find out why the checks fail (run the tests and linters locally).\n")
		b.WriteString("2. Fix what the bumps broke: adapt the code, tests or configuration. Keep the versions the bot chose and keep the change minimal.\n")
	} else {
		fmt.Fprintf(&b, "Pull request #%d in %s was opened by %s to bump `%s`, and its checks fail.\n\n", r.PR, r.Repo, r.Author, r.Package)
		fmt.Fprintf(&b, "You are on that pull request's branch `%s`. Do this:\n\n", r.Branch)
		b.WriteString("1. Find out why the checks fail (run the tests and linters locally).\n")
		b.WriteString("2. Fix what the bump broke: adapt the code, tests or configuration. Keep the version the bot chose and keep the change minimal.\n")
	}
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
