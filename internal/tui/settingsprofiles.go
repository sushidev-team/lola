package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

const specialistAddKey = "specialist.add"

// The form serial is independent of the editable profile ID. Fields own draft
// values until the whole settings form validates and saves successfully.
func specialistKey(serial int, field string) string {
	return fmt.Sprintf("specialist.%d.%s", serial, field)
}

func (f *settingsForm) specialistFields(serial int, p reviewprofile.Profile) []setField {
	var kinds, providers []string
	for _, s := range reviewprofile.Specialties() {
		kinds = append(kinds, s.Kind)
	}
	for _, kind := range config.ReviewProviderKinds() {
		if _, ok := config.ReviewAgentFor(kind); ok {
			providers = append(providers, kind)
		}
	}
	projects := []setPickOpt{}
	for _, project := range f.cfg.Projects {
		projects = append(projects, setPickOpt{project.Name, project.DisplayName()})
	}
	var rubric []string
	if p.Rubric != "" {
		rubric = strings.Split(p.Rubric, "\n")
	}
	fields := []setField{
		{key: "id", label: "Profile ID", kind: sfText, text: p.ID, subsection: p.ID, help: "Stable ID; lowercase letters, digits and hyphens."},
		{key: "kind", label: "Specialty", kind: sfEnum, text: p.Kind, options: kinds, help: "Enter cycles the rubric. UI/UX needs screenshot + interaction evidence."},
		{key: "provider", label: "Provider", kind: sfEnum, text: p.Provider, options: providers, help: "Enable this agent provider below before enabling the profile."},
		{key: "enabled", label: "Enabled", kind: sfBool, b: p.Enabled, help: "Run this specialist when its scope and trigger match."},
		{key: "onpropen", label: "On PR open", kind: sfBool, b: p.OnPROpen, help: "Run on PR open; later pushes require a fresh verdict."},
		{key: "required", label: "Required", kind: sfBool, b: p.Required, help: "Hold goal verification if this applicable profile is not clean."},
		{key: "version", label: "Rubric version", kind: sfText, text: p.Version, help: "Name the rubric revision; changes invalidate previous review results."},
		{key: "rubric", label: "Additional rubric", kind: sfList, lines: rubric, help: "Enter edits extra instructions, one line at a time. Built-in rubric always applies."},
		{key: "projects", label: "Projects", kind: sfList, lines: slices.Clone(p.Projects), choices: projects, help: "Empty means all projects. Enter picks projects by display name."},
		{key: "paths", label: "Paths", kind: sfList, lines: slices.Clone(p.Paths), help: "Empty means all paths. Relative file/directory prefixes; no globs."},
		{key: "artifacts", label: "Extra evidence", kind: sfList, lines: slices.Clone(p.Artifacts), help: "Extra artifact kinds; the specialty's required evidence still applies."},
		{key: "timeout", label: "Timeout seconds", kind: sfInt, text: strconv.Itoa(p.TimeoutSeconds), help: "0 uses the default timeout; maximum 900 seconds."},
		{key: "rounds", label: "Max review rounds", kind: sfInt, text: strconv.Itoa(p.MaxRounds), help: "0 uses the default of 3 rounds; maximum 3."},
		{key: "remove", label: "Remove specialist", kind: sfAction, help: "Remove from this draft. Ctrl-s saves; esc can discard all changes."},
	}
	for i := range fields {
		fields[i].key = specialistKey(serial, fields[i].key)
		fields[i].tab = stCodeRabbit
		fields[i].indent = true
	}
	return fields
}

func (f *settingsForm) initSpecialists() {
	f.fields = append(f.fields, setField{key: specialistAddKey, tab: stCodeRabbit, section: "Specialists", sectionNote: "focused reviews using agent providers", label: "Add specialist", kind: sfAction, help: "Enter chooses Backend, UI/UX, Security, Integration or Performance."})
	for _, p := range f.cfg.ReviewProfiles {
		f.appendSpecialist(p)
	}
}

func (f *settingsForm) appendSpecialist(p reviewprofile.Profile) {
	serial := f.nextSpecialist
	f.nextSpecialist++
	f.specialistKeys = append(f.specialistKeys, serial)
	// Insert before the provider catalog, preserving all unsaved provider fields.
	at := len(f.fields)
	for i, field := range f.fields {
		if field.tab == stCodeRabbit && !strings.HasPrefix(field.key, "specialist.") {
			at = i
			break
		}
	}
	f.fields = slices.Insert(f.fields, at, f.specialistFields(serial, p)...)
}

func (f *settingsForm) specialistAction(key string) {
	if key == specialistAddKey {
		p := &setPicker{title: "Add specialist", key: key}
		for _, s := range reviewprofile.Specialties() {
			p.opts = append(p.opts, setPickOpt{s.Kind, s.Label})
		}
		f.picker = p
		return
	}
	for _, serial := range f.specialistKeys {
		if key != specialistKey(serial, "remove") {
			continue
		}
		prefix := specialistKey(serial, "")
		f.fields = slices.DeleteFunc(f.fields, func(field setField) bool { return strings.HasPrefix(field.key, prefix) })
		f.specialistKeys = slices.DeleteFunc(f.specialistKeys, func(k int) bool { return k == serial })
		f.focusSpecialist(specialistAddKey)
		return
	}
}

func (f *settingsForm) addSpecialist(kind string) {
	id := kind
	for suffix := 2; ; suffix++ {
		exists := false
		for _, serial := range f.specialistKeys {
			if f.field(specialistKey(serial, "id")).text == id {
				exists = true
				break
			}
		}
		if !exists {
			break
		}
		id = fmt.Sprintf("%s-%d", kind, suffix)
	}
	provider := "codex-session"
	for _, k := range config.ReviewProviderKinds() {
		if _, ok := config.ReviewAgentFor(k); ok && f.field(pvKey(k, "enabled")).b {
			provider = k
			break
		}
	}
	serial := f.nextSpecialist
	f.appendSpecialist(reviewprofile.Profile{ID: id, Kind: kind, Provider: provider, Version: "1", OnPROpen: true})
	f.focusSpecialist(specialistKey(serial, "id"))
}

func (f *settingsForm) focusSpecialist(key string) {
	f.tab = stCodeRabbit
	for vi, i := range f.visible() {
		if f.fields[i].key == key {
			f.cursor = vi
			f.scroll = 0
			return
		}
	}
}

func (f *settingsForm) buildSpecialists() ([]reviewprofile.Profile, error) {
	out := make([]reviewprofile.Profile, 0, len(f.specialistKeys))
	for _, serial := range f.specialistKeys {
		field := func(k string) *setField { return f.field(specialistKey(serial, k)) }
		timeout, err := strconv.Atoi(strings.TrimSpace(field("timeout").text))
		if err != nil {
			f.focusSpecialist(field("timeout").key)
			return nil, fmt.Errorf("specialist timeout: enter a whole number from 0 to 900")
		}
		rounds, err := strconv.Atoi(strings.TrimSpace(field("rounds").text))
		if err != nil {
			f.focusSpecialist(field("rounds").key)
			return nil, fmt.Errorf("specialist rounds: enter a whole number from 0 to 3")
		}
		p := reviewprofile.Profile{ID: strings.TrimSpace(field("id").text), Kind: field("kind").text, Provider: field("provider").text, Version: strings.TrimSpace(field("version").text), Rubric: strings.Join(field("rubric").lines, "\n"), Enabled: field("enabled").b, Required: field("required").b, OnPROpen: field("onpropen").b, Projects: trimDropEmpty(field("projects").lines), Paths: trimDropEmpty(field("paths").lines), Artifacts: trimDropEmpty(field("artifacts").lines), TimeoutSeconds: timeout, MaxRounds: rounds}
		if err := p.Validate(); err != nil {
			f.focusSpecialist(field("id").key)
			return nil, fmt.Errorf("specialist %q: %w", p.ID, err)
		}
		out = append(out, p)
	}
	return out, nil
}
