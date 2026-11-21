package reviewpack

import (
	"errors"
	"fmt"
	"strings"
)

// MaxMarks bounds the highlights one prepared page carries.
const MaxMarks = 4

// Prep says how a reviewer's view of one page is prepared: which viewport,
// what to wait for, where to scroll and what to point at. It is DECLARATIVE data
// an agent writes and lola's own script (internal/preview/prepare.js) carries
// out, both in the embedded preview and when a screenshot is taken, so the
// picture shows exactly what the reviewer will find. It never carries code.
type Prep struct {
	// Device is "mobile", "tablet" or "desktop"; empty keeps the reviewer's own
	// viewport. (Not "viewport": Screen already uses that for its size text.)
	Device string `json:"device,omitempty"`
	// WaitFor is a CSS selector that must exist before the view is prepared.
	WaitFor string `json:"waitFor,omitempty"`
	// ScrollY is an offset in CSS pixels, used only when no mark is found.
	ScrollY int `json:"scrollY,omitempty"`
	// Marks are outlined and labelled; the first one found is scrolled to the
	// middle of the view.
	Marks []Mark `json:"marks,omitempty"`
}

// Mark points at one element by CSS selector or by a piece of its visible text.
type Mark struct {
	Selector string `json:"selector,omitempty"`
	Text     string `json:"text,omitempty"`
	Label    string `json:"label,omitempty"`
}

// Empty reports whether nothing is prepared.
func (p Prep) Empty() bool {
	return p.Device == "" && p.WaitFor == "" && p.ScrollY == 0 && len(p.Marks) == 0
}

// ValidatePrep bounds every field; the script treats all of it as untrusted.
func ValidatePrep(p Prep) error {
	switch p.Device {
	case "", "mobile", "tablet", "desktop":
	default:
		return errors.New("device must be mobile, tablet or desktop")
	}
	if len(p.WaitFor) > 500 || p.ScrollY < 0 || p.ScrollY > 100_000 {
		return errors.New("waitFor must be at most 500 bytes and scrollY between 0 and 100000")
	}
	if len(p.Marks) > MaxMarks {
		return fmt.Errorf("at most %d marks per page", MaxMarks)
	}
	for _, m := range p.Marks {
		if strings.TrimSpace(m.Selector) == "" && strings.TrimSpace(m.Text) == "" {
			return errors.New("each mark needs a selector or text")
		}
		if len(m.Selector) > 500 || len(m.Text) > 200 || len(m.Label) > 80 {
			return errors.New("mark selector, text and label are limited to 500, 200 and 80 bytes")
		}
	}
	return nil
}
