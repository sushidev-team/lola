package hubproto

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func goodStats() StatsView {
	return StatsView{Days: []StatsDay{
		{Date: "2026-11-01", Opened: 4, Done: 2, Failed: 1, FixSecondsSum: 7200},
		{Date: "2026-11-03", Opened: 1, Escalated: 1},
	}}
}

func TestStatsViewValidate(t *testing.T) {
	if err := goodStats().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (StatsView{}).Validate(); err != nil {
		t.Fatalf("empty: %v", err)
	}
	full := StatsView{}
	d0 := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	for i := 0; i < StatsDays; i++ {
		full.Days = append(full.Days, StatsDay{Date: d0.AddDate(0, 0, i).Format(StatsDateLayout)})
	}
	if err := full.Validate(); err != nil {
		t.Fatalf("full: %v", err)
	}
	bad := map[string]func(*StatsView){
		"too many": func(s *StatsView) {
			*s = full
			s.Days = append(s.Days, StatsDay{Date: d0.AddDate(0, 0, StatsDays).Format(StatsDateLayout)})
		},
		"span too wide":  func(s *StatsView) { s.Days[1].Date = "2027-02-01" },
		"bad date":       func(s *StatsView) { s.Days[0].Date = "1 Nov 2026" },
		"no padding":     func(s *StatsView) { s.Days[0].Date = "2026-11-1" },
		"impossible day": func(s *StatsView) { s.Days[0].Date = "2026-02-30" },
		"date injection": func(s *StatsView) { s.Days[0].Date = "2026-11-01\n" },
		"repeat":         func(s *StatsView) { s.Days[1].Date = s.Days[0].Date },
		"descending":     func(s *StatsView) { s.Days[0], s.Days[1] = s.Days[1], s.Days[0] },
		"neg opened":     func(s *StatsView) { s.Days[0].Opened = -1 },
		"huge done":      func(s *StatsView) { s.Days[0].Done = MaxStatsCount + 1 },
		"neg failed":     func(s *StatsView) { s.Days[0].Failed = -1 },
		"neg escalated":  func(s *StatsView) { s.Days[0].Escalated = -1 },
		"neg fix":        func(s *StatsView) { s.Days[0].FixSecondsSum = -1 },
		"huge fix":       func(s *StatsView) { s.Days[0].FixSecondsSum = MaxStatsFixSeconds + 1 },
	}
	for name, mut := range bad {
		s := goodStats()
		mut(&s)
		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}

	// The JSON shape is the contract; unknown fields are refused.
	b, _ := json.Marshal(goodStats())
	want := `{"days":[{"date":"2026-11-01","opened":4,"done":2,"failed":1,"escalated":0,"fix_seconds_sum":7200},` +
		`{"date":"2026-11-03","opened":1,"done":0,"failed":0,"escalated":1,"fix_seconds_sum":0}]}`
	if string(b) != want {
		t.Fatalf("got %s", b)
	}
	var back StatsView
	if err := DecodeStrictLimit(strings.NewReader(`{"days":[],"repos":["a/b"]}`), MaxViewBody, &back); err == nil {
		t.Fatal("unknown field accepted")
	}
	if PathStats != "/v1/stats" {
		t.Fatal(PathStats)
	}
}
