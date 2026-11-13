package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/protocol"
)

// The agent-assisted SSH surface's vocabulary: its palette-derived styles, and
// the small pure helpers (tier words, agent names, durations, safe text) every
// ssh*.go file shares. docs/ssh-sessions-ux.md section 17 is the visual spec.
//
// The whole design is three VOICES that must stay distinguishable without a
// font: Lola's facts sit on a colSel band behind a status marker, the agent's
// claims are bare text behind a hollow ring, and the human's words carry an
// accent ▌. Only Lola's voice may ever draw a band or a ✓.

// Palette-derived styles. Registered in rebuildStyles: a package-level style
// built once would keep the previous flavor's colors after a theme change.
var (
	sshTierProd    lipgloss.Style // PRODUCTION: outlined in spirit, never filled
	sshTierStaging lipgloss.Style
	sshTierDev     lipgloss.Style
	sshAccent      lipgloss.Style // the human's voice and wheel
	sshAccentBold  lipgloss.Style
	sshInfo        lipgloss.Style // the agent at the wheel, running commands
	sshInk         lipgloss.Style // default text, for mixing inside faint rows
	sshOrangeBold  lipgloss.Style // needs you
	sshWheelYou    lipgloss.Style // wheel YOU: the one reverse-video control here
	sshStrike      lipgloss.Style // refuted, declined, recalled
	sshPlate       lipgloss.Style // an evidence plate: a small piece of Lola's voice inside a claim
	sshPlateBad    lipgloss.Style
)

func rebuildSSHStyles() {
	fg := func(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }
	sshTierProd = fg(colBad).Bold(true)
	sshTierStaging = fg(colWarn).Bold(true)
	sshTierDev = fg(colFaint).Bold(true)
	sshAccent = fg(colAccent)
	sshAccentBold = fg(colAccent).Bold(true)
	sshInfo = fg(colBlue)
	sshInk = fg(colText)
	sshOrangeBold = fg(colOrange).Bold(true)
	sshWheelYou = fg(colAccent).Bold(true).Reverse(true)
	sshStrike = fg(colFaint).Strikethrough(true)
	sshPlate = fg(colText).Background(lipgloss.Color(colSel))
	sshPlateBad = fg(colBad).Background(lipgloss.Color(colSel))
}

// sshTierNorm folds a tier to the vocabulary. Anything unset or unknown reads
// as production: a host nobody classified gets the strictest handling.
func sshTierNorm(tier string) string {
	switch tier {
	case protocol.SSHTierStaging, protocol.SSHTierDev:
		return tier
	}
	return protocol.SSHTierProduction
}

// sshTierTag is the tier's word, in its colour. It is never a fill: a
// permanently filled red tag becomes wallpaper in a day.
func sshTierTag(tier string) string {
	switch sshTierNorm(tier) {
	case protocol.SSHTierStaging:
		return sshTierStaging.Render("STAGING")
	case protocol.SSHTierDev:
		return sshTierDev.Render("DEV")
	}
	return sshTierProd.Render("PRODUCTION")
}

// sshTierShort is the abbreviated word a list row has room for.
func sshTierShort(tier string) string {
	switch sshTierNorm(tier) {
	case protocol.SSHTierStaging:
		return "STG"
	case protocol.SSHTierDev:
		return "DEV"
	}
	return "PROD"
}

// sshTierRule is the workspace's band: a line in the tier's colour, so nobody
// can be looking at a production workspace without a red line above it.
func sshTierRule(tier string, w int) string {
	rule := strings.Repeat("━", max(0, w))
	switch sshTierNorm(tier) {
	case protocol.SSHTierStaging:
		return warnText.Render(rule)
	case protocol.SSHTierDev:
		return faintText.Render(strings.Repeat("─", max(0, w)))
	}
	return badText.Render(rule)
}

// sshAgentName is the agent's display name.
func sshAgentName(kind string) string {
	switch agent.Parse(kind) {
	case agent.Codex:
		return "Codex"
	case agent.OpenCode:
		return "OpenCode"
	}
	return "Claude"
}

// sshLine makes one piece of agent or remote text printable on a single row.
// The daemon sanitized it already; this is the TUI's own floor, because a raw
// ESC in a frame would be interpreted by the user's terminal.
func sshLine(s string) string {
	return termSafe(strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s))
}

// sshTextLines splits multi-line text into printable rows.
func sshTextLines(s string) []string {
	parts := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, p := range parts {
		parts[i] = termSafe(p)
	}
	return parts
}

// sshWrap word-wraps plain, already printable text to w columns.
func sshWrap(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, para := range sshTextLines(s) {
		if lipgloss.Width(para) <= w {
			out = append(out, para)
			continue
		}
		cur := ""
		for _, word := range strings.SplitAfter(para, " ") {
			if lipgloss.Width(cur)+lipgloss.Width(word) > w && cur != "" {
				out = append(out, strings.TrimRight(cur, " "))
				cur = ""
			}
			for lipgloss.Width(word) > w {
				head, rest := sshCutWidth(word, w)
				out = append(out, head)
				word = rest
			}
			cur += word
		}
		out = append(out, strings.TrimRight(cur, " "))
	}
	return out
}

// sshWrapHard breaks at exactly w columns, keeping every character in place. An
// approval's command and raw output use it: reflowing them at spaces would show
// something that is not quite what will run or what came back.
func sshWrapHard(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, para := range sshTextLines(s) {
		for lipgloss.Width(para) > w {
			head, rest := sshCutWidth(para, w)
			out = append(out, head)
			para = rest
		}
		out = append(out, para)
	}
	return out
}

// sshCutWidth splits s after at most w columns, and always after one rune at
// least so a wide rune cannot stall a caller's loop.
func sshCutWidth(s string, w int) (string, string) {
	cols, end := 0, 0
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if cols+rw > w && i > 0 {
			return s[:i], s[i:]
		}
		cols += rw
		end = i + len(string(r))
	}
	return s[:end], s[end:]
}

// sshDur renders an elapsed time: 0.3s, 12s, 1m 12s, 2h 3m.
func sshDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		if s := int(d.Seconds()) % 60; s != 0 {
			return fmt.Sprintf("%dm %ds", int(d.Minutes()), s)
		}
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if m := int(d.Minutes()) % 60; m != 0 {
		return fmt.Sprintf("%dh %dm", int(d.Hours()), m)
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// sshExpiry is how long an approval has left: whole minutes while there is plenty, so
// the screen holds still, and seconds only for the last 5 minutes. Same as the app.
func sshExpiry(d time.Duration) string {
	if d >= 5*time.Minute {
		return fmt.Sprintf("%d min", int((d+time.Minute-1)/time.Minute))
	}
	return sshDur(d.Round(time.Second))
}

// sshCoarse renders a duration a human waits on: 4s, 29 min, 2h 3m.
func sshCoarse(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// sshCount is "1 command", "2 commands".
func sshCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// sshMinutes says an idle limit the way the copy does: "15 minutes".
func sshMinutes(seconds int) string {
	if m := seconds / 60; m != 1 {
		return fmt.Sprintf("%d minutes", m)
	}
	return "1 minute"
}

// sshClock is a countdown or a stopwatch: m:ss.
func sshClock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// sshBytes renders a stored size: 0 B, 812 B, 2 KB, 3.1 MB.
func sshBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%d KB", (n+512)/1024)
	case n < 10*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%d MB", (n+512*1024)/(1024*1024))
}

// sshURLRe finds a web address in untrusted text. The daemon's openURL is what
// enforces http(s)-only; this only decides what is offered.
var sshURLRe = regexp.MustCompile(`https?://[^\s<>"'\x60]+`)

// sshBareBlockRe finds a bare block id in agent text.
var sshBareBlockRe = regexp.MustCompile(`\bb[0-9]+\b`)

// sshNow is the clock the arming rule and the countdowns read, so a test can
// move time instead of sleeping.
var sshNow = time.Now
