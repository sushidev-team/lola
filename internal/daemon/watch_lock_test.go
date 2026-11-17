package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/eventbox"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/session"
)

// TestNoLockInversion re-watches sessions and feed:<id> in a loop on several
// boxes while other goroutines drive store commits and hold a feed reader
// across a "scan". Every watch must get its first event within 2 s; a deadlock
// shows as that timeout (and the race detector as a failure).
func TestNoLockInversion(t *testing.T) {
	w := newWatchTest(t)
	w.session("s1")
	f := &fakeFeed{epoch: "e"}
	f.add(1, "a")
	// The fake reader is held (a scan) by a desktop-style puller.
	var scan sync.Mutex
	f.install(w.h)
	open := w.h.openFeed
	w.h.openFeed = func(ctx context.Context, id string) (feedView, feedAvail) {
		scan.Lock()
		v, a := open(ctx, id)
		if a != feedReady {
			scan.Unlock()
			return v, a
		}
		v.Close = scan.Unlock
		return v, a
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan string, 64)

	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			box := eventbox.New(w.h.Resnapshot)
			topic := "sessions"
			if g%2 == 1 {
				topic = "feed:s1"
			}
			for i := 0; i < 15 && ctx.Err() == nil; i++ {
				stop, res := w.h.Watch(ctx, remote.Peer{DeviceID: "d" + string(rune('a'+g/2))}, topic, "w"+string(rune('a'+i)), box)
				if !res.OK {
					errs <- "refused: " + res.Code
					return
				}
				got := false
				for !got {
					c, cc := context.WithTimeout(ctx, 2*time.Second)
					it, ok := box.Next(c)
					cc()
					if !ok {
						errs <- "no event within 2s for " + topic
						return
					}
					if ev, isReset := it.Event.(interface{ Part() bool }); isReset && ev.Part() {
						got = true
					}
				}
				stop()
				box.Stop(topic, "w"+string(rune('a'+i)))
			}
		}(g)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200 && ctx.Err() == nil; i++ {
				switch g % 2 {
				case 0:
					w.d.sessions.Update("s1", func(s *session.Session) bool { s.AtPrompt = !s.AtPrompt; return true })
				default:
					scan.Lock()
					f.add(int64(10+g*1000+i), "x")
					scan.Unlock()
					w.h.PokeSession("s1", pokeFeed)
				}
				time.Sleep(time.Millisecond)
			}
		}(g)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: watch loops did not finish")
	}
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	_ = protocol.TopicSessions
}
