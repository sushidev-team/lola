package goalwork

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
)

type Submission struct {
	Goal    string            `json:"goal"`
	Task    string            `json:"task"`
	Attempt string            `json:"attempt"`
	Heads   map[string]string `json:"heads"`
	Summary string            `json:"summary"`
}

// ReadSubmission reads a bounded worker claim. It proves no checks or task
// completion; head objects still have to cross the bundle boundary and pass
// independent verification. A missing file means no submission yet.
func (m *Manager) ReadSubmission(goal, task, attempt string, members []string) (Submission, error) {
	dir, err := m.LaunchDirectory(goal, attempt)
	if err != nil {
		return Submission{}, err
	}
	root, err := os.OpenRoot(filepath.Join(dir, "workspace"))
	if err != nil {
		return Submission{}, err
	}
	defer root.Close()
	fi, err := root.Lstat("submission.json")
	if err != nil {
		return Submission{}, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 65536 {
		return Submission{}, errors.New("submission must be a regular JSON file of at most 64 KiB")
	}
	f, err := root.OpenFile("submission.json", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Submission{}, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return Submission{}, errors.New("submission changed to a nonregular file")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 65537))
	decoder.DisallowUnknownFields()
	var s Submission
	if err := decoder.Decode(&s); err != nil {
		return s, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return s, errors.New("submission has trailing content")
	}
	if s.Goal != goal || s.Task != task || s.Attempt != attempt || len(s.Heads) == 0 || len(s.Heads) > len(members) || len(s.Summary) > 16000 {
		return s, errors.New("submission identity, scope or summary is invalid")
	}
	for member, head := range s.Heads {
		if !slices.Contains(members, member) || validSHA(head) != nil {
			return s, errors.New("submission contains an unassigned member or invalid commit")
		}
	}
	return s, nil
}
