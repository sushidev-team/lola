package reviewpack

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Review records a human's inspection, not approval, verification or merge authority.
// It lives beside immutable packages and survives worktree/session cleanup.
type Review struct {
	Package string    `json:"package"`
	At      time.Time `json:"at"`
}

func reviewPath(home, session string) string {
	return filepath.Join(sessionDir(home, session), "review-"+hash([]byte(session))+".json")
}

func Reviewed(home, session string) (*Review, error) {
	if session == "" {
		return nil, errors.New("review requires a session")
	}
	root, err := os.OpenRoot(sessionDir(home, session))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	raw, err := readRegular(root, filepath.Base(reviewPath(home, session)), 1024)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Review
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	p, err := Read(home, session, r.Package)
	if err != nil {
		return nil, err
	}
	if p.Session != session || r.At.IsZero() {
		return nil, errors.New("review identity mismatch")
	}
	return &r, nil
}

// MarkReviewed is serialized by the daemon. expected prevents a stale client
// from silently replacing a newer review. Publication never calls this.
func MarkReviewed(home, session, id, expected string) (*Review, error) {
	p, err := Read(home, session, id)
	if err != nil {
		return nil, err
	}
	if p.Session != session {
		return nil, errors.New("select the package's repository before marking reviewed")
	}
	old, err := Reviewed(home, session)
	if err != nil {
		return nil, err
	}
	if old != nil && old.Package == id {
		return old, nil
	}
	previous := ""
	if old != nil {
		previous = old.Package
	}
	if previous != expected {
		return nil, errors.New("review changed; refresh before marking reviewed")
	}
	r := &Review{Package: id, At: time.Now().UTC()}
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(sessionDir(home, session), ".review-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(f.Name(), reviewPath(home, session)); err != nil {
		return nil, err
	}
	return r, nil
}
