package tui

// sshsettings.go is the settings form's "SSH hosts" tab: the [[ssh_host]] catalog,
// a list above a form, generated per host like the specialist editor. Lola never
// connects from here. "What it means" asks `ssh -G`, which reads the local config
// and opens no socket.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

const sshAddKey = "ssh.add"

func sshHostKey(serial int, field string) string { return fmt.Sprintf("ssh.%d.%s", serial, field) }

// sshResolvedMsg is what `ssh -G` says an alias means. It is display only.
type sshResolvedMsg struct {
	serial int
	data   protocol.SSHResolveData
	err    error
}

func itoaOrBlank(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func sshAgentOptions() []string {
	return append([]string{""}, agentKindStrings()...)
}

func (f *settingsForm) sshFields(serial int, h config.SSHHost) []setField {
	title := h.DisplayName()
	if title == "" {
		title = "new host"
	}
	num := func(key, label string, v int, help string) setField {
		return setField{key: key, label: label, kind: sfInt, text: itoaOrBlank(v), help: help}
	}
	fields := []setField{
		{key: "alias", label: "SSH alias", kind: sfText, text: h.Alias, subsection: title, help: "An SSH config alias, hostname or IP address."},
		{key: "means", label: "What it means", kind: sfAction, help: "enter reads it from your ssh config. Nothing connects."},
		{key: "label", label: "Label", kind: sfText, text: h.Label, help: "Shown in Lola. Defaults to the name."},
		{key: "name", label: "Name", kind: sfText, text: h.Name, help: "Agents and the CLI use this. Letters, digits, dots, dashes."},
		{key: "user", label: "Remote user", kind: sfText, text: h.User, help: "A restricted user is the real limit on what a command can do. Lola does not filter commands."},
		num("port", "Port", h.Port, "Blank uses SSH settings (normally 22)."),
		{key: "identity_file", label: "Identity file", kind: sfText, text: h.IdentityFile, help: "Optional absolute or ~/ key-file path. Contents stay with SSH."},
		{key: "tier", label: "Tier", kind: sfEnum, text: h.EffectiveTier(), options: config.SSHTiers},
		{key: "sandbox", label: "Direct session agent", kind: sfEnum, text: h.Sandbox, options: []string{"", config.SandboxDocker, config.SandboxNone}},
		{key: "allow_unsandboxed", label: "Allow unsandboxed", kind: sfBool, b: h.AllowUnsandboxed, help: "Production only. Lets an agent that runs as you work here. It could read your keys or talk to Lola directly, so the approval gate would stop accidents and well-behaved agents, not a determined one. Leave off unless you accept that for this host."},
		{key: "agent", label: "Agent", kind: sfEnum, text: h.Agent, options: sshAgentOptions(), help: "The direct session's coding agent. Empty uses [defaults].agent."},
		{key: "default_cwd", label: "Default directory", kind: sfText, text: h.DefaultCwd, help: "Absolute path on the host. Commands that name none run here."},
		{key: "grant_max", label: "Longest grant (min)", kind: sfText, text: itoaOrBlank(h.GrantMaxMinutes), help: "Blank uses the tier's default: staging 30, dev 120. -1 means no grants. Must stay blank on production, which approves every command."},
		num("timeout", "Command timeout (s)", h.CommandTimeoutSeconds, "When a command names none. Blank means 120."),
		num("max_timeout", "Longest timeout (s)", h.MaxCommandTimeoutSeconds, "The most a command may ask for. Blank means 900."),
		num("output_kb", "Output kept (KB)", h.OutputCapKB, "Per stream of one command. Past it the head and the last 64 KB are kept. Blank means 1024."),
		num("persist", "Idle linger (s)", h.ControlPersistSeconds, "How long the connection stays up with no client. Blank means 900."),
		num("connect_wait", "Sign-in wait (s)", h.ConnectWaitSeconds, "Time for passphrase and MFA. Blank means 300."),
		num("approval_wait", "Approval wait (s)", h.ApprovalTimeoutSeconds, "Then a pending command expires, unrun. Blank means 1800."),
		{key: "remove", label: "Remove host", kind: sfAction, help: "Y confirms. Its live connections close when you save, and agents can no longer ask for it. " + protocol.SSHRemoteOutlives},
	}
	for i := range fields {
		fields[i].key = sshHostKey(serial, fields[i].key)
		fields[i].tab = stSSH
		fields[i].indent = true
	}
	return fields
}

// initSSHHosts generates the tab: one block per host, then Add host.
func (f *settingsForm) initSSHHosts() {
	f.fields = append(f.fields, setField{key: sshAddKey, tab: stSSH, label: "Add host", kind: sfAction, help: "Adds a host to this draft. Ctrl-s saves; esc can discard all changes."})
	for _, h := range f.cfg.SSHHosts {
		f.appendSSHHost(h)
	}
	f.sshHeader()
}

// sshHeader puts the tab's one standing sentence on whichever field is first. A
// row of its own would be an "[enter]" that does nothing.
func (f *settingsForm) sshHeader() {
	seen := false
	for i := range f.fields {
		if f.fields[i].tab != stSSH {
			continue
		}
		f.fields[i].section, f.fields[i].sectionNote = "", ""
		if !seen {
			f.fields[i].section = "[[ssh_host]]"
			f.fields[i].sectionNote = "Choose an SSH alias or hostname. Key contents stay with SSH. Nothing connects from here."
			seen = true
		}
	}
}

func (f *settingsForm) appendSSHHost(h config.SSHHost) {
	serial := f.nextSSHHost
	f.nextSSHHost++
	f.sshKeys = append(f.sshKeys, serial)
	fields := f.sshFields(serial, h)
	at := slices.IndexFunc(f.fields, func(fd setField) bool { return fd.key == sshAddKey })
	if at < 0 {
		f.fields = append(f.fields, fields...)
		return
	}
	f.fields = slices.Insert(f.fields, at, fields...)
}

func (f *settingsForm) focusSSH(key string) {
	f.tab = stSSH
	for vi, i := range f.visible() {
		if f.fields[i].key == key {
			f.cursor = vi
			return
		}
	}
}

// sshSerialOf is the host a field key belongs to, -1 for the tab's own rows.
func sshSerialOf(key string) int {
	rest, ok := strings.CutPrefix(key, "ssh.")
	if !ok {
		return -1
	}
	head, _, ok := strings.Cut(rest, ".")
	if !ok {
		return -1
	}
	n, err := strconv.Atoi(head)
	if err != nil {
		return -1
	}
	return n
}

// sshAction answers enter on one of the tab's action rows.
func (f *settingsForm) sshAction(key string) tea.Cmd {
	if key == sshAddKey {
		f.appendSSHHost(config.SSHHost{Tier: config.SSHTierProduction})
		f.sshHeader()
		f.focusSSH(sshHostKey(f.sshKeys[len(f.sshKeys)-1], "alias"))
		return nil
	}
	serial := sshSerialOf(key)
	if serial < 0 {
		return nil
	}
	switch key {
	case sshHostKey(serial, "remove"):
		f.sshRemoving = serial
		return nil
	case sshHostKey(serial, "means"):
		alias := strings.TrimSpace(f.field(sshHostKey(serial, "alias")).text)
		h := f.sshHostFromFields(serial)
		if alias == "" {
			f.sshSetMeans(serial, "Type an alias first.")
			return nil
		}
		f.sshSetMeans(serial, "Reading your ssh config…")
		return func() tea.Msg {
			raw, err := sshCall(protocol.CmdSSHResolve, protocol.SSHResolveArgs{Alias: alias, User: h.User, Port: h.Port, IdentityFile: h.IdentityFile})
			if err != nil {
				return sshResolvedMsg{serial: serial, err: err}
			}
			var d protocol.SSHResolveData
			if err := json.Unmarshal(raw, &d); err != nil {
				return sshResolvedMsg{serial: serial, err: err}
			}
			return sshResolvedMsg{serial: serial, data: d}
		}
	}
	return nil
}

// sshSetMeans shows an answer as the row's help. It is deliberately not a field
// value, so it can never reach config.toml or read as an unsaved edit.
func (f *settingsForm) sshSetMeans(serial int, text string) {
	if fd := f.field(sshHostKey(serial, "means")); fd != nil {
		fd.help = text
	}
}

func (f *settingsForm) sshApplyResolved(v sshResolvedMsg) {
	switch {
	case v.err != nil:
		f.sshSetMeans(v.serial, "ssh could not read this alias: "+sshLine(v.err.Error()))
	case v.data.Error != "":
		f.sshSetMeans(v.serial, "ssh could not read this alias: "+sshLine(v.data.Error))
	default:
		text := fmt.Sprintf("Connects to %s@%s", sshLine(v.data.User), sshLine(v.data.Hostname))
		if v.data.Port != "" {
			text += ":" + sshLine(v.data.Port)
		}
		if v.data.ProxyJump != "" {
			text += " through " + sshLine(v.data.ProxyJump)
		}
		f.sshSetMeans(v.serial, text+". Read from your ssh config. Nothing connected.")
	}
}

func (f *settingsForm) removeSSHHost(serial int) {
	prefix := sshHostKey(serial, "")
	f.fields = slices.DeleteFunc(f.fields, func(fd setField) bool { return strings.HasPrefix(fd.key, prefix) })
	f.sshKeys = slices.DeleteFunc(f.sshKeys, func(k int) bool { return k == serial })
	f.sshHeader()
	f.focusSSH(sshAddKey)
}

// sshRefreshHelp makes the fields whose advice depends on the tier say what is
// true right now. Called when the footer is drawn, so it follows every keystroke.
func (f *settingsForm) sshRefreshHelp() {
	fld := f.cur()
	serial := sshSerialOf(fld.key)
	if fld.tab != stSSH || serial < 0 {
		return
	}
	tier := f.field(sshHostKey(serial, "tier")).text
	switch fld.key {
	case sshHostKey(serial, "tier"):
		switch tier {
		case config.SSHTierStaging:
			fld.help = "Every command asks, unless you open a grant, up to 30 minutes."
		case config.SSHTierDev:
			fld.help = "Every command asks, unless you open a grant, up to 2 hours."
		default:
			fld.help = "Every command asks. No grants. Refuses agents that are not sandboxed."
		}
	case sshHostKey(serial, "user"):
		fld.help = "A restricted user is the real limit on what a command can do. Lola does not filter commands."
		if tier == config.SSHTierProduction && strings.TrimSpace(fld.text) == "" {
			fld.help = "Without a restricted user, commands run as whoever your ssh config connects as."
		}
	case sshHostKey(serial, "sandbox"):
		fld.help = f.sshSandboxHelp(serial)
	}
}

// sshSandboxHelp reads what the choice resolves to, through the one function the
// daemon also uses, so the form cannot promise something a session will not do.
func (f *settingsForm) sshSandboxHelp(serial int) string {
	h := f.sshHostFromFields(serial)
	how, err := f.cfg.SSHDirectSandbox(h)
	switch {
	case err != nil:
		return "Cannot start: " + err.Error()
	case how == config.SandboxDocker:
		return "Empty uses the tier's default. Resolves to: in a container (lola's sandbox image unless [sandbox].image names one)."
	}
	return "Empty uses the tier's default. Resolves to: on this Mac, as you."
}

// sshHostFromFields reads one block, without validating. Numbers that do not parse
// read as 0 here; buildSSHHosts is what reports them.
func (f *settingsForm) sshHostFromFields(serial int) config.SSHHost {
	text := func(field string) string { return strings.TrimSpace(f.field(sshHostKey(serial, field)).text) }
	n := func(field string) int {
		v, _ := strconv.Atoi(text(field))
		return v
	}
	return config.SSHHost{
		Port: n("port"), IdentityFile: text("identity_file"),
		Alias: text("alias"), Label: text("label"), Name: text("name"), User: text("user"),
		Tier: text("tier"), Sandbox: text("sandbox"), Agent: text("agent"), DefaultCwd: text("default_cwd"),
		AllowUnsandboxed:         f.field(sshHostKey(serial, "allow_unsandboxed")).b,
		GrantMaxMinutes:          n("grant_max"),
		CommandTimeoutSeconds:    n("timeout"),
		MaxCommandTimeoutSeconds: n("max_timeout"),
		OutputCapKB:              n("output_kb"),
		ControlPersistSeconds:    n("persist"),
		ConnectWaitSeconds:       n("connect_wait"),
		ApprovalTimeoutSeconds:   n("approval_wait"),
	}
}

// buildSSHHosts reads the whole tab for save. A blank alias with nothing else
// typed is a host added by mistake and is dropped rather than reported.
func (f *settingsForm) buildSSHHosts() ([]config.SSHHost, error) {
	hosts := make([]config.SSHHost, 0, len(f.sshKeys))
	for _, serial := range f.sshKeys {
		if g := strings.TrimSpace(f.field(sshHostKey(serial, "grant_max")).text); g != "" {
			if _, err := strconv.Atoi(g); err != nil {
				return nil, fmt.Errorf("ssh host %s: longest grant must be a number", f.sshHostFromFields(serial).ID())
			}
		}
		h := f.sshHostFromFields(serial)
		if h.Alias == "" && h.Label == "" && h.Name == "" && h.User == "" && h.Port == 0 && h.IdentityFile == "" {
			continue
		}
		hosts = append(hosts, h)
	}
	if len(hosts) == 0 {
		return nil, nil
	}
	return hosts, nil
}
