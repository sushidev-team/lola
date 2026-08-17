package diffanchor

import "testing"

// sampleDiff covers the shapes a real PR diff mixes: an added+context hunk, a
// second hunk in the same file, a removal-only hunk, a brand-new file, a deleted
// file, and a binary file.
const sampleDiff = `diff --git a/internal/tmux/client.go b/internal/tmux/client.go
index 1111111..2222222 100644
--- a/internal/tmux/client.go
+++ b/internal/tmux/client.go
@@ -352,7 +352,9 @@ func (c *Client) ConfigureServer(ctx context.Context) error {
 	cmds := [][]string{
 		{"set-option", "-g", "status", "off"},
 	}
-	if c.MouseLegacy {
+	if c.Mouse {
+		cmds = append(cmds, []string{"set-option", "-g", "mouse", "on"})
+	}
+	if c.Focus {
 		cmds = append(cmds, []string{"set-option", "-g", "focus-events", "on"})
 	}
@@ -400 +403 @@ func (c *Client) Kill(ctx context.Context) error {
-	return c.old()
+	return c.new()
diff --git a/internal/tmux/gone.go b/internal/tmux/gone.go
deleted file mode 100644
--- a/internal/tmux/gone.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package tmux
-
-func gone() {}
diff --git a/internal/tmux/new.go b/internal/tmux/new.go
new file mode 100644
--- /dev/null
+++ b/internal/tmux/new.go
@@ -0,0 +1,2 @@
+package tmux
+
diff --git a/docs/shot.png b/docs/shot.png
index 3333333..4444444 100644
Binary files a/docs/shot.png and b/docs/shot.png differ
`

func TestParseAnchorsAddedAndContextLines(t *testing.T) {
	s := Parse(sampleDiff)

	// Hunk 1 starts at right-side line 352 and spans 9 right-side lines: three
	// context lines, then the added block, then two trailing context lines.
	for _, ln := range []int{352, 353, 354, 355, 356, 357, 358, 359, 360} {
		if !s.Has("internal/tmux/client.go", ln) {
			t.Errorf("client.go:%d should be anchorable", ln)
		}
	}
	// One past the hunk is not in the diff.
	if s.Has("internal/tmux/client.go", 361) {
		t.Error("client.go:361 is outside the hunk and must not be anchorable")
	}
	// A REMOVED line consumes no right-side number: the `-	if c.MouseLegacy {`
	// line must not have shifted the added block by one. 356 is the added
	// `cmds = append(…"mouse", "on"…)` line — the location a finding would name.
	if !s.Has("internal/tmux/client.go", 356) {
		t.Error("the added mouse-on line (356) should be anchorable")
	}
}

func TestParseSingleLineHunkAndSecondHunk(t *testing.T) {
	s := Parse(sampleDiff)
	// "@@ -400 +403 @@" — count omitted means exactly one right-side line.
	if !s.Has("internal/tmux/client.go", 403) {
		t.Error("client.go:403 (single-line hunk) should be anchorable")
	}
	if s.Has("internal/tmux/client.go", 404) {
		t.Error("a single-line hunk must not anchor a second line")
	}
}

func TestParseNewFileAnchorsDeletedFileDoesNot(t *testing.T) {
	s := Parse(sampleDiff)
	if !s.Has("internal/tmux/new.go", 1) || !s.Has("internal/tmux/new.go", 2) {
		t.Error("a new file's added lines should be anchorable")
	}
	// `+++ /dev/null`: the file has no right side at all.
	if s.HasFile("internal/tmux/gone.go") {
		t.Error("a deleted file must contribute no anchors")
	}
	// Its removed lines must not have been attributed to the PREVIOUS file
	// either — that would put a comment on an unrelated line.
	if s.Has("internal/tmux/client.go", 1) {
		t.Error("a deleted file's lines leaked into the previous file")
	}
}

func TestParseBinaryFileHasNoAnchors(t *testing.T) {
	s := Parse(sampleDiff)
	if s.HasFile("docs/shot.png") {
		t.Error("a binary file has no patch and must contribute no anchors")
	}
}

func TestParseEmptyAndGarbage(t *testing.T) {
	for _, in := range []string{"", "\n\n", "not a diff at all\njust prose\n"} {
		if n := Parse(in).Len(); n != 0 {
			t.Errorf("Parse(%q).Len() = %d, want 0", in, n)
		}
	}
	// The zero value is usable: no nil map panic, everything reports false.
	var zero Set
	if zero.Has("a.go", 1) || zero.HasFile("a.go") || zero.Len() != 0 {
		t.Error("the zero Set must be empty and safe")
	}
	if _, ok := zero.Nearest("a.go", 12, 3); ok {
		t.Error("the zero Set must never resolve an anchor")
	}
}

func TestParseTruncatedDiffKeepsWhatItSaw(t *testing.T) {
	// The diff fetch is size-bounded, so a clip mid-hunk is normal. The lines
	// already seen stay anchorable; nothing after the cut is invented.
	clipped := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,6 +1,6 @@
 package a
+var x = 1
`
	s := Parse(clipped)
	if !s.Has("a.go", 1) || !s.Has("a.go", 2) {
		t.Error("lines present before the clip should be anchorable")
	}
	if s.Has("a.go", 6) {
		t.Error("lines the clip removed must not be anchorable")
	}
}

func TestParseUnparseableHunkHeaderSkipsTheHunk(t *testing.T) {
	s := Parse(`diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,2 +oops @@
+var x = 1
@@ -10,1 +10,2 @@
+var y = 2
`)
	if s.Has("a.go", 1) {
		t.Error("a hunk with an unreadable header must anchor nothing")
	}
	// Recovery: the next well-formed hunk still anchors.
	if !s.Has("a.go", 10) {
		t.Error("a later valid hunk should still be parsed")
	}
}

func TestParseQuotedAndPrefixedPaths(t *testing.T) {
	s := Parse("diff --git \"a/pkg/od d.go\" \"b/pkg/od d.go\"\n" +
		"--- \"a/pkg/od d.go\"\n" +
		"+++ \"b/pkg/od d.go\"\n" +
		"@@ -1,1 +1,1 @@\n" +
		"+package pkg\n")
	if !s.Has("pkg/od d.go", 1) {
		t.Errorf("a git-quoted path should be unquoted and b/-stripped; got files %v", s.lines)
	}
}

func TestNearestSnapsWithinWindowAndPrefersEarlier(t *testing.T) {
	s := Parse(sampleDiff)
	const f = "internal/tmux/client.go"

	// Exact hit is returned unchanged.
	if got, ok := s.Nearest(f, 356, 3); !ok || got != 356 {
		t.Errorf("Nearest exact = (%d,%v), want (356,true)", got, ok)
	}
	// 362 is two past the hunk's last line (360): with a window of 3 it snaps
	// back to 360, the nearest anchorable line.
	if got, ok := s.Nearest(f, 362, 3); !ok || got != 360 {
		t.Errorf("Nearest(362) = (%d,%v), want (360,true)", got, ok)
	}
	// Nothing earlier in range: the snap resolves FORWARD to 403, the single
	// line of the second hunk.
	if got, ok := s.Nearest(f, 402, 1); !ok || got != 403 {
		t.Errorf("Nearest(402,1) = (%d,%v), want (403,true)", got, ok)
	}
	// Outside the window: no anchor, and the caller sends it to the summary body.
	if _, ok := s.Nearest(f, 500, 3); ok {
		t.Error("a line far outside every hunk must not resolve")
	}
	// window <= 0 disables snapping entirely.
	if _, ok := s.Nearest(f, 362, 0); ok {
		t.Error("window 0 must require an exact match")
	}
	// A file with no patch never resolves, however wide the window.
	if _, ok := s.Nearest("docs/shot.png", 1, 100); ok {
		t.Error("a file with no patch must never resolve")
	}
}

func TestNearestTieBreaksToTheEarlierLine(t *testing.T) {
	// Two hunks with a one-line gap: line 5 is equidistant from 4 and 6, and the
	// earlier line wins (a defect's declaration reads before its use).
	s := Parse(`diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -4,1 +4,1 @@
+four
@@ -6,1 +6,1 @@
+six
`)
	if got, ok := s.Nearest("a.go", 5, 3); !ok || got != 4 {
		t.Errorf("Nearest(5) = (%d,%v), want (4,true) — earlier line on a tie", got, ok)
	}
}
