package tui

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/protocol"
)

// openManualCmd creates a new branch + worktree with a plain shell (cmd=
// openManual). base "" branches off the project's default branch. It reuses
// openDoneMsg so the outcome flashes like a manual PR open.
func openManualCmd(project, branch, base string) tea.Cmd {
	return func() tea.Msg {
		args, _ := json.Marshal(protocol.OpenManualArgs{Project: project, Branch: branch, Base: base})
		resp, err := requestFn(protocol.Request{Cmd: "openManual", Args: args})
		if err != nil {
			return openDoneMsg{msg: err.Error()}
		}
		if !resp.OK {
			return openDoneMsg{msg: resp.Error}
		}
		var d protocol.OpenData
		if err := json.Unmarshal(resp.Data, &d); err == nil && d.Message != "" {
			return openDoneMsg{msg: d.Message, ok: true}
		}
		return openDoneMsg{msg: "created " + branch, ok: true}
	}
}

// openURLCmd opens a URL in the browser on the daemon side (cmd=openURL), so the
// client stays exec-free. Best-effort: the outcome rides openDoneMsg.
func openURLCmd(url string) tea.Cmd {
	return func() tea.Msg {
		args, _ := json.Marshal(protocol.OpenURLArgs{URL: url})
		resp, err := requestFn(protocol.Request{Cmd: "openURL", Args: args})
		if err != nil {
			return openDoneMsg{msg: "open URL: " + err.Error()}
		}
		if !resp.OK {
			return openDoneMsg{msg: "open URL: " + resp.Error}
		}
		return openDoneMsg{msg: "opened in browser", ok: true}
	}
}
