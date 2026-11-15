package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// The `talk:<id>` topic. The pane has no change signal, so a session watched
// for talk is probed on a schedule: exactly the desktop's poll
// (handleTalkState), with all its effects, so a watch counts as a viewer. The
// probe never types, never calls typeToAgent, sendKeys, agentChoose or any
// lock*Prompt, and moves no field the pane did not prove.

const (
	talkProbeActive  = time.Second
	talkProbeMinGap  = talkProbeActive // a hook burst cannot raise the rate
	talkProbeResting = 15 * time.Second
	talkProbeTimeout = 5 * time.Second
)

type talkProducer struct {
	h    *watchHub
	id   string
	errs int // consecutive pane read failures
}

func (*talkProducer) timing() timing         { return timing{} } // cadence comes from build's again
func (*talkProducer) timeout() time.Duration { return talkProbeTimeout }
func (*talkProducer) replace() bool          { return true }
func (tp *talkProducer) build(ctx context.Context, _ *topicState, _, _ bool, _ string) topicOut {
	s, live := tp.h.d.sessions.Get(tp.id)
	if !live {
		// Gone without an ended record's help: say so once, then nothing.
		return talkValue(protocol.TalkStateData{
			Session: tp.id, Composer: protocol.ComposerGone, Health: protocol.FeedUnsupported,
		}, 0)
	}
	data, err := tp.h.talkProbe(ctx, tp.id)
	if err != nil {
		return topicOut{again: talkProbeResting}
	}
	if tp.h.d.talkReadErr(tp.id) {
		tp.errs = min(tp.errs+1, 6)
	} else {
		tp.errs = 0
	}
	// The probe advanced the feed reader; a feed watcher publishes what moved.
	tp.h.PokeSession(tp.id, pokeFeed)
	return talkValue(data, probeDelay(s, data.Composer, tp.errs))
}

// talkValue wraps a TalkStateData. The fingerprint excludes FeedSeq: a push
// client learns about feed movement from feed:<id>.
func talkValue(data protocol.TalkStateData, again time.Duration) topicOut {
	fpData := data
	fpData.FeedSeq = 0
	b, _ := json.Marshal(fpData)
	return topicOut{
		value: protocol.SizeOf(&protocol.TalkEvent{TalkStateData: data}),
		fp:    fingerprint(b),
		again: again,
	}
}

// probeDelay is the next probe of a live session, decided from its flags BEFORE
// the composer word (spec §7.3): 0 parks it (poke only).
func probeDelay(s session.Session, composer string, errs int) time.Duration {
	switch {
	case s.IsAgentless(), s.GoalManaged(), !sshAgentAlive(s):
		return 0
	case errs > 0:
		return min(talkProbeActive<<errs, talkProbeResting)
	}
	switch composer {
	case protocol.ComposerWorking, protocol.ComposerPrompt, protocol.ComposerModal,
		protocol.ComposerDraft, protocol.ComposerUnknown:
		return talkProbeActive
	}
	if s.AgentState == state.AgentWorking || s.AgentState == state.AgentWaitingInput {
		return talkProbeActive
	}
	return talkProbeResting
}
