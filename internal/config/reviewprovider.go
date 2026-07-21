package config

import "slices"

// The NEW canonical review schema: a GLOBAL provider CATALOG expressed as
// nested array-of-tables under [review] ([[review.provider]]). It generalizes
// the two legacy tables ([review] CLI pass + [coderabbit] PR-comment watch)
// into a set of pluggable, independently-configured providers.
//
// A provider has a KIND (coderabbit-cli | coderabbit-watch | claude-session),
// a set of TRANSPORTS (the sinks its findings route to), and — for the sync
// "pass" kinds — an ordered FALLBACK chain of other kinds tried when it cannot
// answer (unavailable / over-quota). At most ONE provider per kind is allowed
// (guards key by kind), enforced by validateReviewProviders.
//
// The catalog and the legacy [review]/[coderabbit] tables are MUTUALLY
// EXCLUSIVE: a file that carries both is a hard validation error resolved by
// the one-way `lola config migrate-review` command (see MigrateLegacyReview).
// A legacy-only file keeps working forever and is synthesized into effective
// providers at read time by EffectiveReviewProviders.
//
// This package holds only the schema, defaults, static validation, and the
// legacy synthesis/migration; the daemon owns provider execution, guards, and
// transport dispatch.

// provKind names a review provider kind. The three kinds map to two execution
// shapes: coderabbit-cli / claude-session are sync "pass" shapes (exec, return
// findings); coderabbit-watch is a "watch" shape (poll the PR for bot comments).
type provKind string

const (
	provCoderabbitCLI   provKind = "coderabbit-cli"
	provCoderabbitWatch provKind = "coderabbit-watch"
	provClaudeSession   provKind = "claude-session"
)

// valid reports whether k is a known provider kind.
func (k provKind) valid() bool {
	switch k {
	case provCoderabbitCLI, provCoderabbitWatch, provClaudeSession:
		return true
	}
	return false
}

// isWatch reports whether k is the poll/watermark "watch" shape (the only kind
// that cannot classify quota, so it takes no fallback and no github transport).
func (k provKind) isWatch() bool { return k == provCoderabbitWatch }

// Transport is a friendly token in a provider's `transports` multiselect. The
// three tokens expand to the resolved canonical sinks: `lola` -> notify + agent
// (the always-present internal transport, refined by the notify/send_to_agent
// bools), `github` -> a PR comment, `linear` -> a Linear comment.
type Transport string

const (
	// TransportLola is the always-present internal transport (notify + worker
	// hand-off). resolveReviewProvider force-appends it when omitted.
	TransportLola Transport = "lola"
	// TransportGitHub posts findings as a GitHub PR comment (pass shapes only;
	// forbidden on coderabbit-watch by validation).
	TransportGitHub Transport = "github"
	// TransportLinear mirrors findings onto the session's Linear issue.
	TransportLinear Transport = "linear"
)

// valid reports whether t is a known transport token.
func (t Transport) valid() bool {
	switch t {
	case TransportLola, TransportGitHub, TransportLinear:
		return true
	}
	return false
}

// TransportSet is a provider's resolved transport multiselect. It always
// contains TransportLola after resolution.
type TransportSet []Transport

// Has reports whether the set contains x.
func (ts TransportSet) Has(x Transport) bool { return slices.Contains(ts, x) }

// Per-kind hand-off / notification labels for the claude-session provider,
// alongside the coderabbit-kind values kept in review.go / coderabbit.go
// (ReviewNotifyTitle, ReviewToAgentPreamble, CodeRabbitNotifyTitle, …). The
// daemon's route code selects the label set by provider kind so a
// claude-session's findings are never mislabeled "CodeRabbit". Plain strings
// (no template eval) so nothing in the findings can inject a directive.
const (
	// ClaudeReviewNotifyTitle titles the human-facing claude-session notification/comment.
	ClaudeReviewNotifyTitle = "Claude review"
	// ClaudeReviewToAgentPreamble prefixes the findings sent to the worker agent.
	ClaudeReviewToAgentPreamble = "A Claude review of your PR found the following. Address the actionable items, commit, and push. Ignore anything already handled or out of scope:\n"
)

// ReviewProvider is one resolved entry of the global catalog. It carries the
// RESOLVED value of every key (defaults already applied); the on-disk mirror
// (fileReviewProvider) is what distinguishes an absent key from an explicit
// zero. Never serialized directly — Save writes it through reviewProvidersFile
// into the [review].provider array.
//
//   - Provider is the kind; Enabled gates the entry.
//   - OnPROpen (pass shapes) runs the pass when a session first opens a PR.
//   - Command overrides the coderabbit-cli argv (space-split); coderabbit-cli only.
//   - TimeoutSeconds bounds each pass (pass shapes); defaults to 300.
//   - Model optionally sets claude-session's --model; claude-session only.
//   - Author is the login substring matched by the watch; coderabbit-watch only.
//   - Transports is the resolved sink multiselect (always contains lola).
//   - Notify / SendToAgent refine the lola transport: they mute the notify sink
//     and the worker hand-off independently (this preserves the legacy
//     [coderabbit].notify=false opt-out).
//   - Fallback is the ordered chain of kinds tried when this provider cannot
//     answer; pass shapes only, empty for a watch.
type ReviewProvider struct {
	Provider       provKind
	Enabled        bool
	OnPROpen       bool
	Command        string
	TimeoutSeconds int
	Model          string
	Author         string
	Transports     TransportSet
	Notify         bool
	SendToAgent    bool
	Fallback       []provKind
}

// --- on-disk mirror --------------------------------------------------------
//
// fileReviewProvider is the pointer-per-field mirror of one [[review.provider]]
// entry, so load can tell an ABSENT key (nil -> take the default) from an
// explicit zero. Transports and Fallback are POINTERS-TO-SLICE so an absent key
// (nil -> take the default / "no fallback") stays distinct from an explicit
// empty array. Because the struct holds slices it is NON-comparable, so
// reviewProvidersFile keys emptiness off len(), never ==.

type fileReviewProvider struct {
	Provider       *provKind     `toml:"provider,omitempty"`
	Enabled        *bool         `toml:"enabled,omitempty"`
	OnPROpen       *bool         `toml:"on_pr_open,omitempty"`
	Command        *string       `toml:"command,omitempty"`
	TimeoutSeconds *int          `toml:"timeout_seconds,omitempty"`
	Model          *string       `toml:"model,omitempty"`
	Author         *string       `toml:"author,omitempty"`
	Transports     *TransportSet `toml:"transports,omitempty"`
	Notify         *bool         `toml:"notify,omitempty"`
	SendToAgent    *bool         `toml:"send_to_agent,omitempty"`
	Fallback       *[]provKind   `toml:"fallback,omitempty"`
}

// resolveReviewProviders materializes the catalog. An empty (absent) slice
// yields nil so a config with no [[review.provider]] entries has no catalog and
// falls back to legacy synthesis at read time. Each entry overlays its
// explicitly-set fields onto the defaults (see resolveReviewProvider).
func resolveReviewProviders(fps []fileReviewProvider) []ReviewProvider {
	if len(fps) == 0 {
		return nil
	}
	out := make([]ReviewProvider, 0, len(fps))
	for i := range fps {
		out = append(out, resolveReviewProvider(fps[i]))
	}
	return out
}

// resolveReviewProvider applies the per-provider defaults (§1.3): transports
// absent -> [lola] and lola always force-appended; notify / send_to_agent /
// on_pr_open absent -> true; timeout_seconds absent -> DefaultReviewTimeoutSeconds;
// author absent/empty -> DefaultCodeRabbitAuthor; fallback absent/empty -> none.
func resolveReviewProvider(fp fileReviewProvider) ReviewProvider {
	p := ReviewProvider{
		OnPROpen:       true,
		Notify:         true,
		SendToAgent:    true,
		TimeoutSeconds: DefaultReviewTimeoutSeconds,
		Author:         DefaultCodeRabbitAuthor,
	}
	if fp.Provider != nil {
		p.Provider = *fp.Provider
	}
	if fp.Enabled != nil {
		p.Enabled = *fp.Enabled
	}
	if fp.OnPROpen != nil {
		p.OnPROpen = *fp.OnPROpen
	}
	if fp.Command != nil {
		p.Command = *fp.Command
	}
	if fp.TimeoutSeconds != nil {
		p.TimeoutSeconds = *fp.TimeoutSeconds
	}
	if fp.Model != nil {
		p.Model = *fp.Model
	}
	if fp.Author != nil && *fp.Author != "" {
		p.Author = *fp.Author
	}
	if fp.Notify != nil {
		p.Notify = *fp.Notify
	}
	if fp.SendToAgent != nil {
		p.SendToAgent = *fp.SendToAgent
	}
	if fp.Transports != nil {
		p.Transports = slices.Clone(*fp.Transports)
	}
	p.Transports = forceLola(p.Transports)
	if fp.Fallback != nil && len(*fp.Fallback) > 0 {
		p.Fallback = slices.Clone(*fp.Fallback)
	}
	return p
}

// forceLola returns ts with TransportLola guaranteed present (appended if
// missing), never nil. lola is the always-on internal transport.
func forceLola(ts TransportSet) TransportSet {
	if !slices.Contains(ts, TransportLola) {
		ts = append(ts, TransportLola)
	}
	return ts
}

// reviewProvidersFile builds the on-disk mirror for Save. An empty catalog
// returns nil so no [[review.provider]] tables are emitted; otherwise every
// scalar field is written explicitly so the round-trip is exact, while
// Transports/Fallback are written only when non-empty (len-based emptiness —
// the struct is non-comparable). resolveReviewProvider re-applies the same
// defaults on load, so Save/Load is an identity.
func reviewProvidersFile(ps []ReviewProvider) []fileReviewProvider {
	if len(ps) == 0 {
		return nil
	}
	out := make([]fileReviewProvider, 0, len(ps))
	for i := range ps {
		p := ps[i]
		fp := fileReviewProvider{
			Provider:       ptrProvKind(p.Provider),
			Enabled:        &p.Enabled,
			OnPROpen:       &p.OnPROpen,
			Command:        &p.Command,
			TimeoutSeconds: &p.TimeoutSeconds,
			Model:          &p.Model,
			Author:         &p.Author,
			Notify:         &p.Notify,
			SendToAgent:    &p.SendToAgent,
		}
		if len(p.Transports) > 0 {
			ts := slices.Clone(p.Transports)
			fp.Transports = &ts
		}
		if len(p.Fallback) > 0 {
			fb := slices.Clone(p.Fallback)
			fp.Fallback = &fb
		}
		out = append(out, fp)
	}
	return out
}

func ptrProvKind(k provKind) *provKind { return &k }

// --- UI helpers ------------------------------------------------------------
//
// provKind is unexported, so packages outside config (the TUI settings form,
// the desktop config service) cannot name it to build catalog entries. These
// string-typed helpers let a UI enumerate the kinds/transports, read a
// provider's kind/fallback as plain strings, and construct/mutate a provider
// from the string values its widgets carry — without ever touching provKind.

// ReviewProviderKinds is the selectable provider-kind catalog, as strings.
func ReviewProviderKinds() []string {
	return []string{string(provCoderabbitCLI), string(provCoderabbitWatch), string(provClaudeSession)}
}

// TransportTokens is the selectable transport multiselect, as strings.
func TransportTokens() []string {
	return []string{string(TransportLola), string(TransportGitHub), string(TransportLinear)}
}

// ValidReviewProviderKind reports whether s names a known provider kind.
func ValidReviewProviderKind(s string) bool { return provKind(s).valid() }

// IsWatchKind reports whether s is the coderabbit-watch kind (no fallback / no
// github transport — the UI hides those affordances for it).
func IsWatchKind(s string) bool { return provKind(s).isWatch() }

// KindString returns the provider's kind as a plain string.
func (p ReviewProvider) KindString() string { return string(p.Provider) }

// FallbackStrings returns the provider's fallback chain as plain strings.
func (p ReviewProvider) FallbackStrings() []string {
	out := make([]string, len(p.Fallback))
	for i, k := range p.Fallback {
		out[i] = string(k)
	}
	return out
}

// Strings returns the transport set as plain strings.
func (ts TransportSet) Strings() []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

// NewReviewProvider builds a provider of the named kind with every default
// applied (as resolveReviewProvider would), so a UI creating an entry starts
// from the same baseline a fresh [[review.provider]] resolves to. ok is false
// for an unknown kind.
func NewReviewProvider(kind string) (ReviewProvider, bool) {
	if !provKind(kind).valid() {
		return ReviewProvider{}, false
	}
	p := resolveReviewProvider(fileReviewProvider{})
	p.Provider = provKind(kind)
	return p, true
}

// SetKind sets the provider's kind from a string.
func (p *ReviewProvider) SetKind(kind string) { p.Provider = provKind(kind) }

// SetFallbackKinds replaces the fallback chain from strings, dropping empties.
func (p *ReviewProvider) SetFallbackKinds(kinds []string) {
	p.Fallback = nil
	for _, k := range kinds {
		if k == "" {
			continue
		}
		p.Fallback = append(p.Fallback, provKind(k))
	}
}

// SetTransportTokens replaces the transport set from strings; lola is always
// force-present (as resolution guarantees), so a UI can never drop it.
func (p *ReviewProvider) SetTransportTokens(tokens []string) {
	ts := make(TransportSet, 0, len(tokens))
	for _, t := range tokens {
		if t == "" {
			continue
		}
		ts = append(ts, Transport(t))
	}
	p.Transports = forceLola(ts)
}

// ReviewKindStrings returns the project's per-project review-provider selection
// as plain strings, so a UI (which cannot name the unexported provKind) can
// render and diff it.
func (p *Project) ReviewKindStrings() []string {
	out := make([]string, len(p.Review))
	for i, k := range p.Review {
		out[i] = string(k)
	}
	return out
}

// SetReviewKinds replaces the project's per-project review selection from
// strings, dropping empties. It sets the RESOLVED field only — the caller (the
// form's override step) is responsible for clearing Inherits.Review, or Save
// silently discards the write. A non-nil (possibly empty) slice is always
// assigned so an explicit "override to nothing" is distinct from inherit.
func (p *Project) SetReviewKinds(kinds []string) {
	out := make([]provKind, 0, len(kinds))
	for _, k := range kinds {
		if k == "" {
			continue
		}
		out = append(out, provKind(k))
	}
	p.Review = out
}

// ReviewCatalogKinds returns the enabled provider kinds in the effective
// catalog (catalog when present, else legacy synthesis), as plain strings —
// the selectable set a project's per-project review override may pick from.
func (c *Config) ReviewCatalogKinds() []string {
	var out []string
	for _, p := range c.EffectiveReviewProviders() {
		if p.Enabled {
			out = append(out, string(p.Provider))
		}
	}
	return out
}

// EffectiveReviewProviders derives the runtime provider set at read time (like
// AgentForProject / EffectiveCap resolve at read time). If the catalog is
// non-empty it wins; otherwise the legacy [review]/[coderabbit] tables are
// synthesized into equivalent providers so a legacy-only config behaves exactly
// as before. Never serialized.
func (c *Config) EffectiveReviewProviders() []ReviewProvider {
	if len(c.ReviewProviders) > 0 {
		return slices.Clone(c.ReviewProviders)
	}
	return synthesizeLegacyProviders(c.Review, c.CodeRabbit)
}

// synthesizeLegacyProviders builds the effective providers implied by the two
// legacy tables: a coderabbit-cli from a present [review] and a coderabbit-watch
// from a present [coderabbit]. It preserves the legacy resolve ergonomics —
// on_pr_open / send_to_agent already follow Enabled in the resolved tables,
// comment_on_linear maps to the linear transport, and the watch's notify bool
// maps verbatim (preserving the [coderabbit].notify=false opt-out). The cli's
// notify is always ON, matching the legacy review pass which always notified.
// The fixed kinds match the guard keys, so an upgrade re-reviews nothing.
func synthesizeLegacyProviders(rc ReviewConfig, cc CodeRabbitConfig) []ReviewProvider {
	var out []ReviewProvider
	if rc != (ReviewConfig{}) {
		tr := TransportSet{TransportLola}
		if rc.CommentOnLinear {
			tr = append(tr, TransportLinear)
		}
		out = append(out, ReviewProvider{
			Provider:       provCoderabbitCLI,
			Enabled:        rc.Enabled,
			OnPROpen:       rc.OnPROpen,
			Command:        rc.Command,
			TimeoutSeconds: rc.TimeoutSeconds,
			Author:         DefaultCodeRabbitAuthor,
			Transports:     tr,
			Notify:         true,
			SendToAgent:    rc.SendToAgent,
		})
	}
	if cc != (CodeRabbitConfig{}) {
		tr := TransportSet{TransportLola}
		if cc.CommentOnLinear {
			tr = append(tr, TransportLinear)
		}
		out = append(out, ReviewProvider{
			Provider:    provCoderabbitWatch,
			Enabled:     cc.Enabled,
			Author:      cc.Author,
			Transports:  tr,
			Notify:      cc.Notify,
			SendToAgent: cc.SendToAgent,
		})
	}
	return out
}

// MigrateLegacyReview converts the legacy [review]/[coderabbit] tables into the
// canonical catalog IN PLACE and CLEARS the legacy tables. One-way and opt-in
// (the hidden `lola config migrate-review` command): it makes the mutually
// exclusive legacy+catalog pair valid by moving to the catalog side. The
// synthesized kinds match the guard keys, so no session is re-reviewed after
// migration. A no-op when there is nothing to migrate.
func MigrateLegacyReview(c *Config) {
	provs := synthesizeLegacyProviders(c.Review, c.CodeRabbit)
	if len(provs) == 0 {
		return
	}
	c.ReviewProviders = provs
	c.Review = ReviewConfig{}
	c.CodeRabbit = CodeRabbitConfig{}
}
