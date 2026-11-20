package daemon

import (
	"context"
	"testing"

	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/session"
)

type countNotifier struct{ notes []notify.Note }

func (c *countNotifier) Notify(_ context.Context, n notify.Note) { c.notes = append(c.notes, n) }

func TestNoteKeyAgentFiresOncePerEpisode(t *testing.T) {
	n := &countNotifier{}
	d := &Daemon{notifier: n}
	s := session.Session{ID: "app-1"}
	ctx := context.Background()
	d.noteKeyAgent(ctx, s, true)
	d.noteKeyAgent(ctx, s, true)
	if len(n.notes) != 1 || n.notes[0].Priority != notify.Urgent {
		t.Fatalf("notes = %+v, want one urgent", n.notes)
	}
	d.noteKeyAgent(ctx, s, false)
	d.noteKeyAgent(ctx, s, true)
	if len(n.notes) != 2 {
		t.Fatalf("a new episode should announce again, got %d", len(n.notes))
	}
}
