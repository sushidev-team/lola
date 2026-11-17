package tui

// hubsettings.go is the settings form's "Team hub" tab: the [hub] keys a
// member decides for this machine (enabled, which chore kinds, how many a
// day, whether chores need the sandbox) and each project's hub_chores opt-in.
// Joining, leaving and pinning a policy key are NOT here: they go through
// `lola hub join | leave`, which handle the token and the key's fingerprint.
//
// Two keys keep their pointer semantics on save (config.HubConfig): an unset
// max_claims_per_day / require_sandbox stays unset while the form shows its
// default, so a default is never frozen into config.toml.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

const (
	hubEnabledKey  = "hub_enabled"
	hubKindsKey    = "hub_accept_kinds"
	hubClaimsKey   = "hub_max_claims"
	hubSandboxKey  = "hub_require_sandbox"
	hubChoresKeyAt = "hub_chores."
)

func hubChoresKey(project string) string { return hubChoresKeyAt + project }

// hubKindOpts is the fixed multiselect over hubproto's chore kinds.
func hubKindOpts() []setPickOpt {
	var out []setPickOpt
	for _, k := range hubproto.AllKinds() {
		out = append(out, setPickOpt{id: string(k), label: string(k)})
	}
	return out
}

// hubFields builds the tab from the file's config.
func hubFields(cfg *config.Config) []setField {
	h := cfg.Hub
	joined := "not joined (run `lola hub join`)"
	if h.URL != "" {
		joined = h.URL
	}
	fields := []setField{
		{key: hubEnabledKey, section: "[hub]", sectionNote: "team chores from " + joined, label: "Enabled",
			help: "Take part in the team hub. Needs a joined hub url; `lola hub join` sets both.", kind: sfBool, b: h.Enabled},
		{key: hubKindsKey, label: "Chore kinds",
			help: "The chores this machine takes. None picked = every kind lola knows. enter picks.", kind: sfList, choices: hubKindOpts(), lines: append([]string(nil), h.AcceptKinds...)},
		{key: hubClaimsKey, label: "Max chores per day",
			help: fmt.Sprintf("How many chores this machine claims per local day on its own (0 = none; at most %d). Blank means %d.", config.MaxHubClaimsPerDay, config.DefaultHubMaxClaimsPerDay),
			kind: sfInt, text: strconv.Itoa(h.ClaimsPerDay())},
		{key: hubSandboxKey, label: "Chores need the sandbox",
			help: "On (the default): a chore runs only in a project whose agent runs in the docker sandbox. Turning it off lets hub work run unsandboxed on this machine.", kind: sfBool, b: h.SandboxRequired()},
	}
	first := true
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if p.IsStack() {
			continue
		}
		fd := setField{key: hubChoresKey(p.Name), label: p.DisplayName(), kind: sfBool, b: p.HubChores, indent: true,
			help: "Opt this project in to team chores. It needs a GitHub repo (owner/name) set on the project."}
		if first {
			fd.subsection = "Projects that take chores"
			first = false
		}
		fields = append(fields, fd)
	}
	for i := range fields {
		fields[i].tab = stHub
	}
	return fields
}

// hubSaved is what applyHub replaced, for the save's rollback.
type hubSaved struct {
	hub    config.HubConfig
	chores map[string]bool
}

// applyHub writes the tab onto c and returns what it replaced. It reads the
// form's fields only; it never touches [hub].url, the device name or the
// trusted policy keys.
func (f *settingsForm) applyHub(c *config.Config) (hubSaved, error) {
	saved := hubSaved{hub: c.Hub, chores: map[string]bool{}}
	h := c.Hub
	h.Enabled = f.field(hubEnabledKey).b
	h.AcceptKinds = trimDropEmpty(f.field(hubKindsKey).lines)
	if len(h.AcceptKinds) == 0 {
		h.AcceptKinds = nil
	}
	raw := strings.TrimSpace(f.field(hubClaimsKey).text)
	n := config.DefaultHubMaxClaimsPerDay
	if raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return saved, fmt.Errorf("max chores per day must be a whole number")
		}
		n = v
	}
	if h.MaxClaimsPerDay != nil || n != config.DefaultHubMaxClaimsPerDay {
		h.MaxClaimsPerDay = &n
	}
	sb := f.field(hubSandboxKey).b
	if h.RequireSandbox != nil || !sb {
		h.RequireSandbox = &sb
	}
	c.Hub = h
	for i := range c.Projects {
		p := &c.Projects[i]
		fd := f.field(hubChoresKey(p.Name))
		if fd == nil {
			continue
		}
		saved.chores[p.Name] = p.HubChores
		p.HubChores = fd.b
	}
	return saved, nil
}

// restoreHub undoes applyHub after a refused save.
func restoreHub(c *config.Config, s hubSaved) {
	c.Hub = s.hub
	for i := range c.Projects {
		if v, ok := s.chores[c.Projects[i].Name]; ok {
			c.Projects[i].HubChores = v
		}
	}
}
