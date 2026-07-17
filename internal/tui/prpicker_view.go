package tui

import (
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
)

func (m *rootModel) prPickerView() string {
	return strings.Join(m.prPickerLines(), "\n")
}

func (m *rootModel) prPickerLines() []string {
	W, H := m.width, m.height
	if W <= 0 {
		W = 100
	}
	if H <= 0 {
		H = 24
	}
	p := &m.prpick

	out := make([]string, 0, H)
	out = append(out, m.vitalsBar(W))

	crumb := faintText.Render("lola ▸ "+p.project+" ▸ ") + "open PRs"
	right := ""
	if p.data != nil {
		freshness := fmt.Sprintf("%d open · %ds ago", len(p.data.PRs), p.data.AgeSeconds)
		if p.data.Stale {
			freshness += " · " + warnText.Render("stale")
		}
		right = faintText.Render(freshness)
	}
	out = append(out, prpickHeaderLine(crumb, right, W))
	if p.filtering {
		out = append(out, previewLine(faintText.Render("/")+p.filter+"_", W))
	}

	panelH := H - len(out) - 2
	if panelH < 3 {
		panelH = 3
	}
	out = append(out, m.prPickerPanel(W, panelH)...)
	out = append(out, m.prPickerMessage(W))
	out = append(out, m.prPickerKeybar(W))
	return fitHeight(out, H)
}

func prpickHeaderLine(left, right string, w int) string {
	pad := w - lipWidth(left) - lipWidth(right)
	if pad < 1 {
		pad = 1
	}
	return previewLine(left+strings.Repeat(" ", pad)+right, w)
}

func (m *rootModel) prPickerPanel(w, panelH int) []string {
	p := &m.prpick
	var body []string

	switch {
	case p.loading && p.data == nil:
		body = append(body, faintText.Render("  Fetching open PRs…"))
	case p.daemon:
		body = append(body, badText.Render("  daemon not running")+faintText.Render(" — ^r to start"))
	case p.err != "":
		body = append(body, badText.Render("  couldn't list PRs: ")+p.err, faintText.Render("  r to retry"))
	default:
		rows := m.prpickRows()
		if len(rows) == 0 {
			body = append(body, faintText.Render("  No open PRs — w to create a worktree, or r to refresh"))
		} else {
			header := []string{"PR", "TITLE", "AUTHOR", "BRANCH", "CI", "REVIEW"}
			cells := make([][]string, 0, len(rows))
			for _, pr := range rows {
				cells = append(cells, prRowCells(pr))
			}
			widths := colWidths(header, cells)
			body = append(body, tblHeader.Render(padCells(header, widths)))
			innerH := panelH - 3
			if innerH < 1 {
				innerH = 1
			}
			start := viewportStart(p.cursor, len(cells), innerH)
			for i := start; i < len(cells) && i < start+innerH; i++ {
				line := padCells(cells[i], widths)
				if i == p.cursor {
					line = highlightRow(line, w-4, bgSGR(colSel))
				}
				body = append(body, line)
			}
		}
	}
	return box(paneTitle("Open PRs", ""), body, w, panelH, true)
}

func prRowCells(pr protocol.PrRow) []string {
	title := truncPlain(pr.Title, 34)
	badges := ""
	if pr.IsDraft {
		badges += faintText.Render(" [draft]")
	}
	if pr.IsFork {
		badges += faintText.Render(" [fork]")
	}
	author := pr.Author
	if author == "" {
		author = faintText.Render("—")
	}
	ci := prCheckGlyph(pr.Checks)
	review := prReviewGlyph(pr.Review)
	num := fmt.Sprintf("#%d", pr.Number)
	if pr.AlreadyOpen {
		num = faintText.Render(num)
	}
	return []string{num, title + badges, author, truncPlain(pr.Branch, 22), ci, review}
}

func prCheckGlyph(state string) string {
	switch state {
	case "pass":
		return goodText.Render("✓")
	case "fail":
		return badText.Render("✕")
	case "pending":
		return warnText.Render("•")
	default:
		return faintText.Render("—")
	}
}

func prReviewGlyph(decision string) string {
	switch decision {
	case "APPROVED":
		return goodText.Render("✓ appr")
	case "CHANGES_REQUESTED":
		return badText.Render("✗ chg")
	case "REVIEW_REQUIRED":
		return faintText.Render("○ req")
	default:
		return faintText.Render("○")
	}
}

func (m *rootModel) prPickerMessage(w int) string {
	p := &m.prpick
	if p.flash != "" {
		return previewLine(warnText.Render(p.flash), w)
	}
	if p.loading && p.data != nil {
		return previewLine(faintText.Render("refreshing…"), w)
	}
	return ""
}

func (m *rootModel) prPickerKeybar(w int) string {
	p := &m.prpick
	if p.filtering {
		return previewLine(faintText.Render("type to filter · enter apply · esc clear"), w)
	}
	keys := []string{"↑↓ move", "enter open (detached shell)", "o browser", "r refresh", "/ filter", "esc back", "q quit"}
	return previewLine(faintText.Render(strings.Join(keys, " · ")), w)
}

// lipWidth is the display width of a possibly-styled string.
func lipWidth(s string) int { return len(stripANSI(s)) }
