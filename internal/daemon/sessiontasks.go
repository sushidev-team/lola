package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/taskboard"
)

func (d *Daemon) handleSessionTask(ctx context.Context, req protocol.Request, author string) protocol.Response {
	req.Session = session.BaseID(strings.TrimSpace(req.Session))
	var a protocol.ReportArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return protocol.Response{Error: "task: invalid args"}
	}
	if len(a.Argv) == 0 {
		return protocol.Response{Error: taskboard.Usage}
	}
	s, known := d.sessions.Get(req.Session)
	if !known {
		return protocol.Response{Error: "task: unknown session"}
	}
	if len(a.Argv) == 1 && a.Argv[0] == "list" {
		return dataResponse(s.Tasks)
	}
	if !d.reports.allow(req.Session, time.Now()) {
		return protocol.Response{Error: errReportRateLimited.Error()}
	}
	var applyErr error
	var notify *protocol.SessionTask
	_, known = d.sessions.Update(req.Session, func(s *session.Session) bool {
		// A retry sends the existing item, never creates a second card. Only an
		// explicit user action can send; agent reports cannot send messages.
		if a.Argv[0] == "retry" && author == "user" && len(a.Argv) == 2 {
			i := slices.IndexFunc(s.Tasks, func(t protocol.SessionTask) bool { return t.ID == a.Argv[1] })
			if i < 0 {
				applyErr = fmt.Errorf("task: unknown ID")
				return false
			}
			if s.Tasks[i].Notification != "failed" && s.Tasks[i].Notification != "sending" {
				return false
			}
			if s.Tasks[i].Notification == "sending" && time.Since(s.Tasks[i].UpdatedAt) < time.Minute {
				applyErr = fmt.Errorf("task: sending already in progress")
				return false
			}
			s.Tasks = slices.Clone(s.Tasks)
			s.Tasks[i].Notification = "sending"
			s.Tasks[i].SendError = ""
			s.Tasks[i].UpdatedAt = time.Now()
			copied := s.Tasks[i]
			notify = &copied
			return true
		}
		next, id, err := taskboard.Apply(s.Tasks, a.Argv, author, time.Now())
		if err != nil {
			applyErr = err
			return false
		}
		s.Tasks = next
		// Confirmations update the shared agreement without starting a turn or
		// releasing a held conversation queue. The next message already asks the
		// agent to read the board. Scope changes still need its attention now.
		confirmed := a.Argv[0] == "state" && len(a.Argv) == 3 && a.Argv[2] == "done"
		if author == "user" && id != "" && confirmed {
			i := slices.IndexFunc(next, func(t protocol.SessionTask) bool { return t.ID == id })
			s.Tasks[i].Notification = ""
			s.Tasks[i].SendError = ""
		}
		if author == "user" && id != "" && !confirmed && (a.Argv[0] == "add" || a.Argv[0] == "edit" || a.Argv[0] == "state") {
			i := slices.IndexFunc(next, func(t protocol.SessionTask) bool { return t.ID == id })
			s.Tasks[i].Notification = "sending"
			s.Tasks[i].SendError = ""
			copied := s.Tasks[i]
			notify = &copied
		}
		return id != ""
	})
	if !known {
		return protocol.Response{Error: "task: session no longer exists"}
	}
	if applyErr != nil {
		return protocol.Response{Error: applyErr.Error()}
	}
	if err := d.sessions.Save(); err != nil {
		return protocol.Response{Error: "task: could not save: " + err.Error()}
	}
	if notify != nil {
		message := fmt.Sprintf("Session %s [%s]: %s\nUser set status: %s.", notify.Kind, notify.ID, notify.Text, notify.State)
		if notify.ParentID != "" {
			message += "\nRequirement belongs to task [" + notify.ParentID + "]."
		} else if notify.Scope == "global" {
			message += "\nThis is a global requirement for the whole session."
		} else if notify.Kind == "requirement" {
			message += "\nOrganize this under the existing task most responsible for it, or create an appropriate task if none fits."
		}
		message += "\n\n" + taskboard.Briefing("lola")
		_, err := d.handleAgentSay(ctx, protocol.AgentSayArgs{Session: req.Session, Text: message})
		d.sessions.Update(req.Session, func(s *session.Session) bool {
			i := slices.IndexFunc(s.Tasks, func(t protocol.SessionTask) bool { return t.ID == notify.ID })
			if i < 0 || s.Tasks[i].Text != notify.Text || s.Tasks[i].State != notify.State || !s.Tasks[i].UpdatedAt.Equal(notify.UpdatedAt) {
				return false
			}
			s.Tasks = slices.Clone(s.Tasks)
			s.Tasks[i].Notification = "accepted"
			if err != nil {
				s.Tasks[i].Notification = "failed"
				s.Tasks[i].SendError = err.Error()
			}
			return true
		})
		if saveErr := d.sessions.Save(); saveErr != nil {
			return protocol.Response{Error: "task: delivery recorded in memory but could not be saved: " + saveErr.Error()}
		}
	}
	s, _ = d.sessions.Get(req.Session)
	return dataResponse(s.Tasks)
}
