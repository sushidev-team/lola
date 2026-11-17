package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/comparison"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tui"
)

var comparisonRequest = tui.Request

func comparisonCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "compare", Short: "Prepare and report reproducible paired evaluations (no execution)"}
	cmd.AddCommand(&cobra.Command{Use: "template", Short: "Print an unfilled comparison plan as JSON", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return comparisonSend(c, "comparisonTemplate", nil, false) }})
	prepare := &cobra.Command{Use: "prepare <plan.json>", Short: "Print two run-record drafts per declared task; preserve the plan separately", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		raw, err := readComparison(args[0])
		if err != nil {
			return err
		}
		b, err := comparison.Decode(raw)
		if err != nil {
			return err
		}
		if _, err = comparison.Prepare(b); err != nil {
			return err
		}
		return comparisonSend(c, "comparisonPrepare", raw, false)
	}}
	var markdown bool
	report := &cobra.Command{Use: "report <records.json>", Short: "Report paired outcomes with explicit missing-data and provenance limits", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		raw, err := readComparison(args[0])
		if err != nil {
			return err
		}
		if _, err = comparison.Decode(raw); err != nil {
			return err
		}
		return comparisonSend(c, "comparisonReport", raw, markdown)
	}}
	report.Flags().BoolVar(&markdown, "markdown", false, "print Markdown instead of the structured JSON report")
	cmd.AddCommand(prepare, report)
	return cmd
}
func readComparison(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("comparison input must be a regular JSON file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, comparison.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > comparison.MaxBytes {
		return nil, fmt.Errorf("comparison bundle exceeds 512 KiB")
	}
	return raw, nil
}
func comparisonSend(c *cobra.Command, command string, args json.RawMessage, markdown bool) error {
	raw, err := comparisonRequest(protocol.Request{Cmd: command, Args: args})
	if err != nil {
		return err
	}
	if markdown {
		var result comparison.Report
		if err = json.Unmarshal(raw, &result); err != nil {
			return err
		}
		if result.Version != comparison.Version || result.BundleSHA256 == "" || result.Markdown == "" {
			return fmt.Errorf("invalid comparison report response")
		}
		_, err = fmt.Fprint(c.OutOrStdout(), result.Markdown)
		return err
	}
	var result any
	if err = json.Unmarshal(raw, &result); err != nil {
		return err
	}
	enc := json.NewEncoder(c.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
