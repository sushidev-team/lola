package worktree

import (
	"context"
	"strings"
	"testing"
)

func TestDeleteMergedRemoteBranch(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	const branch = "lola/hub-advisories-20261114-0900"
	const url = "git@github.com:acme/nori.git"
	ref := "refs/heads/" + branch
	for _, tc := range []struct {
		name                         string
		stubs                        []stub
		wantErr, wantPush, wantPrune bool
	}{
		{name: "merged head", wantPush: true, wantPrune: true},
		{name: "already deleted", stubs: []stub{{match: "ls-remote", exit: 2}}, wantPrune: true},
		{name: "new push", stubs: []stub{{match: "ls-remote", stdout: strings.Repeat("a", 40) + "\t" + ref}}, wantErr: true},
		{name: "authentication failure", stubs: []stub{{match: "ls-remote", exit: 128}}, wantErr: true},
		{name: "unknown ref", stubs: []stub{{match: "ls-remote", stdout: ""}}, wantErr: true},
		{name: "fork remote", stubs: []stub{{match: "remote get-url", stdout: "git@github.com:someone/nori.git"}}, wantErr: true},
		{name: "multiple push remotes", stubs: []stub{{match: "remote get-url", stdout: url + "\n" + url}}, wantErr: true},
		{name: "lease refused", stubs: []stub{{match: "push --no-verify", exit: 1}}, wantErr: true, wantPush: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubs := append(tc.stubs, stub{match: "remote get-url", stdout: url}, stub{match: "ls-remote", stdout: sha + "\t" + ref})
			bin, log := fakeGit(t, stubs...)
			p := testProject("/repo")
			p.Repo = "acme/nori"
			err := (&Manager{GitBin: bin}).DeleteMergedRemoteBranch(context.Background(), p, branch, sha)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error %v", err, tc.wantErr)
			}
			args := loggedArgs(t, log)
			push := "push --no-verify --force-with-lease=" + ref + ":" + sha + " -- " + url + " :" + ref
			if strings.Contains(args, push) != tc.wantPush {
				t.Fatalf("pinned push = %v, want %v: %s", strings.Contains(args, push), tc.wantPush, args)
			}
			if strings.Contains(args, "update-ref -d refs/remotes/origin/"+branch+" "+sha) != tc.wantPrune {
				t.Fatalf("unexpected tracking ref cleanup: %s", args)
			}
		})
	}
}

func TestDeleteMergedRemoteBranchRejectsUnpinnedOrDefault(t *testing.T) {
	for _, tc := range []struct{ branch, sha string }{
		{"main", strings.Repeat("a", 40)},
		{"", strings.Repeat("a", 40)},
		{"fix", ""},
		{"fix", strings.Repeat("x", 40)},
	} {
		bin, log := fakeGit(t)
		p := testProject("/repo")
		p.Repo = "acme/nori"
		if err := (&Manager{GitBin: bin}).DeleteMergedRemoteBranch(context.Background(), p, tc.branch, tc.sha); err == nil {
			t.Fatalf("accepted branch %q sha %q", tc.branch, tc.sha)
		}
		if args := loggedArgs(t, log); args != "" {
			t.Fatalf("invalid cleanup executed git: %s", args)
		}
	}
}
