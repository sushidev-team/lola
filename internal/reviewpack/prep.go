package reviewpack

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
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
	// Steps are carried out BEFORE the marks: clicks, typing, a login. Only on a
	// local host (see LocalHost); each one is bounded, none carries code.
	Steps []Step `json:"steps,omitempty"`
}

// Mark points at one element by CSS selector or by a piece of its visible text.
type Mark struct {
	Selector string `json:"selector,omitempty"`
	Text     string `json:"text,omitempty"`
	Label    string `json:"label,omitempty"`
}

// Empty reports whether nothing is prepared.
func (p Prep) Empty() bool {
	return p.Device == "" && p.WaitFor == "" && p.ScrollY == 0 && len(p.Marks) == 0 && len(p.Steps) == 0
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

// MaxSteps bounds the steps one prepared page runs.
const MaxSteps = 12

// Step kinds. Every one is plain data interpreted by lola's own script
// (internal/preview/prepare.js); none is evaluated as code.
const (
	StepClick = "click"
	StepFill  = "fill"
	StepPress = "press"
	StepWait  = "wait"
	StepGoto  = "goto"
)

// Step is one action on the page. Click, fill and wait name their element by
// CSS Selector, or by visible Text (for fill: a label, placeholder or aria-label).
type Step struct {
	Do       string `json:"do"`
	Selector string `json:"selector,omitempty"`
	Text     string `json:"text,omitempty"`
	// Value is typed by fill. The manifest is retained and shown to reviewers,
	// so it holds a throwaway development login, never a real secret.
	Value string `json:"value,omitempty"`
	// Key is pressed by press: one of Enter, Escape, Tab, Space, ArrowUp, ArrowDown.
	Key string `json:"key,omitempty"`
	// Path is where goto navigates: a path on the SAME origin.
	Path string `json:"path,omitempty"`
	// Ms is how long wait pauses when it names no element (1-3000).
	Ms int `json:"ms,omitempty"`
}

var stepKeys = map[string]bool{"Enter": true, "Escape": true, "Tab": true, "Space": true, "ArrowUp": true, "ArrowDown": true}

// LocalHost reports whether steps may run against host: a development machine
// the reviewer already trusts with a throwaway login. localhost and *.test /
// *.localhost names, and loopback or private-network addresses (10/8,
// 172.16/12, 192.168/16, link-local, IPv6 ULA). Never a public name or address;
// the same rule is enforced again by the script that carries the steps out.
func LocalHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".test") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// ValidateSteps bounds the steps and requires pageURL to be a local address.
func ValidateSteps(steps []Step, pageURL string) error {
	if len(steps) == 0 {
		return nil
	}
	if len(steps) > MaxSteps {
		return fmt.Errorf("at most %d steps per page", MaxSteps)
	}
	u, err := url.Parse(pageURL)
	if err != nil || u.Hostname() == "" || !LocalHost(u.Hostname()) {
		return errors.New("steps run only on a local address: localhost, *.test, or a loopback or private-network IP")
	}
	for _, s := range steps {
		named := strings.TrimSpace(s.Selector) != "" || strings.TrimSpace(s.Text) != ""
		if len(s.Selector) > 500 || len(s.Text) > 200 || len(s.Value) > 200 || len(s.Path) > 500 {
			return errors.New("step selector, text, value and path are limited to 500, 200, 200 and 500 bytes")
		}
		switch s.Do {
		case StepClick, StepFill:
			if !named {
				return fmt.Errorf("a %s step needs a selector or text", s.Do)
			}
		case StepPress:
			if !stepKeys[s.Key] {
				return errors.New("press needs a key: Enter, Escape, Tab, Space, ArrowUp or ArrowDown")
			}
		case StepWait:
			if !named && (s.Ms < 1 || s.Ms > 3000) {
				return errors.New("wait needs a selector or text, or ms between 1 and 3000")
			}
		case StepGoto:
			if !strings.HasPrefix(s.Path, "/") || strings.HasPrefix(s.Path, "//") || strings.Contains(s.Path, `\`) || strings.ContainsAny(s.Path, "\r\n") {
				return errors.New("goto needs a path on the same origin, starting with a single /")
			}
		default:
			return fmt.Errorf("unknown step %q (click, fill, press, wait, goto)", s.Do)
		}
	}
	return nil
}
