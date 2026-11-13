package attention

import (
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
)

// The quota fixtures use the CLIs' verbatim blocking banners (see the QUOTA
// cue block in activity.go for provenance). A quota pane shows the banner
// above a resting composer; the classifier must read it as
// ActivityQuotaLimited, not as an ordinary resting prompt.
func TestClassifyQuotaBanners(t *testing.T) {
	tests := []struct {
		name string
		kind agent.Kind
		in   string
		want Activity
	}{
		{
			name: "claude: session limit banner above the resting caret",
			kind: agent.Claude,
			in:   "⏺ Done with the edits.\n\nYou've hit your limit · resets 8pm (Europe/Berlin)\n\n❯ \n",
			want: ActivityQuotaLimited,
		},
		{
			name: "claude: weekly limit variant",
			kind: agent.Claude,
			in:   "You've hit your session limit · resets 6pm UTC\n❯ \n",
			want: ActivityQuotaLimited,
		},
		{
			name: "claude: out of extra usage",
			kind: agent.Claude,
			in:   "You're out of extra usage · resets Apr 23 at 4pm (America/Sao_Paulo)\n❯ \n",
			want: ActivityQuotaLimited,
		},
		{
			name: "claude: team seat limit",
			kind: agent.Claude,
			in:   "Limit reached – contact an admin to keep working\n❯ \n",
			want: ActivityQuotaLimited,
		},
		{
			name: "claude: API rate limit error line",
			kind: agent.Claude,
			in:   "API Error: Rate limit reached\n❯ \n",
			want: ActivityQuotaLimited,
		},
		{
			name: "claude: usage credits required",
			kind: agent.Claude,
			in:   "API Error: Usage credits required for 1M context · turn on usage credits at claude.ai/settings/usage\n❯ \n",
			want: ActivityQuotaLimited,
		},
		{
			name: "legacy empty kind resolves to the claude vocabulary",
			kind: agent.Parse(""),
			in:   "You've hit your limit · resets 3:00 AM (UTC)\n❯ \n",
			want: ActivityQuotaLimited,
		},
		{
			name: "codex: usage limit with upgrade path",
			kind: agent.Codex,
			in:   "■ You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro) or try again at 3pm.\n",
			want: ActivityQuotaLimited,
		},
		{
			name: "codex: model-specific limit",
			kind: agent.Codex,
			in:   "■ You've hit your usage limit for gpt-5.6 high. Switch to another model now, or try again later.\n",
			want: ActivityQuotaLimited,
		},
		{
			name: "codex: workspace out of credits",
			kind: agent.Codex,
			in:   "■ You're out of credits. Your workspace is out of credits. Add credits to continue using Codex.\n",
			want: ActivityQuotaLimited,
		},
		{
			name: "codex: spend cap",
			kind: agent.Codex,
			in:   "You hit your spend cap set in your workspace. Increase your spend cap to continue.\n",
			want: ActivityQuotaLimited,
		},
		{
			name: "codex: owner-increase modal body",
			kind: agent.Codex,
			in:   "  Usage limit reached\n  Request a limit increase from your owner to continue using codex. Request increase?\n\n  1. Yes (y)\n› 2. No (default) (n)\n",
			want: ActivityQuotaLimited,
		},
		{
			name: "opencode: free tier exhausted",
			kind: agent.OpenCode,
			in:   "▣ Free usage exceeded\n",
			want: ActivityQuotaLimited,
		},
		{
			name: "opencode: subscription quota exhausted",
			kind: agent.OpenCode,
			in:   "Subscription quota exhausted — wait for your quota to reset\n",
			want: ActivityQuotaLimited,
		},
		{
			name: "opencode: provider insufficient_quota error card",
			kind: agent.OpenCode,
			in:   "ERROR service=llm providerID=openai modelID=gpt-5.6 error=insufficient_quota\n",
			want: ActivityQuotaLimited,
		},
		{
			name: "opencode: provider quota sentence",
			kind: agent.OpenCode,
			in:   "You exceeded your current quota, please check your plan and billing details.\n",
			want: ActivityQuotaLimited,
		},
		{
			name: "opencode: claude-via-opencode credit balance",
			kind: agent.OpenCode,
			in:   "Your credit balance is too low to access the Claude API. Please go to Plans & Billing to upgrade or purchase credits.\n",
			want: ActivityQuotaLimited,
		},
		{
			name: "opencode: suspended balance",
			kind: agent.OpenCode,
			in:   "Error from provider (Alibaba): Your account is suspended due to insufficient balance, please recharge your account.\n",
			want: ActivityQuotaLimited,
		},
		{
			name: "opencode: copilot quota exceeded",
			kind: agent.OpenCode,
			in:   "Too Many Requests: quota exceeded\n",
			want: ActivityQuotaLimited,
		},

		// Negatives.
		{
			name: "opencode: transient retry lines are not a quota banner",
			kind: agent.OpenCode,
			in:   "↳ Retrying (attempt 2) · Rate limit exceeded, please try again later\nretrying in 4s - attempt #2\n",
			want: ActivityUnknown,
		},
		{
			name: "claude: prose mentioning rate limits is not a banner",
			kind: agent.Claude,
			in:   "⏺ The handler should back off when the API returns a rate limit error. Let me add that.\n\n❯ \n",
			want: ActivityWaiting,
		},
		{
			name: "codex banner on a claude pane does not fire (per-kind vocabularies)",
			kind: agent.Claude,
			in:   "■ You've hit your usage limit. Upgrade to Pro or try again at 3pm.\n\n❯ \n",
			want: ActivityWaiting,
		},
		{
			name: "claude banner on a codex pane does not fire",
			kind: agent.Codex,
			in:   "You've hit your limit · resets 8pm (Europe/Berlin)\n",
			want: ActivityUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.in, tt.kind); got != tt.want {
				t.Errorf("Classify() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A banner scrolled out of the shallow quota window must not condemn a pane
// that has moved on: only the resting caret remains in the tail.
func TestClassifyQuotaStaleBanner(t *testing.T) {
	var b strings.Builder
	b.WriteString("You've hit your limit · resets 8pm (Europe/Berlin)\n")
	for i := 0; i < quotaTailLines+5; i++ {
		b.WriteString("some later output line\n")
	}
	b.WriteString("❯ \n")
	if got := Classify(b.String(), agent.Claude); got != ActivityWaiting {
		t.Errorf("stale banner: Classify() = %v, want %v", got, ActivityWaiting)
	}
}

// A streaming agent is alive even when a stale banner sits in its tail: the
// live working cue is checked before the quota cue.
func TestClassifyQuotaLosesToLiveWorking(t *testing.T) {
	in := "You've hit your limit · resets 8pm (Europe/Berlin)\n" +
		"✻ Harmonizing… (5m 58s · ↓ 17.9k tokens · esc to interrupt)\n" +
		"❯ \n"
	if got := Classify(in, agent.Claude); got != ActivityWorking {
		t.Errorf("live working with stale banner: Classify() = %v, want %v", got, ActivityWorking)
	}
}
