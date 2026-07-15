package config

// Default lifecycle comment templates for the P4 Linear write-back feature —
// the thing AO's build cannot do: Lola narrates the agent's progress back onto
// the Linear issue. Each is opted into per poll by the matching comment_on_*
// boolean; the state transition itself is driven by the on_*_state_id /
// blocked_label_id fields on Poll.
//
// Like the reaction Default*Message consts (see reactions.go), these are filled
// by plain strings.ReplaceAll — NOT text/template — so an agent-authored PR
// link or a blocked-reason detail can never inject template directives or reach
// an eval surface. Recognized placeholders:
//
//	{{.Session}} — the lola session name/id           (spawn)
//	{{.PR}}      — the PR number/URL                    (pr)
//	{{.Detail}}  — the escalation reason / block detail (blocked)
//
// merged carries no placeholder — it is a bare acknowledgement.
const (
	// DefaultSpawnComment is posted when a session is spawned for the issue.
	DefaultSpawnComment = "🤖 Lola spawned an agent for this issue (session {{.Session}})."
	// DefaultPRComment is posted when the agent opens a PR.
	DefaultPRComment = "🤖 Agent opened a PR: {{.PR}}"
	// DefaultMergedComment is posted when the PR merges.
	DefaultMergedComment = "✅ Merged."
	// DefaultBlockedComment is posted on escalation (agent blocked, needs a human).
	DefaultBlockedComment = "⚠️ Agent blocked — needs a human. {{.Detail}}"
)
