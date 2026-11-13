package goal

import "github.com/sushidev-team/lola/internal/goalpolicy"

// StartOptions is a read-only suggestion from current configuration, not an
// authorization. Checks must be supplied by the operator; no command is inferred.
type StartOptions struct {
	Goal          Overview       `json:"goal"`
	Authorization Authorization  `json:"authorization"`
	Profiles      []StartProfile `json:"profiles"`
}
type StartProfile struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Required  bool     `json:"required"`
	Artifacts []string `json:"artifacts"`
}

func SuggestedLimits(taskCount int) goalpolicy.Limits {
	return goalpolicy.Limits{Concurrency: 1, MaxTasks: taskCount, AttemptsPerTask: 2, ReviewRounds: 2, AgentRuns: 30, RuntimeMinutes: 120, AttemptMinutes: 30, Tokens: 500000}
}
