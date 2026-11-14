package doctor

// FixID names the one action that resolves a failed Result, so a UI can put
// a button on the row instead of parsing its Detail. The values are a STABLE
// string enum (UIs key off them); "" means no automated fix. A fix id names
// an action, never a value: no secret, path or token is ever carried here.
type FixID string

const (
	FixNone FixID = ""
	// FixStartDaemon: start the daemon (`lola run`).
	FixStartDaemon FixID = "start_daemon"
	// FixInstallGh: install the GitHub CLI.
	FixInstallGh FixID = "install_gh"
	// FixGhLogin: `gh auth login`.
	FixGhLogin FixID = "gh_auth_login"
	// FixAddProject: add a [[project]].
	FixAddProject FixID = "add_project"
	// FixEnablePoll: give a project a Linear team and enable its poll.
	FixEnablePoll FixID = "enable_poll"
	// FixJoinHub: join a hub (`lola hub join`).
	FixJoinHub FixID = "hub_join"
	// FixRejoinHub: leave and join again (key mismatch, revoked device).
	FixRejoinHub FixID = "hub_rejoin"
	// FixHubChores: opt a project in with hub_chores (and give it a repo).
	FixHubChores FixID = "hub_chores"
	// FixSandboxProject: set sandbox = "docker" on a chores project.
	FixSandboxProject FixID = "sandbox_project"
	// FixStartDocker: install or start Docker (or set [sandbox].docker).
	FixStartDocker FixID = "start_docker"
	// FixSandboxToken: `claude setup-token`, then `lola sandbox token`.
	FixSandboxToken FixID = "sandbox_token"
	// FixPinPolicyKey: pin the team's policy key in [hub].trusted_policy_keys.
	FixPinPolicyKey FixID = "pin_policy_key"
)

// AllFixIDs lists every non-empty FixID, for UIs and the parity tests.
func AllFixIDs() []FixID {
	return []FixID{
		FixStartDaemon, FixInstallGh, FixGhLogin, FixAddProject, FixEnablePoll,
		FixJoinHub, FixRejoinHub, FixHubChores, FixSandboxProject, FixStartDocker,
		FixSandboxToken, FixPinPolicyKey,
	}
}
