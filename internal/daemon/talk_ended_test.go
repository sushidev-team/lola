package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshstore"
)

// A session's story outlives the session. By the time a client opens one, the session record is
// gone, so the reader has only what dropSession wrote: the segment names and the directory's name.
// The worktree may be gone too, and the path of the transcript was never trusted anyway.
func TestEndedFeedReadsSegments(t *testing.T) {
	r := newFeedRig(t)
	now := time.Now()
	r.write(t,
		feedUser("00000000-0000-4000-8000-000000000001", "Why does the redirect loop?", now.Add(-2*time.Minute)),
		feedText("00000000-0000-4000-8000-000000000002", "The cookie was cleared after the redirect fired.", now.Add(-time.Minute)),
	)
	r.d.dropSession(r.s)
	if _, ok := r.d.sessions.Get(r.s.ID); ok {
		t.Fatal("the record should be gone")
	}
	ended := sshstore.ReadEnded(r.d.home, r.s.ID)
	if ended == nil || len(ended.Segments) != 1 || ended.Segments[0] != feedSeg {
		t.Fatalf("ended record: %+v", ended)
	}

	data := r.feed(t)
	if data.Health != protocol.FeedOK {
		t.Fatalf("health %q", data.Health)
	}
	found := false
	for _, it := range data.Items {
		found = found || strings.Contains(it.Text, "The cookie was cleared")
	}
	if !found {
		t.Fatalf("the story did not come back: %+v", data.Items)
	}

	state, err := r.d.handleTalkState(context.Background(), protocol.TalkStateArgs{Session: r.s.ID})
	if err != nil || state.Composer != protocol.ComposerGone {
		t.Fatalf("talkState of an ended session: %+v %v", state, err)
	}
	timeline, err := r.d.handleSSHTimeline(protocol.SSHTimelineArgs{Session: r.s.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Items) == 0 {
		t.Fatalf("the store's half of the story is gone: %+v", timeline)
	}
}

// The record names a segment, and a record is a file an unsandboxed agent could have reached. The
// same no-follow rule holds for an ended session as for a live one.
func TestEndedFeedRefusesASymlinkedSegment(t *testing.T) {
	r := newFeedRig(t)
	outside := filepath.Join(t.TempDir(), "secret.jsonl")
	if err := os.WriteFile(outside, []byte(feedText("00000000-0000-4000-8000-000000000003", "not yours", time.Now())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, r.path); err != nil {
		t.Fatal(err)
	}
	r.d.dropSession(r.s)
	for _, it := range r.feed(t).Items {
		if strings.Contains(it.Text, "not yours") {
			t.Fatalf("followed a symlink: %+v", it)
		}
	}
}
