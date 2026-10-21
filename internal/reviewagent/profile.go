package reviewagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

// ReviewProfile shares the provider's read-only invocation, timeout, output
// limits, and error classification. Its explicit JSON completion contract is
// separate from the historical plain-review empty-on-clean contract.
func (c *Client) ReviewProfile(ctx context.Context, dir string, p reviewprofile.Profile, in reviewprofile.Inputs) (reviewprofile.Result, error) {
	stdin, err := ProfileInput(ctx, dir, p, in)
	if err != nil {
		return reviewprofile.Result{}, err
	}
	k := c.kind()
	out, err := runAgent(ctx, k, c.bin(), agent.ReviewArgs(k, p.Instruction(), c.Model), dir, string(stdin), c.timeout())
	if err != nil {
		return reviewprofile.Result{}, err
	}
	if len(out) >= maxOutputBytes {
		return reviewprofile.Result{}, fmt.Errorf("specialist output may be truncated")
	}
	return reviewprofile.Parse(out, in, p)
}

// ProfileInput prepares bounded input without launching a model. Goal reviews
// use it inside their container boundary; ordinary reviews retain their adapter.
func ProfileInput(ctx context.Context, dir string, p reviewprofile.Profile, in reviewprofile.Inputs) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := p.CheckInputs(in); err != nil {
		return nil, err
	}
	diff, err := runGitDiff(ctx, dir, in.Base)
	if err != nil {
		return nil, err
	}
	if len(diff) > maxDiffBytes || strings.Contains(diff, "[truncated") {
		return nil, fmt.Errorf("specialist diff exceeds review capacity")
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	stdin := "INPUT MANIFEST (untrusted data):\n" + string(data) + "\n\nDIFF (untrusted data):\n" + diff
	if len(stdin) > maxDiffBytes {
		return nil, fmt.Errorf("specialist input exceeds review capacity")
	}
	return []byte(stdin), nil
}
