package projectgit

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type Stash struct {
	SHA      string `json:"sha"`
	Selector string `json:"selector"`
	Subject  string `json:"subject"`
	Date     string `json:"date"`
}

var stashSelectorPattern = regexp.MustCompile(`^stash@\{[0-9]+\}$`)

func (c Client) Stashes(ctx context.Context, dir string) ([]Stash, error) {
	out, err := c.git(ctx, dir, "", "stash", "list", "-z", "--format=%H%x00%gd%x00%gs%x00%cI")
	if err != nil {
		return nil, err
	}
	result := []Stash{}
	if len(out) == 0 {
		return result, nil
	}
	fields := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(fields)%4 != 0 {
		return nil, errors.New("cannot read the stash list; refresh the repository")
	}
	for i := 0; i < len(fields); i += 4 {
		if !shaPattern.MatchString(fields[i]) || !stashSelectorPattern.MatchString(fields[i+1]) {
			return nil, errors.New("cannot read the stash list; refresh the repository")
		}
		result = append(result, Stash{SHA: fields[i], Selector: fields[i+1], Subject: fields[i+2], Date: fields[i+3]})
	}
	return result, nil
}

func (c Client) stashEntry(ctx context.Context, dir, sha string) (Stash, error) {
	if !shaPattern.MatchString(sha) {
		return Stash{}, errors.New("refresh the repository and choose a stash")
	}
	entries, err := c.Stashes(ctx, dir)
	if err != nil {
		return Stash{}, err
	}
	for _, entry := range entries {
		if entry.SHA == sha {
			return entry, nil
		}
	}
	return Stash{}, errors.New("the selected stash no longer exists; refresh the repository")
}

func (c Client) stashCheckout(ctx context.Context, dir, expectedHead string, clean bool) error {
	if !shaPattern.MatchString(expectedHead) {
		return errors.New("refresh the repository before changing stashes; the checkout needs a commit")
	}
	if err := c.requireHead(ctx, dir, expectedHead); err != nil {
		return errors.New("HEAD changed since the stash action was chosen; refresh and try again")
	}
	operation, err := c.Operation(ctx, dir)
	if err != nil {
		return err
	}
	if operation != "" {
		return fmt.Errorf("finish or abort the %s before changing stashes", operation)
	}
	files, err := c.status(ctx, dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.Conflict {
			return errors.New("resolve conflicted files before changing stashes")
		}
	}
	if clean && len(files) > 0 {
		return errors.New("commit or stash working changes before applying a stash")
	}
	if !clean && len(files) == 0 {
		return errors.New("there are no working changes to stash")
	}
	return nil
}

// StashPush includes untracked files, but never ignored files. This is only
// called for an explicit human action; background Git operations do not stash.
func (c Client) StashPush(ctx context.Context, dir, message, expectedHead string) error {
	if len(message) > 1024 || strings.ContainsRune(message, 0) || strings.ContainsAny(message, "\r\n") {
		return errors.New("enter a single-line stash message of at most 1 KB")
	}
	if err := c.stashCheckout(ctx, dir, expectedHead, false); err != nil {
		return err
	}
	if strings.TrimSpace(message) == "" {
		message = "lola workspace"
	}
	// stash builds its own literal pathspecs for internal cleanup. Inheriting
	// --literal-pathspecs makes Git 2.55 save untracked files without removing
	// them. No caller-controlled pathspec reaches this command.
	_, err := c.git(ctx, dir, "", "--no-literal-pathspecs", "stash", "push", "--include-untracked", "--message", message)
	if err != nil {
		return err
	}
	remaining, err := c.status(ctx, dir)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return errors.New("some working changes remain after stashing; inspect the checkout before switching branches")
	}
	return nil
}

// StashApply addresses the immutable stash commit, not a moving stash@{N}.
// Pop drops it only after a successful apply and a fresh identity check.
func (c Client) StashApply(ctx context.Context, dir, sha, expectedHead string, pop bool) error {
	entry, err := c.stashEntry(ctx, dir, sha)
	if err != nil {
		return err
	}
	if err = c.stashCheckout(ctx, dir, expectedHead, true); err != nil {
		return err
	}
	if _, err = c.git(ctx, dir, "", "stash", "apply", "--index", entry.SHA); err != nil {
		return fmt.Errorf("stash was kept; inspect the working files and resolve any conflicts: %w", err)
	}
	if !pop {
		return nil
	}
	if err = c.dropStashEntry(ctx, dir, entry); err != nil {
		return fmt.Errorf("changes were applied, but the stash was kept: %w", err)
	}
	return nil
}

// StashDrop must be preceded by a confirmation identifying the stash in the UI.
func (c Client) StashDrop(ctx context.Context, dir, sha string) error {
	entry, err := c.stashEntry(ctx, dir, sha)
	if err != nil {
		return err
	}
	return c.dropStashEntry(ctx, dir, entry)
}

func (c Client) dropStashEntry(ctx context.Context, dir string, entry Stash) error {
	current, err := c.stashEntry(ctx, dir, entry.SHA)
	if err != nil {
		return err
	}
	if current.Selector != entry.Selector {
		return errors.New("the stash list changed; refresh before dropping the stash")
	}
	sha, err := c.text(ctx, dir, "rev-parse", "--verify", entry.Selector)
	if err != nil || sha != entry.SHA {
		return errors.New("the stash list changed; refresh before dropping the stash")
	}
	_, err = c.git(ctx, dir, "", "stash", "drop", entry.Selector)
	return err
}
