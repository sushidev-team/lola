// Package taskboard owns the bounded, shared requirements list. It never drives
// agent lifecycle, and changing a status never executes anything.
package taskboard

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/protocol"
)

const MaxTasks = 100
const MaxText = 500

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)

const Usage = `task list | task add <id> <task|requirement> [--task <task-id>|--global] <text> | task state <id> <todo|doing|checking|done> | task feedback <id> <text> | task edit <id> <text> | task attach <requirement-id> <task-id|--global|none> | task remove <id>`

// Apply returns a new slice and the changed ID. Repeating an identical add is
// idempotent, so retrying a lost reply cannot duplicate a requirement or send.
func Apply(tasks []protocol.SessionTask, argv []string, author string, now time.Time) ([]protocol.SessionTask, string, error) {
	fail := func(message string) ([]protocol.SessionTask, string, error) {
		return tasks, "", fmt.Errorf("task: %s; %s", message, Usage)
	}
	if len(argv) < 2 || !validID.MatchString(argv[1]) {
		return fail("valid action and ID required")
	}
	action, id := argv[0], argv[1]
	idx := slices.IndexFunc(tasks, func(t protocol.SessionTask) bool { return t.ID == id })
	next := slices.Clone(tasks)
	text := func(parts []string) (string, error) {
		value := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, strings.Join(parts, " "))), " ")
		if value == "" || utf8.RuneCountInString(value) > MaxText {
			return "", fmt.Errorf("text must contain 1–%d characters", MaxText)
		}
		return value, nil
	}
	switch action {
	case "add":
		if len(argv) < 4 || (argv[2] != "task" && argv[2] != "requirement") {
			return fail("kind and text required")
		}
		parent, scope := "", ""
		parts := argv[3:]
		if len(parts) > 0 && parts[0] == "--task" {
			if len(parts) < 3 || argv[2] != "requirement" {
				return fail("--task requires a requirement, task ID and text")
			}
			parent = parts[1]
			parts = parts[2:]
			if !slices.ContainsFunc(tasks, func(t protocol.SessionTask) bool { return t.ID == parent && t.Kind == "task" }) {
				return fail("parent task not found")
			}
		}
		if len(parts) > 0 && parts[0] == "--global" && parent == "" {
			if argv[2] != "requirement" {
				return fail("--global applies only to requirements")
			}
			scope = "global"
			parts = parts[1:]
		}
		if author == "agent" && argv[2] == "requirement" && parent == "" && scope == "" {
			return fail("attach requirements to the relevant task: add <id> requirement --task <task-id> <text>; or use --global for session-wide criteria; read task list first")
		}
		value, err := text(parts)
		if err != nil {
			return fail(err.Error())
		}
		if idx >= 0 {
			if tasks[idx].Text == value && tasks[idx].Kind == argv[2] && tasks[idx].ParentID == parent && tasks[idx].Scope == scope {
				return tasks, "", nil
			}
			return fail("ID already exists")
		}
		if len(tasks) >= MaxTasks {
			return fail("board is full")
		}
		next = append(next, protocol.SessionTask{ID: id, ParentID: parent, Scope: scope, Kind: argv[2], Text: value, State: "todo", Author: author, UpdatedAt: now})
	case "state":
		if idx < 0 {
			return fail("unknown ID")
		}
		if len(argv) != 3 || (argv[2] != "todo" && argv[2] != "doing" && argv[2] != "checking" && argv[2] != "done") {
			return fail("invalid state")
		}
		if argv[2] == "done" && tasks[idx].Kind == "task" && slices.ContainsFunc(tasks, func(t protocol.SessionTask) bool { return t.ParentID == id && t.State != "done" }) {
			return fail("complete the task's requirements before marking it done")
		}
		if next[idx].State == argv[2] {
			return tasks, "", nil
		}
		next[idx].State = argv[2]
		next[idx].UpdatedAt = now
	case "feedback":
		if idx < 0 {
			return fail("unknown ID")
		}
		value, err := text(argv[2:])
		if err != nil {
			return fail(err.Error())
		}
		// Reports attach feedback already received through the conversation; they
		// never send it back to the agent or overwrite the agreed scope.
		if next[idx].Feedback == value && next[idx].State == "todo" {
			return tasks, "", nil
		}
		next[idx].Feedback = value
		next[idx].State = "todo"
		next[idx].UpdatedAt = now
	case "edit":
		if idx < 0 {
			return fail("unknown ID")
		}
		if author == "agent" && tasks[idx].Author == "user" {
			return fail("user requirements can be marked, but only the user can rewrite them")
		}
		value, err := text(argv[2:])
		if err != nil {
			return fail(err.Error())
		}
		if value == next[idx].Text {
			return tasks, "", nil
		}
		next[idx].State = "todo"
		next[idx].Text = value
		next[idx].UpdatedAt = now
	case "attach":
		if len(argv) != 3 || idx < 0 || tasks[idx].Kind != "requirement" {
			return fail("a requirement and parent task are required")
		}
		parent, scope := argv[2], ""
		if parent == "--global" {
			parent, scope = "", "global"
		} else if parent == "none" {
			if author == "agent" {
				return fail("agents must attach requirements to a relevant task or explicitly mark them --global")
			}
			parent = ""
		} else if !slices.ContainsFunc(tasks, func(t protocol.SessionTask) bool { return t.ID == parent && t.Kind == "task" }) {
			return fail("parent task not found")
		}
		next[idx].ParentID = parent
		next[idx].Scope = scope
		next[idx].UpdatedAt = now
	case "remove":
		if idx < 0 || len(argv) != 2 {
			return fail("unknown ID")
		}
		if author == "agent" && tasks[idx].Author == "user" {
			return fail("only the user can remove their requirement")
		}
		if author == "agent" && slices.ContainsFunc(tasks, func(t protocol.SessionTask) bool { return t.ParentID == id }) {
			return fail("move requirements to another task before removing their parent")
		}
		next = slices.Delete(next, idx, idx+1)
		for i := range next {
			if next[i].ParentID == id {
				next[i].ParentID = ""
			}
		}
	default:
		return fail("unknown action")
	}
	// Completion never hides newly added or reopened scope. Do not cascade done
	// from a parent to its criteria, and never infer session lifecycle from this.
	for i := range next {
		if next[i].Kind == "task" && next[i].State == "done" && slices.ContainsFunc(next, func(t protocol.SessionTask) bool { return t.ParentID == next[i].ID && t.State != "done" }) {
			next[i].State = "todo"
			next[i].UpdatedAt = now
		}
	}
	return next, id, nil
}
