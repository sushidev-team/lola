package reviewrun

// Tests for the visible-pass hand-off protocol: a stale result can never be
// read as the current pass's, the findings survive verbatim, and every outcome
// class maps back onto the sentinel the review chain keys on.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/review"
	"github.com/sushidev-team/lola/internal/reviewagent"
)

func TestReadReportsNotDoneBeforeTheChildWrites(t *testing.T) {
	dir := t.TempDir()
	_, _, done, err := Read(dir)
	if err != nil {
		t.Fatalf("Read on an empty dir: %v", err)
	}
	if done {
		t.Error("an empty state dir must read as not-done")
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	findings := "**[blocker]** `x.go:12` — boom\n- **What:** it explodes."
	if err := Write(dir, findings, nil); err != nil {
		t.Fatal(err)
	}
	st, got, done, err := Read(dir)
	if err != nil || !done {
		t.Fatalf("Read = (done %v, err %v), want done", done, err)
	}
	if got != findings {
		t.Errorf("findings = %q, want them verbatim", got)
	}
	if st.Class != ClassOK || st.Err() != nil {
		t.Errorf("status = %+v (err %v), want a clean ok", st, st.Err())
	}
}

// Prepare must clear the PREVIOUS pass's files: a stale status would otherwise
// be read instantly as this pass's completion.
func TestPrepareClearsAStaleResult(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, "OLD-FINDING", nil); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(dir); err != nil {
		t.Fatal(err)
	}
	if _, _, done, _ := Read(dir); done {
		t.Fatal("Prepare must leave the state dir with no completion signal")
	}
	if _, err := os.Stat(filepath.Join(dir, findingsName)); !os.IsNotExist(err) {
		t.Errorf("stale findings survived Prepare (stat err = %v)", err)
	}
}

func TestClassifyAndErrRoundTripEverySentinel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		class    string
		wantIs   error
		fallback bool // does the chain advance on it?
	}{
		{"clean", nil, ClassOK, nil, false},
		{"claude timeout", reviewagent.ErrTimeout, ClassTimeout, reviewagent.ErrTimeout, true},
		{"cli timeout", review.ErrTimeout, ClassTimeout, reviewagent.ErrTimeout, true},
		{"quota", review.ErrQuota, ClassQuota, reviewagent.ErrQuota, true},
		{"not found", review.ErrNotFound, ClassNotFound, reviewagent.ErrNotFound, true},
		{"auth", reviewagent.ErrAuth, ClassAuth, reviewagent.ErrAuth, false},
		{"exit", review.ErrExit, ClassExit, reviewagent.ErrExit, false},
		{"anything else", errors.New("git diff failed"), ClassFailed, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			class, msg := Classify(tc.err)
			if class != tc.class {
				t.Fatalf("Classify(%v) = %q, want %q", tc.err, class, tc.class)
			}
			back := Status{Class: class, Message: msg}.Err()
			if tc.wantIs == nil && tc.err == nil {
				if back != nil {
					t.Errorf("a clean pass must map back to no error, got %v", back)
				}
				return
			}
			if tc.wantIs != nil && !errors.Is(back, tc.wantIs) {
				t.Errorf("Status.Err() = %v, want it to match %v", back, tc.wantIs)
			}
			if back == nil {
				t.Fatal("a failed pass must map back to an error")
			}
		})
	}
}

// A multi-line error cannot break the one-line status format.
func TestWriteFlattensAMultiLineMessage(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, "", errors.New("line one\nline two")); err != nil {
		t.Fatal(err)
	}
	st, _, done, err := Read(dir)
	if err != nil || !done {
		t.Fatalf("Read = (done %v, err %v)", done, err)
	}
	if st.Class != ClassFailed {
		t.Errorf("class = %q, want %q", st.Class, ClassFailed)
	}
	if got := st.Message; got != "line one line two" {
		t.Errorf("message = %q, want it flattened onto one line", got)
	}
}
