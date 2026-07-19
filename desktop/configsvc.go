package main

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
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
	return saveConfig(cfg, path)
}

// --- project editor ---------------------------------------------------------

type ProjectFormDTO struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	Repo          string   `json:"repo"`
	DefaultBranch string   `json:"defaultBranch"`
	Agent         string   `json:"agent"` // ""=inherit | claude | codex | opencode
	Symlinks      []string `json:"symlinks"`
	PostCreate    []string `json:"postCreate"`
	Env           []string `json:"env"` // "KEY=value" lines
	IsNew         bool     `json:"isNew"`
}

func (s *ConfigService) GetProject(name string) (ProjectFormDTO, error) {
	if name == "" {
		return ProjectFormDTO{IsNew: true}, nil
	}
	cfg, _, err := loadConfig()
	if err != nil {
		return ProjectFormDTO{}, err
	}
	p := cfg.ProjectByName(name)
	if p == nil {
		return ProjectFormDTO{}, errors.New("no such project: " + name)
	}
	return ProjectFormDTO{
		Name:          p.Name,
		Path:          p.Path,
		Repo:          p.Repo,
		DefaultBranch: p.DefaultBranch,
		Agent:         p.Agent,
		Symlinks:      p.Symlinks,
		PostCreate:    p.PostCreate,
		Env:           envToLines(p.Env),
	}, nil
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
	p.Path = dto.Path
	p.Repo = dto.Repo
	p.DefaultBranch = dto.DefaultBranch
	p.Agent = dto.Agent
	p.Symlinks = nonEmpty(dto.Symlinks)
	p.PostCreate = nonEmpty(dto.PostCreate)
	p.Env = env
	return saveConfig(cfg, path)
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

// --- poll editor (raw fields; Linear IDs entered directly) ------------------

type PollFormDTO struct {
	Project        string   `json:"project"`
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

	OnSpawnStateID   string `json:"onSpawnStateId"`
	OnPRStateID      string `json:"onPrStateId"`
	OnMergedStateID  string `json:"onMergedStateId"`
	BlockedLabelID   string `json:"blockedLabelId"`
	CommentOnSpawn   bool   `json:"commentOnSpawn"`
	CommentOnPR      bool   `json:"commentOnPr"`
	CommentOnMerged  bool   `json:"commentOnMerged"`
	CommentOnBlocked bool   `json:"commentOnBlocked"`
	PRRequiresChecks bool   `json:"prRequiresChecks"`
}

func (s *ConfigService) GetPoll(project string) (PollFormDTO, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return PollFormDTO{}, err
	}
	p := cfg.ProjectByName(project)
	if p == nil {
		return PollFormDTO{Project: project}, nil
	}
	return PollFormDTO{
		Project:          p.Name,
		Enabled:          p.Enabled,
		TeamID:           p.TeamID,
		ProjectID:        p.ProjectID,
		CycleMode:        p.CycleMode,
		CycleID:          p.CycleID,
		StateIDs:         p.StateIDs,
		MatchLabels:      p.MatchLabels,
		MatchMode:        p.MatchMode,
		AssigneeMode:     p.AssigneeMode,
		AssigneeUserID:   p.AssigneeUserID,
		ConcurrencyCap:   p.ConcurrencyCap,
		DedupMode:        p.DedupMode,
		OnSentSetLabel:   p.OnSentSetLabel,
		OnSpawnStateID:   p.OnSpawnStateID,
		OnPRStateID:      p.OnPRStateID,
		OnMergedStateID:  p.OnMergedStateID,
		BlockedLabelID:   p.BlockedLabelID,
		CommentOnSpawn:   p.CommentOnSpawn,
		CommentOnPR:      p.CommentOnPR,
		CommentOnMerged:  p.CommentOnMerged,
		CommentOnBlocked: p.CommentOnBlocked,
		PRRequiresChecks: p.PRRequiresChecks,
	}, nil
}

func (s *ConfigService) SavePoll(dto PollFormDTO) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	p := cfg.ProjectByName(dto.Project)
	if p == nil {
		return errors.New("no such project: " + dto.Project)
	}
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
	p.OnSpawnStateID = dto.OnSpawnStateID
	p.OnPRStateID = dto.OnPRStateID
	p.OnMergedStateID = dto.OnMergedStateID
	p.BlockedLabelID = dto.BlockedLabelID
	p.CommentOnSpawn = dto.CommentOnSpawn
	p.CommentOnPR = dto.CommentOnPR
	p.CommentOnMerged = dto.CommentOnMerged
	p.CommentOnBlocked = dto.CommentOnBlocked
	p.PRRequiresChecks = dto.PRRequiresChecks
	return saveConfig(cfg, path)
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
