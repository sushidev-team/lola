package api

import (
	"net/http"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// GET /v1/stats is the org's daily counters for the last hubproto.StatsDays
// days: counters only, no item, repo or device. Signed and org-scoped like
// /v1/org, and checked with hubproto's own Validate before it is sent.

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request, auth store.Device, body []byte) {
	if len(body) != 0 || r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, hubproto.CodeBadRequest)
		return
	}
	now := s.cfg.Now()
	var resp hubproto.StatsView
	err := s.cfg.Store.View(func(d *store.Data) error {
		dev, err := liveDevice(d, auth.ID)
		if err != nil {
			return err
		}
		resp = StatsView(d, dev.Org, now)
		return nil
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if resp.Validate() != nil {
		s.logf("api: stats view failed validation")
		writeError(w, http.StatusInternalServerError, hubproto.CodeInternal)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// StatsView builds the org's stats: the stored days inside the window ending
// today (UTC), oldest first, strictly increasing, counters clamped to their
// caps. Days with nothing are absent.
func StatsView(d *store.Data, org string, now time.Time) hubproto.StatsView {
	v := hubproto.StatsView{Days: []hubproto.StatsDay{}}
	oldest := store.StatsOldest(now)
	today := now.UTC().Format(hubproto.StatsDateLayout)
	for _, day := range d.Stats[org] {
		if day.Date < oldest || day.Date > today {
			continue
		}
		if n := len(v.Days); n > 0 && day.Date <= v.Days[n-1].Date {
			continue
		}
		clamp := func(n int) int { return min(max(n, 0), hubproto.MaxStatsCount) }
		v.Days = append(v.Days, hubproto.StatsDay{
			Date:          day.Date,
			Opened:        clamp(day.Opened),
			Done:          clamp(day.Done),
			Failed:        clamp(day.Failed),
			Escalated:     clamp(day.Escalated),
			FixSecondsSum: min(max(day.FixSecondsSum, 0), hubproto.MaxStatsFixSeconds),
		})
	}
	return v
}
