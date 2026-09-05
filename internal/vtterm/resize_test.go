package vtterm

import (
	"os/exec"
	"sync"
	"testing"
)

// TestScreenReportsTheGridItActuallyHas is a regression for a torn snapshot.
//
// Resize writes t.w/t.h under t.mu and only THEN takes tapMu to reshape the
// emulator, while screen() used to read its dimensions from Size(). A reading
// taken in between therefore reported the new size over the old grid — in the
// one structure whose entire documented purpose is to be "one coherent
// reading", and which is the whole reason a remote subscriber can paint an
// alternate-screen pane at all. panebus pushes a corrective resync straight
// after a resize, so the usual cost is one bad frame; a subscribe landing in
// the window paints a client at the wrong size until something else moves.
//
// The emulator is read INSIDE the same WithScreen callback, so tapMu guarantees
// the grid cannot have changed between the two observations: any mismatch is
// the snapshot describing a grid that never existed.
func TestScreenReportsTheGridItActuallyHas(t *testing.T) {
	term, err := New(exec.Command("cat"), 80, 24)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			if i%2 == 0 {
				term.Resize(120, 40)
			} else {
				term.Resize(80, 24)
			}
		}
	}()

	for i := 0; i < 2000; i++ {
		term.WithScreen(func(s Screen) {
			if gw, gh := term.emu.Width(), term.emu.Height(); s.W != gw || s.H != gh {
				t.Errorf("Screen says %dx%d, the grid is %dx%d: the snapshot is torn", s.W, s.H, gw, gh)
			}
		})
		if t.Failed() {
			break
		}
	}
	wg.Wait()
}
