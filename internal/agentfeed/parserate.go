package agentfeed

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// parseRateBytes bounds how much of a transcript's tail `lola doctor` reads.
const parseRateBytes = 4 << 20

// ParseRate is how many of a transcript's conversation records this reader understands,
// over its last few megabytes. It reports counts only and keeps nothing it read.
//
// A record counts as a conversation record when it says it is a user or assistant message,
// and as understood when it decodes to a message of a known shape. This is the early
// warning for "Claude Code changed its format": the record types lola does not know are
// tolerated on purpose and do not count against it.
func ParseRate(path string) (understood, total int, err error) {
	f, info, err := openRegular(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	start := max(info.Size()-parseRateBytes, 0)
	buf := make([]byte, info.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return 0, 0, err
	}
	if start > 0 { // the read began mid-line
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	for len(buf) > 0 {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break // still being written
		}
		line := buf[:i]
		buf = buf[i+1:]
		skip := false
		for _, p := range skipPrefixes {
			skip = skip || bytes.HasPrefix(line, p)
		}
		if skip || (!bytes.Contains(line, typeUser) && !bytes.Contains(line, typeAsst)) {
			continue
		}
		total++
		if understandsRecord(line) {
			understood++
		}
	}
	return understood, total, nil
}

func understandsRecord(line []byte) bool {
	var r claudeRecord
	if !decodeRecord(line, &r) || (r.Type != "user" && r.Type != "assistant") {
		return false
	}
	content := bytes.TrimSpace(r.Message.Content)
	if len(content) == 0 {
		return false
	}
	if content[0] == '"' {
		return r.Type == "user"
	}
	var blocks []claudeBlock
	if json.Unmarshal(content, &blocks) != nil {
		return false
	}
	known := len(blocks) == 0
	for _, b := range blocks {
		switch b.Type {
		case "text", "tool_use", "tool_result", "thinking", "redacted_thinking", "image":
			known = true
		}
	}
	return known
}
