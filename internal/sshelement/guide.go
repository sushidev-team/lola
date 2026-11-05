package sshelement

import _ "embed"

// These are what `lola ssh guide` prints: the format, the framework and a page to start from,
// which an agent reads when it needs them, kept out of the briefing every session pays for.
var (
	//go:embed guide/answers.md
	GuideAnswers string
	//go:embed guide/widgets.md
	GuideWidgets string
	//go:embed guide/widget-template.html
	GuideWidgetTemplate string
)
