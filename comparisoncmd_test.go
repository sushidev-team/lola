package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/comparison"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestComparisonCLIUsesOneReadOnlyRPCAndPreservesInput(t *testing.T) {
	old := comparisonRequest
	t.Cleanup(func() { comparisonRequest = old })
	calls := 0
	b := comparison.Template()
	b.Pairs[0] = comparison.Pair{ID: "one", Task: "Preserve focus", Acceptance: []string{"Focus returns"}, Repositories: []comparison.Repository{{Name: "test/repo", Base: strings.Repeat("a", 40)}}, Limits: comparison.Limits{AgentCalls: 4, RuntimeSeconds: 600, Tokens: 10000}, Intervention: "One reviewed lesson"}
	raw, _ := json.Marshal(b)
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	comparisonRequest = func(req protocol.Request) (json.RawMessage, error) {
		calls++
		switch req.Cmd {
		case "comparisonTemplate":
			return json.Marshal(comparison.Template())
		case "comparisonPrepare":
			got, err := comparison.Decode(req.Args)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := comparison.Prepare(got)
			if err != nil {
				t.Fatal(err)
			}
			return json.Marshal(prepared)
		case "comparisonReport":
			got, err := comparison.Decode(req.Args)
			if err != nil {
				t.Fatal(err)
			}
			r, err := comparison.Generate(got)
			if err != nil {
				t.Fatal(err)
			}
			return json.Marshal(r)
		}
		t.Fatal(req.Cmd)
		return nil, nil
	}
	for _, args := range [][]string{{"template"}, {"prepare", path}, {"report", path}, {"report", path, "--markdown"}} {
		cmd := comparisonCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if len(args) == 3 && !strings.HasPrefix(out.String(), "# Comparison:") {
			t.Fatal(out.String())
		}
	}
	if calls != 4 {
		t.Fatal("automatic retry", calls)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(raw) {
		t.Fatal("input changed")
	}
}
func TestComparisonCLIFailsBeforeSocketOnInvalidInput(t *testing.T) {
	old := comparisonRequest
	t.Cleanup(func() { comparisonRequest = old })
	comparisonRequest = func(protocol.Request) (json.RawMessage, error) {
		t.Fatal("invalid input reached daemon")
		return nil, nil
	}
	path := filepath.Join(t.TempDir(), "bad.json")
	for _, raw := range []string{`{"version":99}`, `{} {}`, strings.Repeat(" ", comparison.MaxBytes+1)} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := comparisonCmd()
		cmd.SetArgs([]string{"report", path})
		cmd.SetOut(new(bytes.Buffer))
		cmd.SetErr(new(bytes.Buffer))
		if err := cmd.Execute(); err == nil {
			t.Fatal("invalid file accepted")
		}
	}
}
