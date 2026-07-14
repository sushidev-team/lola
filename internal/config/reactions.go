package config

import (
	"runtime"

	"github.com/sushidev-team/lola/internal/notify"
)

// Default reaction message templates (PLAN P3.16–19). Short and imperative.
// The reaction engine fills the placeholders {{.Detail}} (fetched CI logs or
// review comments), {{.Issue}} (Linear identifier), and {{.PR}} (PR number/URL)
// by plain strings.ReplaceAll — NOT text/template — so an agent-authored PR
// body or a failing log can never inject template directives or reach an eval
// surface. A reaction whose Message is empty sends nothing to the agent.
const (
	// DefaultCIFailedMessage recovers a red PR: hand the agent the failing
	// output and tell it to fix and push.
	DefaultCIFailedMessage = "CI is failing on your PR ({{.PR}}) for {{.Issue}}. Failing output:\n\n{{.Detail}}\n\nInvestigate the failure, fix it, and push."
	// DefaultChangesRequestedMessage relays reviewer feedback back into the
	// session and asks for a re-review.
	DefaultChangesRequestedMessage = "Changes were requested on your PR ({{.PR}}) for {{.Issue}}. Reviewer feedback:\n\n{{.Detail}}\n\nAddress each point, push, then re-request review."
	// DefaultMergeConflictMessage asks the agent to rebase and resolve.
	DefaultMergeConflictMessage = "Your PR ({{.PR}}) for {{.Issue}} has merge conflicts. Rebase onto the base branch, resolve the conflicts, and push."
)

// DefaultCIRetries is the default number of automatic ci_failed recovery
// attempts before the reaction escalates instead of retrying.
const DefaultCIRetries = 2

// DefaultSlackWebhookEnv is the environment-variable NAME the [notify] table
// defaults to for the Slack webhook URL. The URL itself never lives in config.
const DefaultSlackWebhookEnv = "SLACK_WEBHOOK_URL"

// Reaction is one entry in the [reactions] table: how lola responds when a
// session's derived PR/CI status matches this reaction.
//
//   - Auto gates whether lola acts automatically (vs. notify-and-park only).
//   - Retries bounds automatic recovery attempts; it is meaningful only for
//     ci_failed (ignored by the other reactions) and must be >= 0.
//   - Message is the template sent to the live agent (see the Default*Message
//     consts for the placeholder contract); empty means "act but say nothing".
type Reaction struct {
	Auto    bool   `toml:"auto"`
	Retries int    `toml:"retries"`
	Message string `toml:"message"`
}

// ReactionsConfig is the [reactions] table. Every field is optional; unset
// reactions (and an entirely absent table) get the defaults from
// defaultReactions() on load. The zero value means "unconfigured" and is
// replaced by defaults — a fresh Config never persists disabled reactions.
type ReactionsConfig struct {
	CIFailed         Reaction `toml:"ci_failed"`
	ChangesRequested Reaction `toml:"changes_requested"`
	MergeConflict    Reaction `toml:"merge_conflict"`
	ApprovedAndGreen Reaction `toml:"approved_and_green"`
	Merged           Reaction `toml:"merged"`
}

// NotifyConfig is the [notify] table. It holds the environment-variable NAME
// for the Slack webhook, never the URL — resolve the runtime value with
// (*Config).ResolveNotify. Routing maps a priority name (urgent|action|info)
// to the channels (desktop|slack) that receive it.
type NotifyConfig struct {
	Desktop         bool                `toml:"desktop"`
	SlackWebhookEnv string              `toml:"slack_webhook_env"`
	Routing         map[string][]string `toml:"routing"`
}

// notifyPriorities maps the [notify.routing] priority KEYS as written in
// config.toml to notify.Priority. The keys are exactly the names
// notify.Priority.String() produces, so the two stay in sync.
var notifyPriorities = map[string]notify.Priority{
	notify.Urgent.String(): notify.Urgent,
	notify.Action.String(): notify.Action,
	notify.Info.String():   notify.Info,
}

// defaultDesktop is the [notify].desktop default: on (macOS only), since that
// is the only platform with a native desktop-banner channel.
func defaultDesktop() bool { return runtime.GOOS == "darwin" }

// defaultRouting is the [notify.routing] default: urgent is loud (desktop +
// Slack), action reaches both, info is Slack-only. Returns a fresh map each
// call so callers can mutate it safely.
func defaultRouting() map[string][]string {
	return map[string][]string{
		notify.Urgent.String(): {notify.ChannelDesktop, notify.ChannelSlack},
		notify.Action.String(): {notify.ChannelDesktop, notify.ChannelSlack},
		notify.Info.String():   {notify.ChannelSlack},
	}
}

// defaultReactions is the [reactions] default set (PLAN P3): CI failures,
// review changes, and merge conflicts auto-react by messaging the agent;
// approved+green never auto-merges (notify-and-park only); merged auto-cleans.
func defaultReactions() ReactionsConfig {
	return ReactionsConfig{
		CIFailed:         Reaction{Auto: true, Retries: DefaultCIRetries, Message: DefaultCIFailedMessage},
		ChangesRequested: Reaction{Auto: true, Message: DefaultChangesRequestedMessage},
		MergeConflict:    Reaction{Auto: true, Message: DefaultMergeConflictMessage},
		ApprovedAndGreen: Reaction{Auto: false, Message: ""},
		Merged:           Reaction{Auto: true},
	}
}

// defaultNotify is the [notify] default: desktop on (darwin), the standard
// Slack env-var name, and defaultRouting().
func defaultNotify() NotifyConfig {
	return NotifyConfig{
		Desktop:         defaultDesktop(),
		SlackWebhookEnv: DefaultSlackWebhookEnv,
		Routing:         defaultRouting(),
	}
}

// ResolveNotify projects the [notify] table into the runtime notify.NotifyConfig
// the notifier consumes. It reads the Slack webhook URL from the environment
// variable named by SlackWebhookEnv (via notify.ResolveWebhook — "" when unset)
// and never errors and never logs the value; the URL is a secret. Priority
// names are mapped to notify.Priority; any name Validate would reject is skipped
// defensively so a bad routing key can never inject an unknown priority.
func (c *Config) ResolveNotify() notify.NotifyConfig {
	routing := make(map[notify.Priority][]string, len(c.Notify.Routing))
	for name, channels := range c.Notify.Routing {
		p, ok := notifyPriorities[name]
		if !ok {
			continue
		}
		routing[p] = append([]string(nil), channels...)
	}
	return notify.NotifyConfig{
		Desktop:      c.Notify.Desktop,
		SlackWebhook: notify.ResolveWebhook(c.Notify.SlackWebhookEnv),
		Routing:      routing,
	}
}

// --- on-disk mirrors -------------------------------------------------------
//
// The [reactions] and [notify] tables use pointer-per-field file mirrors so
// load can tell an ABSENT key (nil → take the default) from an explicit zero
// (auto = false, retries = 0, message = "") the operator wants preserved. The
// whole table is a nil pointer when unconfigured, so a fresh Config persists
// neither table and reloads to full defaults.

type fileReaction struct {
	Auto    *bool   `toml:"auto,omitempty"`
	Retries *int    `toml:"retries,omitempty"`
	Message *string `toml:"message,omitempty"`
}

type fileReactionsConfig struct {
	CIFailed         fileReaction `toml:"ci_failed"`
	ChangesRequested fileReaction `toml:"changes_requested"`
	MergeConflict    fileReaction `toml:"merge_conflict"`
	ApprovedAndGreen fileReaction `toml:"approved_and_green"`
	Merged           fileReaction `toml:"merged"`
}

type fileNotifyConfig struct {
	Desktop         *bool               `toml:"desktop,omitempty"`
	SlackWebhookEnv *string             `toml:"slack_webhook_env,omitempty"`
	Routing         map[string][]string `toml:"routing,omitempty"`
}

// resolve overlays the explicitly-set fields of fr onto def, leaving unset
// (nil) fields at their default.
func (fr fileReaction) resolve(def Reaction) Reaction {
	if fr.Auto != nil {
		def.Auto = *fr.Auto
	}
	if fr.Retries != nil {
		def.Retries = *fr.Retries
	}
	if fr.Message != nil {
		def.Message = *fr.Message
	}
	return def
}

// resolveReactions materializes the [reactions] table: nil (absent) yields the
// full defaults; a present table overlays each explicitly-set field.
func resolveReactions(frc *fileReactionsConfig) ReactionsConfig {
	d := defaultReactions()
	if frc == nil {
		return d
	}
	d.CIFailed = frc.CIFailed.resolve(d.CIFailed)
	d.ChangesRequested = frc.ChangesRequested.resolve(d.ChangesRequested)
	d.MergeConflict = frc.MergeConflict.resolve(d.MergeConflict)
	d.ApprovedAndGreen = frc.ApprovedAndGreen.resolve(d.ApprovedAndGreen)
	d.Merged = frc.Merged.resolve(d.Merged)
	return d
}

// resolveNotify materializes the [notify] table: nil (absent) yields the full
// defaults; a present table overlays desktop / env / per-priority routing,
// leaving unspecified priorities at their default channels.
func resolveNotify(fnc *fileNotifyConfig) NotifyConfig {
	d := defaultNotify()
	if fnc == nil {
		return d
	}
	if fnc.Desktop != nil {
		d.Desktop = *fnc.Desktop
	}
	if fnc.SlackWebhookEnv != nil {
		d.SlackWebhookEnv = *fnc.SlackWebhookEnv
	}
	for prio, channels := range fnc.Routing {
		d.Routing[prio] = channels
	}
	return d
}

// reactionsFile / notifyFile build the on-disk mirrors for Save. A zero
// (unconfigured) table returns nil so it is omitted entirely; otherwise every
// field is written explicitly so the round-trip is exact and an operator's
// explicit false/0/"" is preserved.

func reactionFile(r Reaction) fileReaction {
	return fileReaction{Auto: &r.Auto, Retries: &r.Retries, Message: &r.Message}
}

func reactionsFile(rc ReactionsConfig) *fileReactionsConfig {
	if rc == (ReactionsConfig{}) {
		return nil
	}
	return &fileReactionsConfig{
		CIFailed:         reactionFile(rc.CIFailed),
		ChangesRequested: reactionFile(rc.ChangesRequested),
		MergeConflict:    reactionFile(rc.MergeConflict),
		ApprovedAndGreen: reactionFile(rc.ApprovedAndGreen),
		Merged:           reactionFile(rc.Merged),
	}
}

func notifyFile(n NotifyConfig) *fileNotifyConfig {
	if !n.Desktop && n.SlackWebhookEnv == "" && len(n.Routing) == 0 {
		return nil
	}
	return &fileNotifyConfig{
		Desktop:         &n.Desktop,
		SlackWebhookEnv: &n.SlackWebhookEnv,
		Routing:         n.Routing,
	}
}
