package daemon

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/reviewpack"
)

func TestReviewPublicationRetainedAndDisplayOnly(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	s.Worktree = t.TempDir()
	d.sessions.Upsert(s)
	before, _ := d.sessions.Get(s.ID)
	m := reviewpack.Manifest{Version: 1, Title: "Review", Body: "Changes and limitations", Base: strings.Repeat("a", 40), Head: strings.Repeat("b", 40)}
	manifest, _ := json.Marshal(m)
	args, _ := json.Marshal(protocol.ReviewPublishArgs{Manifest: manifest})
	req := protocol.Request{Cmd: protocol.CmdReviewPublish, Session: s.ID, Args: args}
	if r := d.handle(context.Background(), req); !r.OK {
		t.Fatal(r.Error)
	}
	after, _ := d.sessions.Get(s.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("publication changed session state")
	}
	r := d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewPackages, Session: s.ID})
	var data protocol.ReviewPackagesData
	if err := json.Unmarshal(r.Data, &data); err != nil || len(data.Packages) != 1 {
		t.Fatalf("read: %s %v", r.Data, err)
	}
	// The storage read does not depend on a live record; a new daemon with the same home can inspect it.
	other := &Daemon{home: d.home}
	if r := other.handleReviewPackage(protocol.Request{Cmd: protocol.CmdReviewPackages, Session: s.ID}); !r.OK {
		t.Fatal(r.Error)
	}
	req.Session = "missing"
	if r := d.handle(context.Background(), req); r.OK {
		t.Fatal("unknown session published")
	}
	req.Session = s.ID
	secret, err := d.hookSecret()
	if err != nil {
		t.Fatal(err)
	}
	req.HookToken = hook.TokenFor(secret, s.ID)
	if err := d.bridgeAdmit(req); err != nil {
		t.Fatal(err)
	}
	req.Session = "other"
	if err := d.bridgeAdmit(req); err == nil {
		t.Fatal("token crossed sessions")
	}
	for _, cmd := range []string{protocol.CmdReviewPublish, protocol.CmdReviewPackages, protocol.CmdReviewImage} {
		if !remote.CommandDenied(cmd) {
			t.Fatalf("remote permits %s", cmd)
		}
		if cmd != protocol.CmdReviewPublish && hookBridgeCmds[cmd] {
			t.Fatalf("bridge permits read %s", cmd)
		}
	}
}
