package remote

import (
	"encoding/json"
	"errors"
	"reflect"

	"github.com/sushidev-team/lola/internal/protocol"
)

// flat is a bit per protocol.Request flat field a command may carry.
type flat uint32

const (
	fSession flat = 1 << iota
	fText
	fProject
	fRef
	fPoll
	fDryRun
	fProvider
	fKeepWorktree
	fCols
	fRows
	fLines
	fArgs
)

// Never carried for any command: Force, Event, Detail, Hook, HookToken. They are
// absent from every mask, so rebuild cannot copy them.

// flatNames maps each bit to the Request JSON field it carries (audit names and
// the drift test use it).
var flatNames = map[flat]string{
	fSession: "session", fText: "text", fProject: "project", fRef: "ref", fPoll: "poll",
	fDryRun: "dryRun", fProvider: "provider", fKeepWorktree: "keepWorktree",
	fCols: "cols", fRows: "rows", fLines: "lines", fArgs: "args",
}

// fieldSpec is everything one remotely reachable command may carry.
type fieldSpec struct {
	fields flat
	args   reflect.Type // the handler's Args type; nil when fields has no fArgs
	// check runs on the REBUILT request and may rewrite its Args. An error is
	// answered with its fixed sentence (CodeDenied); it never echoes the payload.
	check func(req *protocol.Request, has func(tier string) bool) (checkResult, error)
	audit []string // flat or Args JSON field names the audit line may name
}

// checkResult is what a check reports besides accepting.
type checkResult struct {
	dropped     []string // Args/flat fields removed, named in the audit line
	attachBytes int      // attachImage only: len(Data) of this part
	attachLast  bool     // attachImage only
}

// Fixed refusal sentences. None is built from input.
var (
	errNoFieldSpec   = errors.New("command is not available remotely")
	errBadArgs       = errors.New("malformed arguments")
	errOpenLocation  = errors.New("only a new worktree can be opened from a phone")
	errOpenShellTier = errors.New("opening a shell needs the shell permission")
	errAttachPart    = errors.New("an image part must carry 1 to 512 KiB")
)

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

// rebuild returns a FRESH Request: Cmd from the envelope, only the flat fields the
// command's spec lists copied from in, Args copied verbatim when fArgs, then the
// spec's check run. Everything else the phone sent is discarded, so a handler that
// later starts reading a new field reads nothing a phone chose until a human adds
// the field here. A command with no spec is an error.
func rebuild(cmd string, in protocol.Request, has func(tier string) bool) (protocol.Request, checkResult, error) {
	spec, ok := requestFields[cmd]
	if !ok {
		return protocol.Request{}, checkResult{}, errNoFieldSpec
	}
	out := protocol.Request{Cmd: cmd}
	if spec.fields&fSession != 0 {
		out.Session = in.Session
	}
	if spec.fields&fText != 0 {
		out.Text = in.Text
	}
	if spec.fields&fProject != 0 {
		out.Project = in.Project
	}
	if spec.fields&fRef != 0 {
		out.Ref = in.Ref
	}
	if spec.fields&fPoll != 0 {
		out.Poll = in.Poll
	}
	if spec.fields&fDryRun != 0 {
		out.DryRun = in.DryRun
	}
	if spec.fields&fProvider != 0 {
		out.Provider = in.Provider
	}
	if spec.fields&fKeepWorktree != 0 {
		out.KeepWorktree = in.KeepWorktree
	}
	if spec.fields&fCols != 0 {
		out.Cols = in.Cols
	}
	if spec.fields&fRows != 0 {
		out.Rows = in.Rows
	}
	if spec.fields&fLines != 0 {
		out.Lines = in.Lines
	}
	if spec.fields&fArgs != 0 && len(in.Args) > 0 {
		out.Args = append(json.RawMessage(nil), in.Args...)
	}
	var res checkResult
	if spec.check != nil {
		var err error
		if res, err = spec.check(&out, has); err != nil {
			return protocol.Request{}, checkResult{}, err
		}
	}
	return out, res, nil
}

// decodeArgs unmarshals req.Args into v; absent Args decode to the zero value.
func decodeArgs(req *protocol.Request, v any) error {
	if len(req.Args) == 0 {
		return nil
	}
	if err := json.Unmarshal(req.Args, v); err != nil {
		return errBadArgs
	}
	return nil
}

func encodeArgs(req *protocol.Request, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return errBadArgs
	}
	req.Args = b
	return nil
}

func checkPrs(req *protocol.Request, _ func(string) bool) (checkResult, error) {
	var a protocol.PrsArgs
	if err := decodeArgs(req, &a); err != nil {
		return checkResult{}, err
	}
	var res checkResult
	if a.Refresh {
		res.dropped = []string{"refresh"}
		a.Refresh = false
	}
	return res, encodeArgs(req, a)
}

func checkAgentSay(req *protocol.Request, _ func(string) bool) (checkResult, error) {
	var a protocol.AgentSayArgs
	if err := decodeArgs(req, &a); err != nil {
		return checkResult{}, err
	}
	var res checkResult
	if a.Conn != "" {
		res.dropped = []string{"conn"}
		a.Conn = ""
	}
	return res, encodeArgs(req, a)
}

func checkOpenManual(req *protocol.Request, has func(string) bool) (checkResult, error) {
	var a protocol.OpenManualArgs
	if err := decodeArgs(req, &a); err != nil {
		return checkResult{}, err
	}
	if (a.Location != "" && a.Location != "worktree") || a.Path != "" {
		return checkResult{}, errOpenLocation
	}
	if !a.Agent && !has(protocol.TierShell) {
		return checkResult{}, errOpenShellTier
	}
	return checkResult{}, nil
}

// checkAttachImage refuses a part outside 1..AttachChunkBytes here, before any
// bucket: an empty part would cost nothing in the byte bucket and reach the
// handler (and the audit trail) for free.
func checkAttachImage(req *protocol.Request, _ func(string) bool) (checkResult, error) {
	var a protocol.AttachImageArgs
	if err := decodeArgs(req, &a); err != nil {
		return checkResult{}, err
	}
	if len(a.Data) == 0 || len(a.Data) > protocol.AttachChunkBytes {
		return checkResult{}, errAttachPart
	}
	return checkResult{attachBytes: len(a.Data), attachLast: a.Last}, nil
}

// requestFields has one entry per command whose tier is not never, transport
// commands included (TestEveryTieredCommandHasAFieldSpec).
var requestFields = buildRequestFields()

func buildRequestFields() map[string]fieldSpec {
	m := map[string]fieldSpec{}
	put := func(spec fieldSpec, cmds ...string) {
		for _, c := range cmds {
			if _, dup := m[c]; dup {
				panic("remote: field spec declared twice: " + c)
			}
			m[c] = spec
		}
	}
	put(fieldSpec{}, "status", "sessions", "projects", protocol.CmdHubStatus, protocol.CmdEndedSessions)
	put(fieldSpec{fields: fSession},
		"panes", "revive", "coderabbit", "resolveConflict", "diff", "checkpoints", "closeExitedTemporary",
		protocol.CmdReviewPackages)
	put(fieldSpec{fields: fSession | fLines}, "pane")
	put(fieldSpec{fields: fSession | fText}, "answer", "renameSession")
	put(fieldSpec{fields: fSession | fProvider}, "review")
	put(fieldSpec{fields: fSession | fKeepWorktree, audit: []string{"keepWorktree"}}, "kill")
	put(fieldSpec{fields: fSession | fCols | fRows}, "shellCreate")
	put(fieldSpec{fields: fProject | fRef}, "open")
	put(fieldSpec{fields: fPoll}, "enable", "disable")
	put(fieldSpec{fields: fPoll | fDryRun}, "pollOnce")

	put(fieldSpec{fields: fSession | fArgs, args: typeOf[protocol.PRReadinessArgs]()}, "prReadiness", "addressPRFeedback")
	put(fieldSpec{fields: fSession | fArgs, args: typeOf[protocol.PRReadinessArgs](),
		audit: []string{"method", "confirmUnready"}}, "mergePR")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.PrsArgs](), check: checkPrs}, "prs")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.TicketsArgs]()}, "tickets")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.OpenPrArgs]()}, "openPr")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.OpenTicketArgs]()}, "openTicket")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.OpenManualArgs](), check: checkOpenManual,
		audit: []string{"agent"}}, "openManual", "openExisting")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.BestOfArgs]()}, "bestOf")
	// BestOfPickArgs names the picked attempt as `session`; the plan's "winner" is not a field.
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.BestOfPickArgs](), audit: []string{"session"}}, "bestOfPick")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.BestOfCompareArgs]()}, "bestOfCompare")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.FeedbackArgs]()}, "feedback")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.BroadcastArgs]()}, "broadcast")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.CheckpointArgs]()},
		"checkpointDiff", "restoreCheckpoint", "forkCheckpoint")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.SwitchAgentArgs]()}, "switchAgent")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.DevArgs](), audit: []string{"on"}}, "dev")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.DevFreePortArgs]()}, "devFreePort")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.PaneCloseArgs]()}, "paneClose")
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.PaneResizeArgs]()}, "paneResize")
	put(fieldSpec{fields: fSession | fArgs, args: typeOf[protocol.ReportArgs]()}, "sessionTask")
	// The human plan verdict: a session id plus approve/comment. Approving unlocks
	// coding, so it needs the control tier; the daemon re-validates the state.
	put(fieldSpec{fields: fSession | fArgs, args: typeOf[protocol.PlanDecideArgs](), audit: []string{"approve"}}, "planDecide")

	put(fieldSpec{fields: fArgs, args: typeOf[protocol.AgentFeedArgs]()}, protocol.CmdAgentFeed)
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.AgentFeedOutputArgs]()}, protocol.CmdAgentFeedOutput)
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.TalkStateArgs]()}, protocol.CmdTalkState)
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.AgentChooseArgs]()}, protocol.CmdAgentChoose)
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.AgentStopArgs]()}, protocol.CmdAgentStop)
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.AgentQueueReleaseArgs]()}, protocol.CmdAgentQueueRelease)
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.AgentSettingArgs]()}, protocol.CmdAgentSetting)
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.AgentSayArgs](), check: checkAgentSay,
		audit: []string{"stopFirst"}}, protocol.CmdAgentSay)
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.SSHRecallArgs]()}, protocol.CmdSSHRecall)
	// The element-Conn rule lives in the daemon's handleRemote: remote cannot see the element.
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.SSHAnswerArgs]()}, protocol.CmdSSHAnswer)

	put(fieldSpec{fields: fArgs, args: typeOf[protocol.ProjectGitArgs](), audit: []string{"op", "project"}},
		protocol.CmdProjectGit, protocol.CmdProjectGitAct, protocol.CmdProjectGitDiscard)
	put(fieldSpec{fields: fSession | fArgs, args: typeOf[protocol.ReviewDiffArgs]()}, protocol.CmdReviewDiff)
	put(fieldSpec{fields: fSession | fArgs, args: typeOf[protocol.ReviewImageArgs]()}, protocol.CmdReviewImage)
	put(fieldSpec{fields: fSession | fArgs, args: typeOf[protocol.ReviewMarkArgs]()}, protocol.CmdReviewMark)
	put(fieldSpec{fields: fSession | fArgs, args: typeOf[protocol.ReviewVerifyArgs]()},
		protocol.CmdReviewChecks, protocol.CmdReviewRecover, protocol.CmdReviewVerify)
	put(fieldSpec{fields: fSession | fArgs, args: typeOf[protocol.ReviewCaptureArgs]()}, protocol.CmdReviewCapture)
	put(fieldSpec{fields: fSession | fArgs, args: typeOf[protocol.ReviewFeedbackArgs]()}, protocol.CmdReviewFeedback)

	put(fieldSpec{fields: fArgs, args: typeOf[protocol.SessionTimelineArgs]()}, protocol.CmdSessionTimeline)
	// Self-only by construction (the daemon reads the device from the peer); the
	// handler writes its own audit line.
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.RelayCredArgs]()}, protocol.CmdRelayCred)

	put(fieldSpec{fields: fArgs, args: typeOf[protocol.WatchArgs]()}, protocol.CmdWatch)
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.UnwatchArgs]()}, protocol.CmdUnwatch)
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.AttachImageArgs](), check: checkAttachImage,
		audit: []string{"mime", "part"}}, protocol.CmdAttachImage)
	put(fieldSpec{fields: fArgs, args: typeOf[protocol.AttachmentImageArgs]()}, protocol.CmdAttachmentImage)
	return m
}
