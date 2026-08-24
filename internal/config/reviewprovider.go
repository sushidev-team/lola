package config

import (
	"slices"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
)

// The NEW canonical review schema: a GLOBAL provider CATALOG expressed as
// nested array-of-tables under [review] ([[review.provider]]). It generalizes
// the two legacy tables ([review] CLI pass + [coderabbit] PR-comment watch)
// into a set of pluggable, independently-configured providers.
//
// NOTHING here is hardwired to one vendor. A provider has a KIND, a set of
// TRANSPORTS (the sinks its findings route to), and — for the sync "pass" kinds
// — an ordered FALLBACK chain of other kinds tried when it cannot answer
// (unavailable / over-quota). The kinds group into three FAMILIES, each of which
// is a swappable slot rather than a fixed tool:
//
//	family   kinds                                                shape
//	agent    claude-session, codex-session, opencode-session      pass
//	cli      coderabbit-cli, custom-cli                           pass
//	watch    coderabbit-watch, bot-watch                          watch
//
// So the review AGENT is a config choice (run codex instead of claude, or run
// claude with codex as its over-quota fallback), the review CLI is a config
// choice (custom-cli takes any `command`), and which bot's GitHub review is
// relayed is a config choice (bot-watch takes any `author`). The coderabbit-*
// and claude-* kinds are simply the ones that ship with a working default.
//
// At most ONE provider per kind is allowed (guards key by kind), enforced by
// validateReviewProviders — which is also why every agent gets its own kind
// rather than sharing one with an `agent =` field: two agents can then run as
// primary and fallback for the same session.
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

// provKind names a review provider kind. Each kind maps to one of two execution
// shapes: the agent and cli families are sync "pass" shapes (exec, return
// findings); the watch family is a "watch" shape (poll the PR for bot comments).
type provKind string

const (
	// The cli family: exec an external review CLI in the worktree.
	provCoderabbitCLI provKind = "coderabbit-cli"
	provCustomCLI     provKind = "custom-cli"
	// The watch family: poll the PR for a review bot's own comments.
	provCoderabbitWatch provKind = "coderabbit-watch"
	provBotWatch        provKind = "bot-watch"
	// The agent family: one bounded, read-only headless coding-agent review.
	provClaudeSession   provKind = "claude-session"
	provCodexSession    provKind = "codex-session"
	provOpenCodeSession provKind = "opencode-session"
)

// provKinds is every known kind, in the order UIs and validation messages
// enumerate them: the two families that need a tool first, then the agents.
var provKinds = []provKind{
	provCoderabbitCLI, provCustomCLI,
	provCoderabbitWatch, provBotWatch,
	provClaudeSession, provCodexSession, provOpenCodeSession,
}

// provKindList renders the kinds for an error message ("a|b|c").
func provKindList() string {
	out := make([]string, len(provKinds))
	for i, k := range provKinds {
		out[i] = string(k)
	}
	return strings.Join(out, "|")
}

// valid reports whether k is a known provider kind.
func (k provKind) valid() bool { return slices.Contains(provKinds, k) }

// isWatch reports whether k is in the poll/watermark "watch" family (the only
// shape that cannot classify quota, so it takes no fallback and no github
// transport).
func (k provKind) isWatch() bool {
	return k == provCoderabbitWatch || k == provBotWatch
}

// isCLI reports whether k execs an external review CLI, so `command` (and
// `base_flag`) apply to it.
func (k provKind) isCLI() bool {
	return k == provCoderabbitCLI || k == provCustomCLI
}

// reviewAgents maps each agent-family kind to the coding agent it runs. A kind
// absent from this map is not an agent kind.
var reviewAgents = map[provKind]agent.Kind{
	provClaudeSession:   agent.Claude,
	provCodexSession:    agent.Codex,
	provOpenCodeSession: agent.OpenCode,
}

// reviewAgent returns the coding agent k reviews with, and whether k is an
// agent-family kind at all.
func (k provKind) reviewAgent() (agent.Kind, bool) {
	a, ok := reviewAgents[k]
	return a, ok
}

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

// Per-kind hand-off / notification labels, alongside the coderabbit-kind values
// kept in review.go / coderabbit.go (ReviewNotifyTitle, ReviewToAgentPreamble,
// CodeRabbitNotifyTitle, …). The daemon's route code selects the label set by
// provider kind so a codex review is never mislabeled "CodeRabbit" — the whole
// point of a pluggable catalog is that the human reading a finding can tell WHO
// produced it. Plain strings (no template eval) so nothing in the findings can
// inject a directive.
const (
	// ClaudeReviewNotifyTitle titles the human-facing claude-session notification/comment.
	ClaudeReviewNotifyTitle = "Claude review"
	// ClaudeReviewToAgentPreamble prefixes the findings sent to the worker agent.
	ClaudeReviewToAgentPreamble = "A Claude review of your PR found the following. Address the actionable items, commit, and push. Ignore anything already handled or out of scope:\n"
	// CodexReviewNotifyTitle / OpenCodeReviewNotifyTitle are the same for the
	// other two review agents.
	CodexReviewNotifyTitle    = "Codex review"
	OpenCodeReviewNotifyTitle = "opencode review"
	// CustomCLIReviewNotifyTitle titles a custom-cli pass, whose tool lola cannot
	// name (it is whatever `command` runs), so the label stays generic.
	CustomCLIReviewNotifyTitle = "Code review"
)

// ReviewAgentPreamble is the worker hand-off preamble for a review by agent a.
// It names the reviewer so the worker knows whose findings it is being handed,
// and is lola's OWN text, prepended to untrusted findings — never templated
// from them.
func ReviewAgentPreamble(who string) string {
	return "A " + who + " review of your PR found the following. Address the actionable items, commit, and push. Ignore anything already handled or out of scope:\n"
}

// ReviewProvider is one resolved entry of the global catalog. It carries the
// RESOLVED value of every key (defaults already applied); the on-disk mirror
// (fileReviewProvider) is what distinguishes an absent key from an explicit
// zero. Never serialized directly — Save writes it through reviewProvidersFile
// into the [review].provider array.
//
//   - Provider is the kind; Enabled gates the entry.
//   - OnPROpen (pass shapes) runs the pass when a session first opens a PR.
//   - Command is the review CLI argv (space-split); cli family only. It is
//     OPTIONAL for coderabbit-cli (which has a working default) and REQUIRED for
//     custom-cli (which has no tool of its own).
//   - BaseFlag names the flag the base branch is passed with; cli family only.
//     Defaults to "--base"; an explicit empty value appends nothing, for a tool
//     that takes no base argument.
//   - TimeoutSeconds bounds each pass (pass shapes); defaults to 300, or 900 for
//     an agent kind (that pass reads the PR's files before it reports).
//   - Model optionally sets the agent's --model; agent family only. opencode
//     expects "provider/model".
//   - Author is the login substring matched by the watch; watch family only. It
//     defaults to CodeRabbit's login for coderabbit-watch and is REQUIRED for
//     bot-watch (which exists precisely to name a different bot).
//   - Transports is the resolved sink multiselect (always contains lola).
//   - GitHubInline refines the github transport: the findings are posted as a
//     pull-request REVIEW with one anchored, resolvable thread per finding
//     instead of a single issue comment. Default true; it degrades to the plain
//     comment by itself whenever the anchors or the API say no, so turning it off
//     is only needed to force the flat comment.
//   - Notify / SendToAgent refine the lola transport: they mute the notify sink
//     and the worker hand-off independently (this preserves the legacy
//     [coderabbit].notify=false opt-out).
//   - Visible runs the pass in its own tmux session ("<session>-review") so a
//     human can watch it and read its output afterwards; pass shapes only.
//   - Fallback is the ordered chain of kinds tried when this provider cannot
//     answer; pass shapes only, empty for a watch.
type ReviewProvider struct {
	Provider       provKind
	Enabled        bool
	OnPROpen       bool
	Command        string
	BaseFlag       string
	TimeoutSeconds int
	Model          string
	Author         string
	Transports     TransportSet
	GitHubInline   bool
	Notify         bool
	SendToAgent    bool
	Visible        bool
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
	BaseFlag       *string       `toml:"base_flag,omitempty"`
	TimeoutSeconds *int          `toml:"timeout_seconds,omitempty"`
	Model          *string       `toml:"model,omitempty"`
	Author         *string       `toml:"author,omitempty"`
	Transports     *TransportSet `toml:"transports,omitempty"`
	GitHubInline   *bool         `toml:"github_inline,omitempty"`
	Notify         *bool         `toml:"notify,omitempty"`
	SendToAgent    *bool         `toml:"send_to_agent,omitempty"`
	Visible        *bool         `toml:"visible,omitempty"`
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

// applyKindDefaults fills the three defaults that depend on WHICH kind a
// provider is. It is called with only p.Provider set, so it must never read
// another field.
//
//   - TimeoutSeconds: an agent pass reads the PR's files before it reports, so a
//     real PR takes minutes where a CLI pass takes seconds — hence
//     DefaultClaudeReviewTimeoutSeconds for the agent family and
//     DefaultReviewTimeoutSeconds for everything else.
//   - BaseFlag: the cli family names the base branch on the argv
//     (DefaultReviewBaseFlag). Non-cli kinds have no argv of their own.
//   - Author: coderabbit-watch watches CodeRabbit. bot-watch deliberately gets
//     NO default — it exists to watch a different bot, and defaulting it to
//     CodeRabbit's login would silently make the two kinds identical.
func applyKindDefaults(p *ReviewProvider) {
	p.TimeoutSeconds = DefaultReviewTimeoutSeconds
	if _, isAgent := p.Provider.reviewAgent(); isAgent {
		p.TimeoutSeconds = DefaultClaudeReviewTimeoutSeconds
	}
	if p.Provider.isCLI() {
		p.BaseFlag = DefaultReviewBaseFlag
	}
	if p.Provider == provCoderabbitWatch {
		p.Author = DefaultCodeRabbitAuthor
	}
}

// resolveReviewProvider applies the per-provider defaults (§1.3): transports
// absent -> [lola] and lola always force-appended; notify / send_to_agent /
// on_pr_open / github_inline absent -> true; fallback absent/empty -> none. The
// three KIND-DEPENDENT defaults (timeout_seconds, base_flag, author) are applied
// from applyKindDefaults once the kind is known, BEFORE the explicit keys overlay, so
// an explicit value always wins and a kind that has no sensible default (a
// bot-watch's author, a custom-cli's command) resolves EMPTY and is caught by
// validation rather than silently inheriting CodeRabbit's.
func resolveReviewProvider(fp fileReviewProvider) ReviewProvider {
	p := ReviewProvider{
		OnPROpen:     true,
		GitHubInline: true,
		Notify:       true,
		SendToAgent:  true,
		Visible:      true,
	}
	if fp.Provider != nil {
		p.Provider = *fp.Provider
	}
	applyKindDefaults(&p)
	if fp.Enabled != nil {
		p.Enabled = *fp.Enabled
	}
	if fp.OnPROpen != nil {
		p.OnPROpen = *fp.OnPROpen
	}
	if fp.Command != nil {
		p.Command = *fp.Command
	}
	if fp.BaseFlag != nil {
		p.BaseFlag = *fp.BaseFlag
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
	if fp.GitHubInline != nil {
		p.GitHubInline = *fp.GitHubInline
	}
	if fp.Notify != nil {
		p.Notify = *fp.Notify
	}
	if fp.SendToAgent != nil {
		p.SendToAgent = *fp.SendToAgent
	}
	if fp.Visible != nil {
		p.Visible = *fp.Visible
	}
	if p.Provider.isWatch() {
		p.Visible = false // a watch has no exec to watch: it polls the PR
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
			BaseFlag:       &p.BaseFlag,
			TimeoutSeconds: &p.TimeoutSeconds,
			Model:          &p.Model,
			Author:         &p.Author,
			GitHubInline:   &p.GitHubInline,
			Notify:         &p.Notify,
			SendToAgent:    &p.SendToAgent,
			Visible:        &p.Visible,
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

// ReviewProviderKinds is the selectable provider-kind catalog, as strings, in
// the order a UI should offer them. Both UIs BUILD THEIR PROVIDER EDITORS FROM
// THIS LIST rather than hardcoding kinds, so adding a kind here is all it takes
// for both to offer it.
func ReviewProviderKinds() []string {
	out := make([]string, len(provKinds))
	for i, k := range provKinds {
		out[i] = string(k)
	}
	return out
}

// ReviewProviderPassKinds is the subset of ReviewProviderKinds with the sync
// "pass" shape — the kinds a fallback chain may reference and `lola review
// --provider` may force.
func ReviewProviderPassKinds() []string {
	var out []string
	for _, k := range provKinds {
		if !k.isWatch() {
			out = append(out, string(k))
		}
	}
	return out
}

// TransportTokens is the selectable transport multiselect, as strings.
func TransportTokens() []string {
	return []string{string(TransportLola), string(TransportGitHub), string(TransportLinear)}
}

// ValidReviewProviderKind reports whether s names a known provider kind.
func ValidReviewProviderKind(s string) bool { return provKind(s).valid() }

// IsWatchKind reports whether s is a watch-family kind (no fallback / no github
// transport — the UI hides those affordances for it).
func IsWatchKind(s string) bool { return provKind(s).isWatch() }

// IsCLIKind reports whether s execs an external review CLI, so a UI should offer
// it the `command` / `base_flag` fields.
func IsCLIKind(s string) bool { return provKind(s).isCLI() }

// ReviewAgentFor returns the coding agent kind s reviews with ("claude" |
// "codex" | "opencode"), and false when s is not an agent-family kind — the test
// a UI uses to decide whether to offer the `model` field.
func ReviewAgentFor(s string) (string, bool) {
	a, ok := provKind(s).reviewAgent()
	return string(a), ok
}

// ReviewKindRequiresCommand reports whether s has no built-in tool of its own,
// so `command` is mandatory rather than an override.
func ReviewKindRequiresCommand(s string) bool { return provKind(s) == provCustomCLI }

// ReviewKindRequiresAuthor reports whether s has no default bot login, so
// `author` is mandatory.
func ReviewKindRequiresAuthor(s string) bool { return provKind(s) == provBotWatch }

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
	k := provKind(kind)
	if !k.valid() {
		return ReviewProvider{}, false
	}
	// Resolve an entry that carries ONLY the kind, so every kind-dependent
	// default (timeout, base flag, author, the watch's Visible rule) is applied
	// by the same code path a fresh [[review.provider]] takes on load.
	return resolveReviewProvider(fileReviewProvider{Provider: &k}), true
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
			BaseFlag:       DefaultReviewBaseFlag, // the legacy pass always passed --base
			TimeoutSeconds: rc.TimeoutSeconds,
			Author:         DefaultCodeRabbitAuthor,
			Transports:     tr,
			GitHubInline:   true, // matches the catalog default (moot without the github transport)
			Notify:         true,
			Visible:        true, // a pass is watchable; matches the catalog default
			SendToAgent:    rc.SendToAgent,
		})
	}
	if cc != (CodeRabbitConfig{}) {
		tr := TransportSet{TransportLola}
		if cc.CommentOnLinear {
			tr = append(tr, TransportLinear)
		}
		out = append(out, ReviewProvider{
			Provider:     provCoderabbitWatch,
			Enabled:      cc.Enabled,
			Author:       cc.Author,
			Transports:   tr,
			GitHubInline: true, // matches the catalog default (a watch takes no github transport)
			Notify:       cc.Notify,
			SendToAgent:  cc.SendToAgent,
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
