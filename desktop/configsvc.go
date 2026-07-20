package main

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

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

// ConfigService lets the settings / project / poll forms read and write
// config.toml directly, the same way the TUI does — the daemon protocol has no
// config-write command; config.toml is the single source of truth and the
// daemon only re-reads it on `reload`. Every Save validates, persists atomically
// (config.Save is temp+rename, 0600), then best-effort reloads a live daemon.
//
// Secrets never pass through here: the Linear key and Slack webhook live in the
// keychain / env by *name*, and those name fields are the only secret-adjacent
// values these DTOs carry.
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
	// Best-effort: a live daemon picks the change up; a down daemon is fine.
	_ = call(protocol.Request{Cmd: "reload"}, shortTimeout, nil)
	return nil
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

	ReviewEnabled         bool   `json:"reviewEnabled"`
	ReviewCommand         string `json:"reviewCommand"`
	ReviewOnPROpen        bool   `json:"reviewOnPrOpen"`
	ReviewSendToAgent     bool   `json:"reviewSendToAgent"`
	ReviewCommentOnLinear bool   `json:"reviewCommentOnLinear"`
	ReviewTimeout         int    `json:"reviewTimeout"`

	CrEnabled         bool   `json:"crEnabled"`
	CrAuthor          string `json:"crAuthor"`
	CrNotify          bool   `json:"crNotify"`
	CrSendToAgent     bool   `json:"crSendToAgent"`
	CrCommentOnLinear bool   `json:"crCommentOnLinear"`

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

// PrioritySortKeys returns the sort keys the daemon understands, so the
// settings form can offer them instead of taking free text. These are LOLA's
// own keys, not a Linear concept — there is nothing to fetch from the API.
func (s *ConfigService) PrioritySortKeys() []string {
	return append([]string(nil), config.PrioritySortKeys...)
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
		ReviewEnabled:            cfg.Review.Enabled,
		ReviewCommand:            cfg.Review.Command,
		ReviewOnPROpen:           cfg.Review.OnPROpen,
		ReviewSendToAgent:        cfg.Review.SendToAgent,
		ReviewCommentOnLinear:    cfg.Review.CommentOnLinear,
		ReviewTimeout:            cfg.Review.TimeoutSeconds,
		CrEnabled:                cfg.CodeRabbit.Enabled,
		CrAuthor:                 cfg.CodeRabbit.Author,
		CrNotify:                 cfg.CodeRabbit.Notify,
		CrSendToAgent:            cfg.CodeRabbit.SendToAgent,
		CrCommentOnLinear:        cfg.CodeRabbit.CommentOnLinear,

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
	cfg.Review.Enabled = dto.ReviewEnabled
	cfg.Review.Command = dto.ReviewCommand
	cfg.Review.OnPROpen = dto.ReviewOnPROpen
	cfg.Review.SendToAgent = dto.ReviewSendToAgent
	cfg.Review.CommentOnLinear = dto.ReviewCommentOnLinear
	cfg.Review.TimeoutSeconds = dto.ReviewTimeout
	cfg.CodeRabbit.Enabled = dto.CrEnabled
	cfg.CodeRabbit.Author = dto.CrAuthor
	cfg.CodeRabbit.Notify = dto.CrNotify
	cfg.CodeRabbit.SendToAgent = dto.CrSendToAgent
	cfg.CodeRabbit.CommentOnLinear = dto.CrCommentOnLinear

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
	// Repository / worktree setup.
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	Repo          string   `json:"repo"`
	DefaultBranch string   `json:"defaultBranch"`
	BranchPrefix  string   `json:"branchPrefix"`
	Agent         string   `json:"agent"` // ""=inherit | claude | codex | opencode
	Symlinks      []string `json:"symlinks"`
	PostCreate    []string `json:"postCreate"`
	Env           []string `json:"env"` // "KEY=value" lines

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
		Path:          p.Path,
		Repo:          p.Repo,
		DefaultBranch: p.DefaultBranch,
		BranchPrefix:  p.BranchPrefix,
		Agent:         p.Agent,
		Symlinks:      p.Symlinks,
		PostCreate:    p.PostCreate,
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
	if strings.TrimSpace(dto.Name) == "" {
		return errors.New("project name is required")
	}
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	env, err := linesToEnv(dto.Env)
	if err != nil {
		return err
	}
	p := cfg.ProjectByName(dto.Name)
	if p == nil {
		cfg.Projects = append(cfg.Projects, config.Project{Name: dto.Name})
		p = &cfg.Projects[len(cfg.Projects)-1]
	}
	prioritySort := p.PrioritySort // not exposed by the form; preserved as-is

	p.Path = dto.Path
	p.Repo = dto.Repo
	p.DefaultBranch = dto.DefaultBranch
	p.BranchPrefix = dto.BranchPrefix
	p.Agent = dto.Agent
	p.Symlinks = nonEmpty(dto.Symlinks)
	p.PostCreate = nonEmpty(dto.PostCreate)
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

// DetectRepo resolves the GitHub "owner/name" of the checkout at path so the
// project form can prefill Repo instead of making the user copy it. Returns ""
// when it cannot be determined — not a git repo, no GitHub remote, a
// non-GitHub host. That empty value is deliberate and safe: it disables PR
// checks (fail-closed) rather than pointing them at the wrong repository.
//
// Prefers the "upstream" remote over "origin": in a fork, origin is the fork
// but upstream is where the pull requests actually land.
func (s *ConfigService) DetectRepo(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return gitrepo.Detect(ctx, path)
}

// Branches lists the branches the checkout at path can fork worktrees from —
// local branches plus remote-tracking ones with no local counterpart, the
// repository's own default first. Empty when path is not a checkout; the form
// then leaves the field as free text rather than trapping the user.
func (s *ConfigService) Branches(path string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b := gitrepo.Branches(ctx, path)
	if b == nil {
		return []string{} // marshal as [] rather than null for the frontend
	}
	return b
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
	if err := secrets.StoreLinearAPIKey(setupKeychainService, dto.LinearKey); err == nil {
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
	cfg.Projects = []config.Project{{
		Name:          dto.ProjectName,
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
