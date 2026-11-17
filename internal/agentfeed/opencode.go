package agentfeed

import (
	"context"
	"errors"

	"github.com/sushidev-team/lola/internal/protocol"
)

// NewOpencode is a Source that says it has no reader. opencode's local data on the machine
// this was written against dates from August, so its current write path is unverified, and
// the design offers nothing it has not measured. It stays this way until a fresh session
// has been read: the sessions open on their terminal, and nothing pretends otherwise.
func NewOpencode(_ string) Source { return opencodeSource{} }

type opencodeSource struct{}

func (opencodeSource) Advance(context.Context, int64) ([]Item, Health) {
	return nil, protocol.FeedUnsupported
}
func (opencodeSource) AtEOF() bool            { return true }
func (opencodeSource) Generation() int64      { return 0 }
func (opencodeSource) Counts() Counts         { return Counts{} }
func (opencodeSource) TextHash(string) string { return "" }
func (opencodeSource) Output(context.Context, string) (string, bool, error) {
	return "", false, errors.New("no reader for this agent")
}
func (opencodeSource) Thread(context.Context, string) ([]Item, error) {
	return nil, errors.New("no reader for this agent")
}
