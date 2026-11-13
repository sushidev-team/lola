package goalplan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
)

// EditRequest changes only the task proposal. The original user brief, source
// snapshots, model output and paid usage remain immutable and attributable.
type EditRequest struct {
	ID       string `json:"id"`
	Project  string `json:"project"`
	Revision uint64 `json:"revision"`
	EditID   string `json:"edit_id"`
	Reason   string `json:"reason"`
	Output   Output `json:"output"`
}
type Edit struct {
	Request  EditRequest `json:"request"`
	Revision uint64      `json:"revision"`
	At       time.Time   `json:"at"`
}

func DecodeEdit(body []byte) (EditRequest, error) {
	var a EditRequest
	if len(body) > 96<<10 {
		return a, errors.New("proposal edit exceeds 96 KiB")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return a, errors.New("trailing proposal edit")
	}
	return a, a.Validate()
}
func (a EditRequest) Validate() error {
	if !identity.MatchString(a.ID) || !identity.MatchString(a.EditID) || a.Project == "" || a.Revision == 0 || len(strings.TrimSpace(a.Reason)) < 8 || len(a.Reason) > 2000 || strings.ContainsFunc(a.Reason, func(r rune) bool {
		return (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.Is(unicode.Cf, r)
	}) {
		return errors.New("edit requires proposal and edit UUIDs, project, reviewed revision and 8–2000 byte reason")
	}
	body, err := json.Marshal(a.Output)
	if err != nil {
		return err
	}
	if len(body) > 64<<10 {
		return errors.New("proposal output exceeds 64 KiB")
	}
	return nil
}
func sameEditValue(a, b any) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(aa, bb)
}
func (r Record) HasEdit(id string) bool {
	for _, e := range r.Edits {
		if e.Request.EditID == id {
			return true
		}
	}
	return false
}

// Edit is an atomic revision compare-and-swap. Identical request replays return
// the current record, including later edits or acceptance; they never rewind it.
func (s *Store) Edit(a EditRequest) (Record, error) {
	if err := a.Validate(); err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data.Records[a.ID]
	if !ok || r.Input.Request.Project != a.Project {
		return Record{}, errors.New("proposal unavailable in this project")
	}
	for _, e := range r.Edits {
		if e.Request.EditID == a.EditID {
			if !sameEditValue(e.Request, a) {
				return Record{}, errors.New("edit identity already belongs to a different decision")
			}
			return clone(r), nil
		}
	}
	if r.State != "proposed" || r.Output == nil || r.Revision != a.Revision || r.Input.Repair != nil {
		return Record{}, errors.New("review an unaccepted initial proposal's current revision before editing")
	}
	if len(r.Edits) >= 50 {
		return Record{}, errors.New("proposal edit history reached its 50 revision limit")
	}
	body, _ := json.Marshal(a.Output)
	if _, err := Parse(body, r.Input); err != nil {
		return Record{}, err
	}
	if sameEditValue(*r.Output, a.Output) {
		return Record{}, errors.New("proposal output is unchanged")
	}
	r = clone(r)
	if r.ModelOutput == nil {
		r.ModelOutput = r.Output
	}
	r.Revision++
	r.Edits = append(r.Edits, Edit{Request: a, Revision: r.Revision, At: time.Now().UTC()})
	r.Output = &a.Output
	if err := s.commit(r); err != nil {
		return Record{}, err
	}
	return clone(r), nil
}
func validateEdits(r Record, version int) error {
	if len(r.Edits) == 0 {
		if r.ModelOutput != nil {
			return errors.New("model output has no edit history")
		}
		return nil
	}
	if version < 2 || r.ModelOutput == nil || r.Output == nil || len(r.Edits) > 50 || r.Input.Repair != nil || (r.State != "proposed" && r.State != "accepted") {
		return errors.New("invalid edited proposal record")
	}
	if err := ValidateOutput(*r.ModelOutput, r.Input); err != nil {
		return err
	}
	baseRevision := uint64(2)
	if r.Usage != nil {
		baseRevision++
	}
	if r.Edits[0].Request.Revision != baseRevision {
		return errors.New("proposal edit history does not start at the model result")
	}
	seen := map[string]bool{}
	var previous Edit
	for i, e := range r.Edits {
		a := e.Request
		if err := a.Validate(); err != nil {
			return err
		}
		if a.ID != r.ID || a.Project != r.Input.Request.Project || seen[a.EditID] || e.Revision != a.Revision+1 || e.At.Before(r.FinishedAt) || (i > 0 && (a.Revision != previous.Revision || e.At.Before(previous.At))) {
			return errors.New("invalid proposal edit lineage")
		}
		body, _ := json.Marshal(a.Output)
		if _, err := Parse(body, r.Input); err != nil {
			return fmt.Errorf("invalid edited task graph: %w", err)
		}
		seen[a.EditID] = true
		previous = e
	}
	expected := previous.Revision
	if r.State == "accepted" {
		expected++
	}
	if r.Revision != expected || !sameEditValue(*r.Output, previous.Request.Output) {
		return errors.New("proposal output does not match its edit history")
	}
	return nil
}
