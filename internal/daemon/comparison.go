package daemon

import (
	"github.com/sushidev-team/lola/internal/comparison"
	"github.com/sushidev-team/lola/internal/protocol"
)

// Comparisons are portable input/output records. They do not mutate goal,
// knowledge or review stores, start agents, or contact external services.
func (d *Daemon) handleComparison(req protocol.Request) protocol.Response {
	if req.Cmd == "comparisonTemplate" {
		return dataResponse(comparison.Template())
	}
	b, err := comparison.Decode(req.Args)
	if err != nil {
		return protocol.Response{Error: err.Error()}
	}
	if req.Cmd == "comparisonPrepare" {
		out, err := comparison.Prepare(b)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return dataResponse(out)
	}
	report, err := comparison.Generate(b)
	if err != nil {
		return protocol.Response{Error: err.Error()}
	}
	return dataResponse(report)
}
