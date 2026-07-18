package tui

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/protocol"
)

// openManualCmd creates a new branch + worktree (cmd=openManual): with useAgent
// it launches the coding agent (seeded with prompt), else a plain shell. base ""
// branches off the project's default branch. Reuses openDoneMsg for the outcome.
func openManualCmd(project, branch, base string, useAgent bool, prompt string) tea.Cmd {
	return func() tea.Msg {
		args, _ := json.Marshal(protocol.OpenManualArgs{Project: project, Branch: branch, Base: base, Agent: useAgent, Prompt: prompt})
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

// openPrCmd opens a PR's head branch as a tracking worktree with the coding
// agent (cmd=openPr, the "agent on PR" upgrade). The daemon refuses a fork.
func openPrCmd(project, branch string, number int, isFork bool) tea.Cmd {
	return func() tea.Msg {
		args, _ := json.Marshal(protocol.OpenPrArgs{Project: project, Branch: branch, Number: number, IsFork: isFork})
		resp, err := requestFn(protocol.Request{Cmd: "openPr", Args: args})
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
		return openDoneMsg{msg: "opened PR " + branch, ok: true}
	}
}

// openTicketCmd starts a Linear issue on demand (cmd=openTicket): a worktree +
// agent, deduped like a poll dispatch. Reuses openDoneMsg for the outcome.
func openTicketCmd(project string, is protocol.TicketRow) tea.Cmd {
	return func() tea.Msg {
		args, _ := json.Marshal(protocol.OpenTicketArgs{Project: project, Identifier: is.Identifier, UUID: is.UUID, Branch: is.Branch, Title: is.Title})
		resp, err := requestFn(protocol.Request{Cmd: "openTicket", Args: args})
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
		return openDoneMsg{msg: "started " + is.Identifier, ok: true}
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
