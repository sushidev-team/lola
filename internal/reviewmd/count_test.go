package reviewmd

import "testing"

func TestCount(t *testing.T) {
	body := "**[blocker]** `a.go:1` — x\n- **Gist:** g\n\n**[major]** `b.go:2` — y\n\n**[minor]** `c.go:3` — z\n\n**[nit]** `d.go:4` — w\n"
	if got, want := Count(body), (Counts{Blocker: 1, Major: 1, Minor: 2}); got != want {
		t.Fatalf("Count = %+v, want %+v", got, want)
	}
	if got := Count("  \n"); got != (Counts{}) {
		t.Fatalf("empty body = %+v, want zeros", got)
	}
	if got := Count("Looks mostly fine, but the cache is never invalidated."); got != (Counts{Other: 1}) {
		t.Fatalf("plain text = %+v, want one Other", got)
	}
}
