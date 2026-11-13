package worktree

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckoutExisting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stubs   []stub
		want    string
		wantErr bool
		inUse   bool
	}{
		{name: "local", want: "worktree add {dir} feature"},
		{name: "remote", stubs: []stub{{match: "show-ref", exit: 1}, {match: "for-each-ref", stdout: "refs/remotes/origin/feature"}}, want: "worktree add --track -b feature {dir} refs/remotes/origin/feature"},
		{name: "upstream", stubs: []stub{{match: "show-ref", exit: 1}, {match: "for-each-ref", stdout: "refs/remotes/upstream/feature"}}, want: "worktree add --track -b feature {dir} refs/remotes/upstream/feature"},
		{name: "origin wins", stubs: []stub{{match: "show-ref", exit: 1}, {match: "for-each-ref", stdout: "refs/remotes/backup/feature\nrefs/remotes/origin/feature"}}, want: "worktree add --track -b feature {dir} refs/remotes/origin/feature"},
		{name: "ambiguous", stubs: []stub{{match: "show-ref", exit: 1}, {match: "for-each-ref", stdout: "refs/remotes/backup/feature\nrefs/remotes/upstream/feature"}}, wantErr: true},
		{name: "unknown", stubs: []stub{{match: "show-ref", exit: 1}}, wantErr: true},
		{name: "in use", stubs: []stub{{match: "worktree add", stderr: "fatal: 'feature' is already used by worktree at '/other'", exit: 1}}, wantErr: true, inUse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin, log := fakeGit(t, tc.stubs...)
			m := Manager{GitBin: bin, Root: t.TempDir()}
			p := testProject(t.TempDir())
			dir, err := m.CheckoutExisting(context.Background(), p, "session", "feature")
			if (err != nil) != tc.wantErr {
				t.Fatalf("dir=%s err=%v", dir, err)
			}
			calls := loggedArgs(t, log)
			if tc.want != "" && !strings.Contains(calls, strings.ReplaceAll(tc.want, "{dir}", filepath.Join(m.Root, p.Name, "session"))) {
				t.Fatalf("wrong checkout:\n%s", calls)
			}
			if tc.inUse && !errors.Is(err, ErrBranchCheckedOut) {
				t.Fatalf("want checked-out error, got %v", err)
			}
			if tc.wantErr && !tc.inUse && strings.Contains(calls, "worktree add") {
				t.Fatalf("must not create unknown branch:\n%s", calls)
			}
			if strings.Contains(calls, "--force") || strings.Contains(calls, " -B ") || strings.Contains(calls, " fetch ") {
				t.Fatalf("must not reset or fetch:\n%s", calls)
			}
		})
	}
}

func TestCheckoutExistingRejectsInvalidNames(t *testing.T) {
	for _, branch := range []string{"", "-b", "@{-1}"} {
		bin, log := fakeGit(t)
		m := Manager{GitBin: bin, Root: t.TempDir()}
		if _, err := m.CheckoutExisting(context.Background(), testProject(t.TempDir()), "session", branch); err == nil {
			t.Fatalf("accepted %q", branch)
		}
		if calls := loggedArgs(t, log); calls != "" {
			t.Fatalf("invalid branch reached git: %s", calls)
		}
	}
}
