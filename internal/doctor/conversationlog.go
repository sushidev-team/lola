package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sushidev-team/lola/internal/agentfeed"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/usage"
)

const (
	checkConversationLog = "conversation log"
	// parseRateFloor is the share of conversation records the reader must understand
	// before lola calls the format healthy. Claude Code documents the transcript as
	// internal, so a release can change it, and the reader tolerates what it does not
	// know by design: without this figure such a change would only ever look like a
	// conversation with something missing.
	parseRateFloor = 0.9
	// parseRateMinRecords keeps a brand-new transcript from raising a warning on the
	// strength of one odd line.
	parseRateMinRecords = 10
)

// conversationLogResult reports how much of the newest lola session's transcript the
// conversation reader understands. There is no row until a lola session has written one.
// It reports counts and nothing from the file. The result never gates anything.
func conversationLogResult() (Result, bool) {
	home, err := config.Home()
	if err != nil {
		return Result{}, false
	}
	path := newestLolaTranscript(usage.ProjectsRoot(), filepath.Join(home, "worktrees"))
	if path == "" {
		return Result{}, false
	}
	understood, total, err := agentfeed.ParseRate(path)
	if err != nil || total == 0 {
		return Result{}, false
	}
	rate := float64(understood) / float64(total)
	res := Result{
		Name:   checkConversationLog,
		OK:     true,
		Detail: fmt.Sprintf("read %d of %d records in the newest transcript", understood, total),
	}
	if rate < parseRateFloor && total >= parseRateMinRecords {
		res.OK = false
		res.Detail = fmt.Sprintf("read only %d of %d records in the newest transcript: the conversation view may be missing part of it, and the terminal is still complete", understood, total)
	}
	return res, true
}

// newestLolaTranscript finds the most recently written transcript of a session under
// worktrees. A person's own claude work elsewhere is none of lola's business, so only the
// slug directories of that tree are listed.
func newestLolaTranscript(root, worktrees string) string {
	prefix := filepath.Base(usage.SlugDir(root, worktrees))
	if root == "" || prefix == "" || prefix == "." {
		return ""
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var newest string
	var newestAt int64
	for _, d := range dirs {
		if !d.IsDir() || !strings.HasPrefix(d.Name(), prefix) {
			continue
		}
		dir := filepath.Join(root, d.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !agentfeed.ValidSegment(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if at := info.ModTime().UnixNano(); at > newestAt {
				newest, newestAt = filepath.Join(dir, e.Name()), at
			}
		}
	}
	return newest
}
