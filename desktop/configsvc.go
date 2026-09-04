package main

import (
	"context"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitrepo"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/secrets"
)

// First-run setup constants, matching the TUI wizard (internal/tui/setup.go) so
// a config written by either is read identically.
const (
	setupKeychainService = "lola-linear"
	setupEnvVar          = "LINEAR_API_KEY"
)

// storeLinearKey is the exec seam over the keychain write, so tests exercise
// Setup and SetLinearKey without touching the operator's real login keychain.
var storeLinearKey = secrets.StoreLinearAPIKey

// ConfigService lets the settings / project / poll forms read and write
// config.toml directly, the same way the TUI does — the daemon protocol has no
// config-write command; config.toml is the single source of truth and the
// daemon only re-reads it on `reload`. Every Save validates, persists atomically
// (config.Save is temp+rename, 0600), then best-effort reloads a live daemon.
//
// Secrets are WRITE-ONLY here, and only through SetLinearKey: the Linear key and
// Slack webhook live in the keychain / env by *name*, and those name fields are
// the only secret-adjacent values any DTO carries. Nothing ever reads a secret
// back out to the frontend — LinearKeyStatus reports where the key lives and
// whether it resolves, never its value.
//
// ConnectCode is the ONE deliberate exception, and it is written down rather
// than left as an inconsistency. It returns the phone listener's bearer key
// because the key IS the thing being handed over — a QR nobody can read is not
// a hand-off — and the exception costs nothing in privilege: the answer comes
// over ~/.lola/lola.sock, which is srw------- inside a 0700 directory, and
// anything that can open it already reaches cmd=answer, which types into a
// running coding agent. What the exception does cost is EXPOSURE, so the rule
// it replaces the write-only one with is narrower rather than absent: the value
// is fetched only when a human asks for it, it is never logged or persisted on
// this side, and the surface that renders it has a hide.
type ConfigService struct{}

func loadConfig() (*config.Config, string, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, path, err
	}
	return cfg, path, nil
}

func saveConfig(cfg *config.Config, path string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := cfg.Save(path); err != nil {
		return err
	}
	// The write SUCCEEDED; the reload is advisory. A down daemon is fine (it
	// reads the file on next start), but a daemon that is up and REJECTS the
	// config is worth surfacing — it used to be discarded, so a live daemon
	// could sit on stale config indefinitely with the UI reporting success.
	if err := call(protocol.Request{Cmd: "reload"}, shortTimeout, nil); err != nil {
		if hint := reloadRejectionHint(err.Error()); hint != "" {
			return errors.New("saved, but the running daemon rejected it: " + hint)
		}
	}
	return nil
}

// reloadRejectionHint returns a user-facing explanation for a daemon reload
// rejection, or "" when the reload merely failed to reach a daemon (down, or a
// command an older daemon does not know) — neither of which is a problem worth
// interrupting a successful save for.
//
// A rejection means the daemon disagrees with the Validate this build just ran,
// and the daemon is the one running older code: it does not hot-reload its own
// binary. The tell is a complaint about a key now INHERITED from [defaults] and
// therefore no longer written into the project's own table.
func reloadRejectionHint(msg string) string {
	if !strings.Contains(msg, "config invalid") {
		return ""
	}
	for _, key := range []string{"match_mode", "dedup_mode", "on_sent_set_label", "priority_sort", "blocked_label_id", "match_labels"} {
		if strings.Contains(msg, key) {
			return msg + "  — this build accepts that config; the daemon is an OLDER binary predating [defaults] inheritance. Restart it from the daemon controls."
		}
	}
	return msg
}

// --- settings ([defaults]/[notify]/[brain]/[review]/[coderabbit]) -----------

type SettingsDTO struct {
	GlobalCap      int    `json:"globalCap"`
	ConcurrencyCap int    `json:"concurrencyCap"`
	PollInterval   string `json:"pollInterval"` // duration string, e.g. "60s"
	Agent          string `json:"agent"`        // claude|codex|opencode

	NotifyDesktop   bool   `json:"notifyDesktop"`
	SlackWebhookEnv string `json:"slackWebhookEnv"` // env var NAME, never the URL

	BrainEnabled             bool   `json:"brainEnabled"`
	BrainModel               string `json:"brainModel"`
	BrainTimeout             int    `json:"brainTimeout"`
	BrainSummarizeEscalation bool   `json:"brainSummarizeEscalation"`
	BrainSummarizeApproved   bool   `json:"brainSummarizeApproved"`

	// [statusagent] — the display-only status interpreter.
	StatusAgentEnabled           bool    `json:"statusAgentEnabled"`
	StatusAgentBin               string  `json:"statusAgentBin"`
	StatusAgentModel             string  `json:"statusAgentModel"`
	StatusAgentTimeout           int     `json:"statusAgentTimeout"`
	StatusAgentMinInterval       int     `json:"statusAgentMinInterval"`
	StatusAgentMaxPerCycle       int     `json:"statusAgentMaxPerCycle"`
	StatusAgentMinConfidence     float64 `json:"statusAgentMinConfidence"`
	StatusAgentIncludeTranscript bool    `json:"statusAgentIncludeTranscript"`

	// [remote] — the phone listener (mobile/PLAN.md milestone 1). Three keys and
	// no inheritance, because a listener is a property of the MACHINE rather than
	// of a project. RemoteBind is either one of config.RemoteBinds or an IP
	// literal, so the form must be able to round-trip a literal it cannot offer
	// in a picker; coercing one back to a keyword on save would silently rebind
	// the daemon to a different set of interfaces.
	RemoteEnabled     bool   `json:"remoteEnabled"`
	RemoteBind        string `json:"remoteBind"`
	RemotePort        int    `json:"remotePort"`
	RemoteInsecureLAN bool   `json:"remoteInsecureLan"`
	RemoteAdvertise   bool   `json:"remoteAdvertise"`

	// ReviewProviders is the pluggable review catalog ([[review.provider]]),
	// resolved to the EFFECTIVE set (the real catalog, or the entries synthesized
	// from the legacy [review]/[coderabbit] tables). ReviewLegacy reports that the
	// config still carries the legacy tables and no catalog — in which state the
	// UI shows the providers read-only and offers MigrateReview.
	ReviewProviders []ReviewProviderDTO `json:"reviewProviders"`
	ReviewLegacy    bool                `json:"reviewLegacy"`

	// Project defaults: the [defaults] counterpart of each inheritable
	// [[project]] key. A project that does not override the key uses these.
	BranchPrefix   string   `json:"branchPrefix"`
	Symlinks       []string `json:"symlinks"`
	PostCreate     []string `json:"postCreate"`
	Env            []string `json:"env"` // "KEY=value" lines
	MatchLabels    []string `json:"matchLabels"`
	MatchMode      string   `json:"matchMode"`
	OnSentSetLabel string   `json:"onSentSetLabel"`
	BlockedLabelID string   `json:"blockedLabelId"`
	DedupMode      string   `json:"dedupMode"`
	PrioritySort   []string `json:"prioritySort"`
}

// ReviewProviderDTO is one entry of the review provider catalog, flattened for
// the settings form. Provider/Fallback/Transports are plain strings so the
// frontend never needs the (unexported) provKind type; the Go side converts.
type ReviewProviderDTO struct {
	Provider       string   `json:"provider"` // one of ReviewKinds()
	Enabled        bool     `json:"enabled"`
	OnPROpen       bool     `json:"onPrOpen"`
	Command        string   `json:"command"`        // cli family
	BaseFlag       string   `json:"baseFlag"`       // cli family; empty appends no base
	TimeoutSeconds int      `json:"timeoutSeconds"` // pass shapes
	Model          string   `json:"model"`          // agent family
	Author         string   `json:"author"`         // watch family
	Transports     []string `json:"transports"`     // lola (always) | github | linear
	GitHubInline   bool     `json:"githubInline"`   // github: anchored, resolvable threads instead of one comment
	Notify         bool     `json:"notify"`
	SendToAgent    bool     `json:"sendToAgent"`
	Visible        bool     `json:"visible"`  // pass shapes: run in a watchable "<session>-review" tmux session
	Fallback       []string `json:"fallback"` // ordered pass kinds
}

// PrioritySortKeys returns the sort keys the daemon understands, so the
// settings form can offer them instead of taking free text. These are LOLA's
// own keys, not a Linear concept — there is nothing to fetch from the API.
func (s *ConfigService) PrioritySortKeys() []string {
	return append([]string(nil), config.PrioritySortKeys...)
}

// RemoteBinds returns the [remote].bind keywords the daemon accepts, so the
// settings form offers them instead of taking free text. Same posture as
// PrioritySortKeys: MEMBERSHIP is the Go side's call, since config.Validate
// rejects anything that is neither one of these nor an IP literal.
//
// The literal case is why the form cannot be a picker alone — see the comment
// on SettingsDTO.RemoteBind.
func (s *ConfigService) RemoteBinds() []string {
	return append([]string(nil), config.RemoteBinds...)
}

// ReviewProviderKinds / TransportTokens expose the selectable catalog values so
// the frontend renders its pickers without hardcoding them.
func (s *ConfigService) ReviewProviderKinds() []string { return config.ReviewProviderKinds() }
func (s *ConfigService) TransportTokens() []string     { return config.TransportTokens() }

// ReviewKindDTO describes ONE provider kind to the settings form: its id, the
// heading it is drawn under, and which fields it actually has. The frontend
// renders its Review tab from this list instead of a hardcoded array of kinds
// plus a set of `p.provider === "…"` tests, so adding a review agent to
// config.ReviewProviderKinds() makes the app offer it with no frontend edit —
// which is the whole point of a pluggable catalog.
type ReviewKindDTO struct {
	Kind string `json:"kind"`
	// Label is the section heading: the kind's name plus what it does.
	Label string `json:"label"`
	// Watch marks the poll/watermark shape: it has an author, and neither a
	// github transport nor a fallback chain (validation forbids both).
	Watch bool `json:"watch"`
	// CLI marks a kind that execs an external review CLI (command + base flag).
	CLI bool `json:"cli"`
	// Agent names the coding agent an agent-family kind reviews with, or "" when
	// the kind is not one (it is also the "offer a model field" test).
	Agent string `json:"agent"`
	// RequiresCommand / RequiresAuthor mark the generic kinds, which carry no
	// built-in tool or bot of their own and are rejected by validation while
	// enabled-and-empty. The form marks the field required and says why.
	RequiresCommand bool `json:"requiresCommand"`
	RequiresAuthor  bool `json:"requiresAuthor"`
}

// ReviewKinds returns one descriptor per selectable provider kind, in the order
// the form should offer them.
func (s *ConfigService) ReviewKinds() []ReviewKindDTO {
	kinds := config.ReviewProviderKinds()
	out := make([]ReviewKindDTO, 0, len(kinds))
	for _, k := range kinds {
		a, _ := config.ReviewAgentFor(k)
		out = append(out, ReviewKindDTO{
			Kind:            k,
			Label:           reviewKindLabel(k),
			Watch:           config.IsWatchKind(k),
			CLI:             config.IsCLIKind(k),
			Agent:           a,
			RequiresCommand: config.ReviewKindRequiresCommand(k),
			RequiresAuthor:  config.ReviewKindRequiresAuthor(k),
		})
	}
	return out
}

// reviewKindLabel is a kind's section heading. The kind names alone
// ("custom-cli", "bot-watch") do not say what they do, so each carries one
// clause that does.
func reviewKindLabel(kind string) string {
	switch kind {
	case "coderabbit-cli":
		return kind + " — execs `coderabbit review` on PR-open"
	case "custom-cli":
		return kind + " — execs your own review CLI on PR-open"
	case "coderabbit-watch":
		return kind + " — polls the PR for the CodeRabbit app's comments"
	case "bot-watch":
		return kind + " — polls the PR for any review bot's comments"
	}
	if a, ok := config.ReviewAgentFor(kind); ok {
		return kind + " — headless `" + agent.Kind(a).Binary() + "` review on PR-open"
	}
	return kind
}

// reviewProvidersDTO flattens the effective catalog for the form.
func reviewProvidersDTO(cfg *config.Config) []ReviewProviderDTO {
	eff := cfg.EffectiveReviewProviders()
	out := make([]ReviewProviderDTO, 0, len(eff))
	for _, p := range eff {
		out = append(out, ReviewProviderDTO{
			Provider:       p.KindString(),
			Enabled:        p.Enabled,
			OnPROpen:       p.OnPROpen,
			Command:        p.Command,
			BaseFlag:       p.BaseFlag,
			TimeoutSeconds: p.TimeoutSeconds,
			Model:          p.Model,
			Author:         p.Author,
			Transports:     p.Transports.Strings(),
			GitHubInline:   p.GitHubInline,
			Notify:         p.Notify,
			SendToAgent:    p.SendToAgent,
			Visible:        p.Visible,
			Fallback:       p.FallbackStrings(),
		})
	}
	return out
}

// providersFromDTO rebuilds the catalog from the form, emitting an entry for
// every VALID kind (unknown kinds are dropped). Transports/fallback are set via
// the string setters so the frontend never touches provKind.
func providersFromDTO(dtos []ReviewProviderDTO) []config.ReviewProvider {
	var out []config.ReviewProvider
	for _, d := range dtos {
		p, ok := config.NewReviewProvider(d.Provider)
		if !ok {
			continue
		}
		p.Enabled = d.Enabled
		p.OnPROpen = d.OnPROpen
		p.Command = d.Command
		p.BaseFlag = d.BaseFlag
		p.TimeoutSeconds = d.TimeoutSeconds
		p.Model = d.Model
		p.Author = d.Author
		p.GitHubInline = d.GitHubInline
		p.Notify = d.Notify
		p.SendToAgent = d.SendToAgent
		p.Visible = d.Visible
		p.SetTransportTokens(d.Transports)
		p.SetFallbackKinds(d.Fallback)
		out = append(out, p)
	}
	return out
}

// legacyReviewOnly reports whether the config still carries the legacy tables
// and no catalog — the read-only-pending-migration state.
func legacyReviewOnly(cfg *config.Config) bool {
	hasLegacy := cfg.Review != (config.ReviewConfig{}) || cfg.CodeRabbit != (config.CodeRabbitConfig{})
	return hasLegacy && len(cfg.ReviewProviders) == 0
}

// MigrateReview folds the legacy [review]/[coderabbit] tables into the editable
// provider catalog and persists (one-way; mirrors `lola config migrate-review`
// and the TUI's in-place migrate). A no-op when there is nothing to migrate.
func (s *ConfigService) MigrateReview() error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	if !legacyReviewOnly(cfg) {
		return nil
	}
	config.MigrateLegacyReview(cfg)
	return saveConfig(cfg, path)
}

func (s *ConfigService) GetSettings() (SettingsDTO, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return SettingsDTO{}, err
	}
	return SettingsDTO{
		GlobalCap:                cfg.Defaults.GlobalCap,
		ConcurrencyCap:           cfg.Defaults.ConcurrencyCap,
		PollInterval:             cfg.Defaults.PollInterval.String(),
		Agent:                    cfg.Defaults.Agent,
		NotifyDesktop:            cfg.Notify.Desktop,
		SlackWebhookEnv:          cfg.Notify.SlackWebhookEnv,
		BrainEnabled:             cfg.Brain.Enabled,
		BrainModel:               cfg.Brain.Model,
		BrainTimeout:             cfg.Brain.TimeoutSeconds,
		BrainSummarizeEscalation: cfg.Brain.SummarizeEscalation,
		BrainSummarizeApproved:   cfg.Brain.SummarizeApproved,

		StatusAgentEnabled:           cfg.StatusAgent.Enabled,
		StatusAgentBin:               cfg.StatusAgent.Bin,
		StatusAgentModel:             cfg.StatusAgent.Model,
		StatusAgentTimeout:           cfg.StatusAgent.TimeoutSeconds,
		StatusAgentMinInterval:       cfg.StatusAgent.MinIntervalSeconds,
		StatusAgentMaxPerCycle:       cfg.StatusAgent.MaxPerCycle,
		StatusAgentMinConfidence:     cfg.StatusAgent.MinConfidence,
		StatusAgentIncludeTranscript: cfg.StatusAgent.IncludeTranscript,

		// The EFFECTIVE values, not the raw ones: BindMode/ListenPort resolve ""
		// and 0 to their defaults, so the form shows what the daemon would
		// actually do rather than a blank that reads as "nothing".
		RemoteEnabled:     cfg.Remote.Enabled,
		RemoteBind:        cfg.Remote.BindMode(),
		RemotePort:        cfg.Remote.ListenPort(),
		RemoteInsecureLAN: cfg.Remote.InsecureLAN,
		RemoteAdvertise:   cfg.Remote.Advertise,

		ReviewProviders: reviewProvidersDTO(cfg),
		ReviewLegacy:    legacyReviewOnly(cfg),

		BranchPrefix:   cfg.Defaults.BranchPrefix,
		Symlinks:       cfg.Defaults.Symlinks,
		PostCreate:     cfg.Defaults.PostCreate,
		Env:            envToLines(cfg.Defaults.Env),
		MatchLabels:    cfg.Defaults.MatchLabels,
		MatchMode:      cfg.Defaults.MatchMode,
		OnSentSetLabel: cfg.Defaults.OnSentSetLabel,
		BlockedLabelID: cfg.Defaults.BlockedLabelID,
		DedupMode:      cfg.Defaults.DedupMode,
		PrioritySort:   cfg.Defaults.PrioritySort,
	}, nil
}

func (s *ConfigService) SaveSettings(dto SettingsDTO) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	if dto.PollInterval != "" {
		d, perr := time.ParseDuration(dto.PollInterval)
		if perr != nil {
			return errors.New("poll interval: " + perr.Error())
		}
		cfg.Defaults.PollInterval = d
	}
	cfg.Defaults.GlobalCap = dto.GlobalCap
	cfg.Defaults.ConcurrencyCap = dto.ConcurrencyCap
	cfg.Defaults.Agent = dto.Agent
	cfg.Notify.Desktop = dto.NotifyDesktop
	cfg.Notify.SlackWebhookEnv = dto.SlackWebhookEnv
	cfg.Brain.Enabled = dto.BrainEnabled
	cfg.Brain.Model = dto.BrainModel
	cfg.Brain.TimeoutSeconds = dto.BrainTimeout
	cfg.Brain.SummarizeEscalation = dto.BrainSummarizeEscalation
	cfg.Brain.SummarizeApproved = dto.BrainSummarizeApproved
	cfg.StatusAgent.Enabled = dto.StatusAgentEnabled
	cfg.StatusAgent.Bin = dto.StatusAgentBin
	cfg.StatusAgent.Model = dto.StatusAgentModel
	cfg.StatusAgent.TimeoutSeconds = dto.StatusAgentTimeout
	cfg.StatusAgent.MinIntervalSeconds = dto.StatusAgentMinInterval
	cfg.StatusAgent.MaxPerCycle = dto.StatusAgentMaxPerCycle
	cfg.StatusAgent.MinConfidence = dto.StatusAgentMinConfidence
	cfg.StatusAgent.IncludeTranscript = dto.StatusAgentIncludeTranscript
	cfg.Remote.Enabled = dto.RemoteEnabled
	cfg.Remote.Bind = dto.RemoteBind
	cfg.Remote.Port = dto.RemotePort
	cfg.Remote.InsecureLAN = dto.RemoteInsecureLAN
	cfg.Remote.Advertise = dto.RemoteAdvertise
	// Review catalog. While the legacy tables are still present (read-only in the
	// UI), the provider array is not written back — editing it alongside the
	// legacy tables would produce a mixed config, a hard validation error;
	// MigrateReview is the explicit path off that. In catalog mode the built
	// array replaces the catalog and the legacy tables stay empty.
	if !legacyReviewOnly(cfg) {
		cfg.ReviewProviders = providersFromDTO(dto.ReviewProviders)
	}

	env, err := linesToEnv(dto.Env)
	if err != nil {
		return err
	}
	cfg.Defaults.BranchPrefix = dto.BranchPrefix
	cfg.Defaults.Symlinks = nonEmpty(dto.Symlinks)
	cfg.Defaults.PostCreate = nonEmpty(dto.PostCreate)
	cfg.Defaults.Env = env
	cfg.Defaults.MatchLabels = nonEmpty(dto.MatchLabels)
	cfg.Defaults.MatchMode = dto.MatchMode
	cfg.Defaults.OnSentSetLabel = dto.OnSentSetLabel
	cfg.Defaults.BlockedLabelID = dto.BlockedLabelID
	cfg.Defaults.DedupMode = dto.DedupMode
	cfg.Defaults.PrioritySort = nonEmpty(dto.PrioritySort)
	return saveConfig(cfg, path)
}

// --- appearance ([ui]) ------------------------------------------------------

// The theme is deliberately NOT a SettingsDTO field, and these three methods
// are the whole of its surface:
//
//   - Instant apply is the right UX for a theme, and SaveSettings cannot give
//     it: that is a whole-form commit the overlay closes on, so a picker routed
//     through it would only take effect after save+close — you could never see
//     what you were picking.
//   - Blast radius. SaveSettings writes ~30 fields across [defaults]/[notify]/
//     [brain]/[review]/[coderabbit]; a cosmetic click must not be able to
//     commit half-edited form state or fail on an unrelated field's validation.
//   - The reader is not the settings form. The app shell and terminals need the
//     theme at startup, long before any overlay exists — loading and marshaling
//     the entire config to learn one string would make the paint depend on
//     every unrelated settings field being readable.
//   - Single writer. A DTO field that is read but ignored on write is a trap;
//     keeping Theme off the DTO makes SetTheme the sole writer by construction.

// Themes returns the theme identifiers config.Validate accepts, so the settings
// form enumerates them instead of carrying its own copy that could drift out of
// sync and start writing configs the daemon rejects. Same precedent as
// PrioritySortKeys above.
func (s *ConfigService) Themes() []string {
	return append([]string(nil), config.UIThemes...)
}

// GetTheme returns the EFFECTIVE theme identifier — never "" — so the frontend
// has one unambiguous value to apply. A config that cannot be read falls back
// to the default rather than erroring: a broken or missing config must still
// paint.
func (s *ConfigService) GetTheme() string {
	cfg, _, err := loadConfig()
	if err != nil {
		return config.DefaultUITheme
	}
	return cfg.UITheme()
}

// SetTheme persists [ui].theme on its own. It writes through the shared
// saveConfig path — Validate, then config.Save's atomic temp+rename, then a
// best-effort daemon reload — so an unknown identifier is rejected here by the
// same static check the daemon applies, and nothing writes the file directly.
// An empty name clears the key, which drops the [ui] table and restores
// config.DefaultUITheme.
func (s *ConfigService) SetTheme(name string) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	cfg.UI.Theme = strings.TrimSpace(name)
	if err := saveConfig(cfg, path); err != nil {
		return err
	}
	// Follow the switch on the native NSWindow too. The webview repaints itself
	// via the frontend applier, but the window background is native chrome and
	// would otherwise lag until restart. cfg.UITheme() resolves ""→default so an
	// empty (cleared) theme repaints to the default flavor's canvas.
	repaintWindowCanvas(cfg.UITheme())
	return nil
}

// --- project editor ---------------------------------------------------------

// InheritsDTO mirrors config.ProjectInherits: true means the project leaves the
// key to [defaults], so the form shows the resolved value as a ghost and the
// key is not written into the project's own table.
type InheritsDTO struct {
	Symlinks       bool `json:"symlinks"`
	PostCreate     bool `json:"postCreate"`
	Env            bool `json:"env"`
	MatchLabels    bool `json:"matchLabels"`
	MatchMode      bool `json:"matchMode"`
	OnSentSetLabel bool `json:"onSentSetLabel"`
	BlockedLabelID bool `json:"blockedLabelId"`
	DedupMode      bool `json:"dedupMode"`
	PrioritySort   bool `json:"prioritySort"`
}

// ProjectFormDTO is the whole of one [[project]] — repository setup, Linear
// polling filter and write-back — because a project IS the poll unit. The
// values are the RESOLVED ones (see config.ResolveInheritance); Inherits says
// which of them came from [defaults] rather than the project itself.
type ProjectFormDTO struct {
	// Repository / worktree setup. Name is the project's ID — a path segment and
	// the prefix of every session/tmux name — while Label is the free-text
	// display string ("" falls back to Name). Changing Label here is an ordinary
	// save; changing Name is a RENAME and must go through
	// DaemonService.RenameProject FIRST, so that by the time SaveProject runs the
	// project on disk already answers to the new id.
	// The project's GROUP is deliberately absent: filing a project is done in
	// the sidebar, by dragging its row onto a folder, and a second place to set
	// it would let a stale form move a project nobody dragged. SaveProject leaves
	// Project.Group untouched.
	Name          string   `json:"name"`
	Label         string   `json:"label"`
	Path          string   `json:"path"`
	Repo          string   `json:"repo"`
	DefaultBranch string   `json:"defaultBranch"`
	BranchPrefix  string   `json:"branchPrefix"`
	Agent         string   `json:"agent"` // ""=inherit | claude | codex | opencode
	Symlinks      []string `json:"symlinks"`
	PostCreate    []string `json:"postCreate"`
	// DevCommands are the project's long-running dev processes, run by whichever
	// session is ACTIVE (one per project — see internal/daemon/dev.go). Not an
	// inheritable key: a dev command belongs to one repository.
	DevCommands []string `json:"devCommands"`
	Env         []string `json:"env"` // "KEY=value" lines

	// Linear polling filter.
	Enabled        bool     `json:"enabled"`
	TeamID         string   `json:"teamId"`
	ProjectID      string   `json:"projectId"`
	CycleMode      string   `json:"cycleMode"`
	CycleID        string   `json:"cycleId"`
	StateIDs       []string `json:"stateIds"`
	MatchLabels    []string `json:"matchLabels"`
	MatchMode      string   `json:"matchMode"`
	AssigneeMode   string   `json:"assigneeMode"`
	AssigneeUserID string   `json:"assigneeUserId"`
	ConcurrencyCap int      `json:"concurrencyCap"`
	DedupMode      string   `json:"dedupMode"`
	OnSentSetLabel string   `json:"onSentSetLabel"`

	// Linear write-back.
	OnSpawnStateID   string `json:"onSpawnStateId"`
	OnPRStateID      string `json:"onPrStateId"`
	OnMergedStateID  string `json:"onMergedStateId"`
	BlockedLabelID   string `json:"blockedLabelId"`
	CommentOnSpawn   bool   `json:"commentOnSpawn"`
	CommentOnPR      bool   `json:"commentOnPr"`
	CommentOnMerged  bool   `json:"commentOnMerged"`
	CommentOnBlocked bool   `json:"commentOnBlocked"`
	PRRequiresChecks bool   `json:"prRequiresChecks"`

	Inherits InheritsDTO `json:"inherits"`
	IsNew    bool        `json:"isNew"`
}

// GetProject returns the named project's full form state. An empty name is a
// new project: it starts inheriting everything it can, so a first project picks
// up whatever shared setup [defaults] already carries.
func (s *ConfigService) GetProject(name string) (ProjectFormDTO, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return ProjectFormDTO{}, err
	}
	if name == "" {
		blank := config.Project{
			DefaultBranch: config.DefaultBranchName,
			CycleMode:     "none",
			AssigneeMode:  "anyone",
			Inherits: config.ProjectInherits{
				Symlinks: true, PostCreate: true, Env: true,
				MatchLabels: true, MatchMode: true, OnSentSetLabel: true,
				BlockedLabelID: true, DedupMode: true, PrioritySort: true,
			},
		}
		// Resolve against a scratch config so the new project's ghosts show the
		// [defaults] values the real project will inherit once saved.
		scratch := *cfg
		scratch.Projects = []config.Project{blank}
		scratch.ResolveInheritance()
		dto := projectDTO(&scratch.Projects[0])
		dto.IsNew = true
		return dto, nil
	}
	p := cfg.ProjectByName(name)
	if p == nil {
		return ProjectFormDTO{}, errors.New("no such project: " + name)
	}
	return projectDTO(p), nil
}

func projectDTO(p *config.Project) ProjectFormDTO {
	return ProjectFormDTO{
		Name:          p.Name,
		Label:         p.Label,
		Path:          p.Path,
		Repo:          p.Repo,
		DefaultBranch: p.DefaultBranch,
		BranchPrefix:  p.BranchPrefix,
		Agent:         p.Agent,
		Symlinks:      p.Symlinks,
		PostCreate:    p.PostCreate,
		DevCommands:   p.DevCommands,
		Env:           envToLines(p.Env),

		Enabled:        p.Enabled,
		TeamID:         p.TeamID,
		ProjectID:      p.ProjectID,
		CycleMode:      p.CycleMode,
		CycleID:        p.CycleID,
		StateIDs:       p.StateIDs,
		MatchLabels:    p.MatchLabels,
		MatchMode:      p.MatchMode,
		AssigneeMode:   p.AssigneeMode,
		AssigneeUserID: p.AssigneeUserID,
		ConcurrencyCap: p.ConcurrencyCap,
		DedupMode:      p.DedupMode,
		OnSentSetLabel: p.OnSentSetLabel,

		OnSpawnStateID:   p.OnSpawnStateID,
		OnPRStateID:      p.OnPRStateID,
		OnMergedStateID:  p.OnMergedStateID,
		BlockedLabelID:   p.BlockedLabelID,
		CommentOnSpawn:   p.CommentOnSpawn,
		CommentOnPR:      p.CommentOnPR,
		CommentOnMerged:  p.CommentOnMerged,
		CommentOnBlocked: p.CommentOnBlocked,
		PRRequiresChecks: p.PRRequiresChecks,

		Inherits: InheritsDTO{
			Symlinks:       p.Inherits.Symlinks,
			PostCreate:     p.Inherits.PostCreate,
			Env:            p.Inherits.Env,
			MatchLabels:    p.Inherits.MatchLabels,
			MatchMode:      p.Inherits.MatchMode,
			OnSentSetLabel: p.Inherits.OnSentSetLabel,
			BlockedLabelID: p.Inherits.BlockedLabelID,
			DedupMode:      p.Inherits.DedupMode,
			PrioritySort:   p.Inherits.PrioritySort,
		},
	}
}

func (s *ConfigService) SaveProject(dto ProjectFormDTO) error {
	// The id is canonicalized here as well as in the form: this is the last gate
	// before it becomes a directory name, and a client that skipped the frontend
	// slug must not be able to write a name with a "/" in it.
	name := config.Slug(dto.Name)
	if name == "" {
		return errors.New("project id is required — a label like \"Nori App\" becomes the id \"nori-app\"")
	}
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	env, err := linesToEnv(dto.Env)
	if err != nil {
		return err
	}
	p := cfg.ProjectByName(name)
	if p == nil {
		if !dto.IsNew {
			// An existing project that no longer answers to this id means the
			// rename that should have preceded this save did not happen. Appending
			// would silently fork the project in two, so refuse.
			return errors.New("no such project: " + name + " (rename it via the daemon before saving)")
		}
		cfg.Projects = append(cfg.Projects, config.Project{Name: name})
		p = &cfg.Projects[len(cfg.Projects)-1]
	}
	prioritySort := p.PrioritySort // not exposed by the form; preserved as-is

	// A label identical to the id carries nothing; drop it so DisplayName's
	// fallback does the work and the file stays free of redundant keys.
	p.Label = strings.TrimSpace(dto.Label)
	if p.Label == name {
		p.Label = ""
	}
	p.Path = dto.Path
	p.Repo = dto.Repo
	p.DefaultBranch = dto.DefaultBranch
	p.BranchPrefix = dto.BranchPrefix
	p.Agent = dto.Agent
	p.Symlinks = nonEmpty(dto.Symlinks)
	p.PostCreate = nonEmpty(dto.PostCreate)
	p.DevCommands = nonEmpty(dto.DevCommands)
	p.Env = env

	p.Enabled = dto.Enabled
	p.TeamID = dto.TeamID
	p.ProjectID = dto.ProjectID
	p.CycleMode = dto.CycleMode
	p.CycleID = dto.CycleID
	p.StateIDs = nonEmpty(dto.StateIDs)
	p.MatchLabels = nonEmpty(dto.MatchLabels)
	p.MatchMode = dto.MatchMode
	p.AssigneeMode = dto.AssigneeMode
	p.AssigneeUserID = dto.AssigneeUserID
	p.ConcurrencyCap = dto.ConcurrencyCap
	p.DedupMode = dto.DedupMode
	p.OnSentSetLabel = dto.OnSentSetLabel
	p.PrioritySort = prioritySort

	p.OnSpawnStateID = dto.OnSpawnStateID
	p.OnPRStateID = dto.OnPRStateID
	p.OnMergedStateID = dto.OnMergedStateID
	p.BlockedLabelID = dto.BlockedLabelID
	p.CommentOnSpawn = dto.CommentOnSpawn
	p.CommentOnPR = dto.CommentOnPR
	p.CommentOnMerged = dto.CommentOnMerged
	p.CommentOnBlocked = dto.CommentOnBlocked
	p.PRRequiresChecks = dto.PRRequiresChecks

	p.Inherits = config.ProjectInherits{
		Symlinks:       dto.Inherits.Symlinks,
		PostCreate:     dto.Inherits.PostCreate,
		Env:            dto.Inherits.Env,
		MatchLabels:    dto.Inherits.MatchLabels,
		MatchMode:      dto.Inherits.MatchMode,
		OnSentSetLabel: dto.Inherits.OnSentSetLabel,
		BlockedLabelID: dto.Inherits.BlockedLabelID,
		DedupMode:      dto.Inherits.DedupMode,
		PrioritySort:   dto.Inherits.PrioritySort,
	}
	return saveConfig(cfg, path)
}

// PathInfoDTO is everything the project form derives from one picked folder.
// Path is the checkout ROOT when there is one (picking a subdirectory still
// configures the repository), else the directory as given.
type PathInfoDTO struct {
	Path           string   `json:"path"`
	IsRepo         bool     `json:"isRepo"`
	Repo           string   `json:"repo"`
	DefaultBranch  string   `json:"defaultBranch"`
	Branches       []string `json:"branches"`
	SuggestedLabel string   `json:"suggestedLabel"`
	SuggestedID    string   `json:"suggestedId"`
}

// InspectPath reads a checkout in ONE pass — GitHub "owner/name", the branch
// worktrees should fork from, the branch list and a suggested label/id — so
// picking a folder fills the Repo tab instead of making the user copy four
// values by hand.
//
// Every unknown is empty rather than guessed: a non-GitHub or unrecognised
// remote leaves Repo "" (fail-closed — that disables PR checks instead of
// pointing them at the wrong repository), and a directory that is not a checkout
// simply contributes nothing.
//
// The label/id suggestion is computed HERE rather than in the frontend so the
// app and the TUI propose the same identity for the same folder.
func (s *ConfigService) InspectPath(path string) PathInfoDTO {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	info := gitrepo.Inspect(ctx, path)

	out := PathInfoDTO{
		Path:          strings.TrimSpace(path),
		IsRepo:        info.IsRepo,
		Repo:          info.Repo,
		DefaultBranch: info.DefaultBranch,
		Branches:      info.Branches,
	}
	if info.Root != "" {
		out.Path = info.Root
	}
	if out.Branches == nil {
		out.Branches = []string{} // marshal as [] rather than null for the frontend
	}
	out.SuggestedLabel = config.LabelFromPath(out.Path)
	out.SuggestedID = config.Slug(out.SuggestedLabel)
	return out
}

// PickFolder opens the native directory chooser and returns the selected path,
// or "" when the user cancels. start seeds the dialog's directory so re-picking
// a project's folder does not begin at $HOME again.
//
// The dialog is the app's, not the form's: only the backend can put a real macOS
// panel in front of the window, which is why this is a service method rather
// than anything in the frontend.
func (s *ConfigService) PickFolder(start string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("no application window to attach a dialog to")
	}
	d := app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		ResolvesAliases(true).
		SetTitle("Choose the project's repository").
		SetButtonText("Use this folder")
	if s := strings.TrimSpace(start); s != "" {
		d.SetDirectory(s)
	}
	if win := app.Window.Current(); win != nil {
		d.AttachToWindow(win)
	}
	return d.PromptForSingleSelection()
}

func (s *ConfigService) RemoveProject(name string) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	out := cfg.Projects[:0]
	for _, p := range cfg.Projects {
		if p.Name != name {
			out = append(out, p)
		}
	}
	cfg.Projects = out
	return saveConfig(cfg, path)
}

// --- first-run setup --------------------------------------------------------

// ConfigExists reports whether ~/.lola/config.toml is present, so the frontend
// can gate a first-run setup screen.
func (s *ConfigService) ConfigExists() bool {
	path, err := config.DefaultPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// ValidateLinearKey checks a key against Linear's API (Viewer), so the setup
// screen can confirm it before writing config. Bounded to 15s.
func (s *ConfigService) ValidateLinearKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("empty key")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := linear.New(config.DefaultEndpoint, key).Viewer(ctx)
	return err
}

// LinearKeyStatusDTO describes WHERE the Linear key lives and whether it can be
// resolved right now — never the key. Source is a human-readable origin
// ("macOS Keychain (lola-linear)" / "environment variable LINEAR_API_KEY"), so
// the settings screen can say why a key it cannot see is nonetheless fine.
type LinearKeyStatusDTO struct {
	Configured bool   `json:"configured"` // config names a source
	Resolvable bool   `json:"resolvable"` // that source actually yields a key
	Source     string `json:"source"`
	Detail     string `json:"detail"` // why it does not resolve, when it doesn't
}

// LinearKeyStatus reports the key's origin and health for the settings screen.
//
// This exists because the key was settable ONLY in the first-run wizard: neither
// this app's settings nor the TUI's had a field for it, so a key could never be
// added to a hand-written config, and rotating one meant editing the Keychain by
// hand. A daemon with no key fails every poll, which is the exact silent failure
// the app is supposed to surface.
func (s *ConfigService) LinearKeyStatus() LinearKeyStatusDTO {
	cfg, _, err := loadConfig()
	if err != nil {
		return LinearKeyStatusDTO{Detail: err.Error()}
	}
	kc, env := cfg.Linear.APIKeyKeychain, cfg.Linear.APIKeyEnv
	out := LinearKeyStatusDTO{Configured: kc != "" || env != ""}
	switch {
	case kc != "":
		out.Source = "macOS Keychain (" + kc + ")"
	case env != "":
		out.Source = "environment variable " + env
	default:
		out.Detail = "no key source configured"
		return out
	}
	// Resolve only to learn IF it works. The value is discarded immediately and
	// never leaves this function.
	if _, rerr := secrets.LinearAPIKey(kc, env); rerr != nil {
		out.Detail = rerr.Error()
		return out
	}
	out.Resolvable = true
	return out
}

// ConnectCodeDTO is everything a phone needs to reach this machine's daemon:
// the scannable token plus the same values as text.
//
// Both shapes, deliberately. Code is what the Remote tab renders as a QR; the
// loose fields are what a human reads out when the camera will not focus, the
// camera permission was denied, or the client is a Simulator with no camera at
// all. A QR must be a convenience and never the only way in.
//
// Every field here is a secret while Key is set, and Code most of all — it
// CONTAINS the key. Nothing on this side writes any of it to disk or to a log,
// and the frontend keeps it behind an explicit reveal.
type ConnectCodeDTO struct {
	Code     string   `json:"code"`
	Hosts    []string `json:"hosts"`
	Port     int      `json:"port"`
	Pin      string   `json:"pin"`
	Key      string   `json:"key"`
	Insecure bool     `json:"insecure"`

	// Problem names why there is no code in one human sentence — the listener
	// is off, or nothing bound — so the tab renders a reason in place of the
	// code. It is a STATE rather than an error precisely because it is
	// actionable; a build with no bearer-key path at all IS an error, and
	// arrives as one.
	Problem string `json:"problem"`
}

// ConnectCode asks the daemon for the phone listener's connect details
// (cmd=pairBegin) so the Remote tab can hand them to a phone.
//
// It asks the DAEMON rather than reading ~/.lola/device.crt and
// ~/.lola/remote-dev-key, and that is the whole point of the method existing.
// Recomputing the pin here would mean calling remote.LoadOrCreateDeviceKey,
// whose only exported form CREATES an identity when none is there — this
// process would mint the daemon's TLS identity as a side effect of drawing a
// settings tab, from the wrong process, even with [remote] disabled. And it
// would answer about a FILE: the key file is the running daemon's key only when
// the script that wrote it also started the daemon, so a code rendered from it
// after a `lola run` from another shell produces a scan the daemon answers with
// "authenticate first" — which, from the phone, is indistinguishable from a bad
// camera read. The daemon holds the live value of both facts; only it can
// answer.
//
// The timeout is the short one: pairBegin reads in-memory state and execs
// nothing.
func (s *ConfigService) ConnectCode() (ConnectCodeDTO, error) {
	var data protocol.PairBeginData
	if err := call(protocol.Request{Cmd: "pairBegin"}, 5*time.Second, &data); err != nil {
		return ConnectCodeDTO{}, err
	}
	return ConnectCodeDTO{
		Code:     data.Code,
		Hosts:    data.Hosts,
		Port:     data.Port,
		Pin:      data.Pin,
		Key:      data.Key,
		Insecure: data.Insecure,
		Problem:  data.Problem,
	}, nil
}

// RegenerateRemoteKey rolls the phone listener's shared bearer key
// (cmd=regenerateRemoteKey) and returns once the listener has been rebuilt
// around the new one.
//
// It is milestone 1's ONLY revocation, and it is blunt: every paired phone
// loses access at once, because every paired phone holds the same key. The UI
// says so before it asks, rather than offering it as routine maintenance —
// milestone 2's per-device revocation is the precise version, and this command
// disappears with the rest of the insecure path.
//
// Like ConnectCode this asks the daemon rather than writing the file here.
// Deleting a key file from this process would roll the value on disk and leave
// the RUNNING listener authenticating with the old one, so the app would report
// a revocation that had not happened — the single worst outcome for a control
// whose entire purpose is to stop a key working.
//
// The timeout is longer than ConnectCode's because the daemon tears the
// listener down and binds a new one, which closes live connections.
func (s *ConfigService) RegenerateRemoteKey() error {
	return call(protocol.Request{Cmd: "regenerateRemoteKey"}, 15*time.Second, nil)
}

// SetLinearKey stores a new Linear key and points config.toml at it. The key
// goes to the macOS Keychain under the same service the first-run wizards use,
// so a key set here is read identically by the daemon, the TUI and this app;
// when the Keychain is unavailable it falls back to naming an env var, exactly
// as Setup does. The returned message says which happened.
//
// Deliberately NOT a SettingsDTO field, for the reasons the theme is not one
// (see below) plus one of its own: a whole-form commit would carry a secret
// through every save of an unrelated field, and a validation failure elsewhere
// in the form would silently drop the key the user just typed.
func (s *ConfigService) SetLinearKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("empty key")
	}
	cfg, path, err := loadConfig()
	if err != nil {
		return "", err
	}
	msg := ""
	if serr := storeLinearKey(setupKeychainService, key); serr == nil {
		cfg.Linear.APIKeyKeychain = setupKeychainService
		cfg.Linear.APIKeyEnv = ""
		msg = "key stored in the macOS Keychain (service " + setupKeychainService + ")"
	} else {
		// Keychain unavailable: name an env var instead. The key itself is NOT
		// written anywhere — the user must export it — so say so plainly.
		cfg.Linear.APIKeyEnv = setupEnvVar
		msg = "couldn't use the Keychain — export the key as " + setupEnvVar + " before starting the daemon"
	}
	if cfg.Linear.Endpoint == "" {
		cfg.Linear.Endpoint = config.DefaultEndpoint
	}
	if verr := cfg.Validate(); verr != nil {
		return "", verr
	}
	if serr := saveConfig(cfg, path); serr != nil {
		return "", serr
	}
	return msg, nil
}

type SetupDTO struct {
	LinearKey      string `json:"linearKey"`
	ProjectName    string `json:"projectName"`
	ProjectPath    string `json:"projectPath"`
	Repo           string `json:"repo"`
	DefaultBranch  string `json:"defaultBranch"`
	ConcurrencyCap int    `json:"concurrencyCap"`
	GlobalCap      int    `json:"globalCap"`
	PollInterval   string `json:"pollInterval"`
}

type SetupResultDTO struct {
	KeychainStored bool   `json:"keychainStored"` // key in the macOS Keychain
	EnvVar         string `json:"envVar"`         // set when the key must come from an env var instead
	Message        string `json:"message"`
}

// Setup writes the initial config.toml from the wizard: it stores the Linear key
// in the Keychain (falling back to an env var by name if that fails), records one
// project, and sets the caps/interval. The key itself is never written to config.
func (s *ConfigService) Setup(dto SetupDTO) (SetupResultDTO, error) {
	if strings.TrimSpace(dto.ProjectName) == "" {
		return SetupResultDTO{}, errors.New("project name is required")
	}
	path, err := config.DefaultPath()
	if err != nil {
		return SetupResultDTO{}, err
	}

	cfg := &config.Config{}
	cfg.Linear.Endpoint = config.DefaultEndpoint

	res := SetupResultDTO{}
	if err := storeLinearKey(setupKeychainService, dto.LinearKey); err == nil {
		cfg.Linear.APIKeyKeychain = setupKeychainService
		res.KeychainStored = true
		res.Message = "key stored in the macOS Keychain (service " + setupKeychainService + ")"
	} else {
		// Keychain unavailable (or non-darwin): fall back to an env var by name.
		cfg.Linear.APIKeyEnv = setupEnvVar
		res.EnvVar = setupEnvVar
		res.Message = "couldn't use the Keychain — export the key as " + setupEnvVar + " before starting the daemon"
	}

	cfg.Defaults.ConcurrencyCap = orDefault(dto.ConcurrencyCap, 2)
	cfg.Defaults.GlobalCap = orDefault(dto.GlobalCap, 4)
	interval := 60 * time.Second
	if dto.PollInterval != "" {
		if d, perr := time.ParseDuration(dto.PollInterval); perr == nil {
			interval = d
		}
	}
	cfg.Defaults.PollInterval = interval

	branch := dto.DefaultBranch
	if branch == "" {
		branch = config.DefaultBranchName
	}
	// The id is SLUGGED here: it becomes a worktree directory, a state filename
	// and a tmux session prefix, so a name typed as "Nori App" must not reach the
	// file with a space in it. The typed form is kept as the display Label when
	// it says more than the id does.
	name := config.Slug(dto.ProjectName)
	if name == "" {
		return SetupResultDTO{}, errors.New("project name must contain a letter or digit")
	}
	label := strings.TrimSpace(dto.ProjectName)
	if label == name {
		label = ""
	}
	cfg.Projects = []config.Project{{
		Name:          name,
		Label:         label,
		Path:          dto.ProjectPath,
		Repo:          dto.Repo,
		DefaultBranch: branch,
	}}

	if err := cfg.Validate(); err != nil {
		return res, err
	}
	if err := cfg.Save(path); err != nil {
		return res, err
	}
	_ = call(protocol.Request{Cmd: "reload"}, shortTimeout, nil)
	return res, nil
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// --- helpers ----------------------------------------------------------------

func envToLines(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func linesToEnv(lines []string) (map[string]string, error) {
	out := map[string]string{}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			return nil, errors.New("env line must be KEY=value: " + l)
		}
		out[strings.TrimSpace(k)] = v
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// --- project groups & manual order ------------------------------------------
//
// A project's place in the sidebar is TWO facts, and both live in config.toml:
// which [[group]] it is filed under (Project.Group) and where it sits in the
// order (its index in the [[project]] array — the order every surface already
// renders, TUI included, so a drag in the app reorders the TUI's list too).
//
// The frontend therefore never sends a delta. It computes the whole arrangement
// and sends it as ONE layout, which is applied atomically or not at all: a drop
// that moved a project between groups is a single write, and a layout naming a
// project or group that no longer exists is refused rather than half-applied.

// ProjectPlacementDTO is one row of the sidebar arrangement: a project id and
// the group it belongs to ("" = top level).
type ProjectPlacementDTO struct {
	Name  string `json:"name"`
	Group string `json:"group"`
}

// ProjectLayoutDTO is the WHOLE sidebar arrangement — every configured project
// in render order with its group, and every group in render order. Both lists
// are complete: a partial layout is a bug in the caller, not a merge to
// attempt, because the array positions ARE the order.
type ProjectLayoutDTO struct {
	Groups   []GroupDTO            `json:"groups"`
	Projects []ProjectPlacementDTO `json:"projects"`
}

// GroupDTO is one [[group]] as the frontend sees it. Position is its index
// among the top-level rows (see config.Group) — the sidebar draws folders beside
// the projects, so a group's place is a value of its own.
type GroupDTO struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Position  int    `json:"position"`
	Collapsed bool   `json:"collapsed"`
}

// AddGroup creates an empty group from a free-text label and returns its id.
// The id is derived with config.Slug — the same transform a project id goes
// through — and de-duplicated with a numeric suffix, so two folders a human
// would both call "Clients" can coexist instead of one silently replacing the
// other. Empty groups are the point: the folder is created first and projects
// are dragged into it after.
func (s *ConfigService) AddGroup(label string) (string, error) {
	cfg, path, err := loadConfig()
	if err != nil {
		return "", err
	}
	label = strings.TrimSpace(label)
	base := config.Slug(label)
	if base == "" {
		return "", errors.New("group name is required")
	}
	name := base
	for i := 2; cfg.GroupByName(name) != nil; i++ {
		name = base + "-" + strconv.Itoa(i)
	}
	// The new folder lands at the END of the top-level list: after the ungrouped
	// projects and after the folders that already exist. Anywhere else would
	// move rows the user did not touch.
	ungrouped := 0
	for i := range cfg.Projects {
		if cfg.Projects[i].Group == "" {
			ungrouped++
		}
	}
	g := config.Group{Name: name, Position: ungrouped + len(cfg.Groups)}
	if label != name {
		// A label identical to the id carries nothing — DisplayName falls back
		// to the id — so it is not written, exactly as a project's is not.
		g.Label = label
	}
	cfg.Groups = append(cfg.Groups, g)
	if err := saveConfig(cfg, path); err != nil {
		return "", err
	}
	return name, nil
}

// RenameGroup changes a group's DISPLAY label only. The id stays put on purpose:
// it is what every [[project]].group references, and rewriting it would have to
// rewrite those in the same breath for no gain — unlike a project id, a group id
// names no directory, tmux session or state file, so nothing reads it but this
// table.
func (s *ConfigService) RenameGroup(name, label string) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	g := cfg.GroupByName(name)
	if g == nil {
		return errors.New("no such group: " + name)
	}
	label = strings.TrimSpace(label)
	if config.Slug(label) == "" {
		return errors.New("group name is required")
	}
	if label == g.Name {
		g.Label = ""
	} else {
		g.Label = label
	}
	return saveConfig(cfg, path)
}

// RemoveGroup deletes the folder and files its members at the top level. It
// never touches a project beyond that reference: a group is arrangement, so
// deleting one must not be able to lose a project, its worktrees or its
// sessions.
func (s *ConfigService) RemoveGroup(name string) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.GroupByName(name) == nil {
		return errors.New("no such group: " + name)
	}
	out := cfg.Groups[:0]
	for _, g := range cfg.Groups {
		if g.Name != name {
			out = append(out, g)
		}
	}
	cfg.Groups = out
	for i := range cfg.Projects {
		if cfg.Projects[i].Group == name {
			cfg.Projects[i].Group = ""
		}
	}
	return saveConfig(cfg, path)
}

// SetGroupCollapsed persists a folder's disclosure state. It lives in
// config.toml rather than in the web view's storage so the app and a future TUI
// rendering agree, and so it survives a reinstall like every other arrangement
// key.
func (s *ConfigService) SetGroupCollapsed(name string, collapsed bool) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	g := cfg.GroupByName(name)
	if g == nil {
		return errors.New("no such group: " + name)
	}
	if g.Collapsed == collapsed {
		return nil // no write for a no-op toggle
	}
	g.Collapsed = collapsed
	return saveConfig(cfg, path)
}

// SetProjectLayout applies a whole sidebar arrangement: the group order, and
// every project's group plus its position. It FAILS CLOSED — the projects list
// must be an exact permutation of what is configured and the groups list an
// exact permutation of the configured groups, or nothing is written.
//
// That strictness is the point. The layout is computed by a drag handler in the
// frontend against a snapshot that may be a config reload behind; applying it
// loosely would let a stale layout resurrect a project the user just removed,
// or drop one it had not heard of yet. A refused layout costs one drag, which
// the next render corrects.
func (s *ConfigService) SetProjectLayout(dto ProjectLayoutDTO) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}

	// Groups first: the projects below reference them.
	byGroup := make(map[string]config.Group, len(cfg.Groups))
	for _, g := range cfg.Groups {
		byGroup[g.Name] = g
	}
	if len(dto.Groups) != len(byGroup) {
		return errors.New("layout is out of date: the configured groups have changed")
	}
	groups := make([]config.Group, 0, len(dto.Groups))
	seenGroup := make(map[string]bool, len(dto.Groups))
	for _, gd := range dto.Groups {
		cur, ok := byGroup[gd.Name]
		if !ok || seenGroup[gd.Name] {
			return errors.New("layout is out of date: the configured groups have changed")
		}
		seenGroup[gd.Name] = true
		// Only the PLACE (and the disclosure state, which rides along with a
		// drag) comes from the layout. The label is the rename path's to change.
		cur.Position = gd.Position
		cur.Collapsed = gd.Collapsed
		groups = append(groups, cur)
	}

	byProject := make(map[string]int, len(cfg.Projects))
	for i := range cfg.Projects {
		byProject[cfg.Projects[i].Name] = i
	}
	if len(dto.Projects) != len(byProject) {
		return errors.New("layout is out of date: the configured projects have changed")
	}
	projects := make([]config.Project, 0, len(dto.Projects))
	seenProject := make(map[string]bool, len(dto.Projects))
	for _, pd := range dto.Projects {
		idx, ok := byProject[pd.Name]
		if !ok || seenProject[pd.Name] {
			return errors.New("layout is out of date: the configured projects have changed")
		}
		seenProject[pd.Name] = true
		p := cfg.Projects[idx]
		if g := strings.TrimSpace(pd.Group); g == "" {
			p.Group = ""
		} else if seenGroup[g] {
			p.Group = g
		} else {
			return errors.New("layout names group " + g + ", which is not configured")
		}
		projects = append(projects, p)
	}

	cfg.Groups = groups
	cfg.Projects = projects
	return saveConfig(cfg, path)
}
