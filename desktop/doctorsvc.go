package main

import (
	"context"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/doctor"
)

// DoctorService runs the same structured health checks the TUI's doctor overlay
// does (internal/doctor.Check) — in-process, off the daemon, bounded to 15s.
type DoctorService struct{}

type DoctorResultDTO struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail"`
	Critical bool   `json:"critical"`
}

type DoctorReportDTO struct {
	Results []DoctorResultDTO `json:"results"`
	Summary string            `json:"summary"`
	OK      bool              `json:"ok"`
}

// Run loads config and executes the health checks. A missing/invalid config is
// not fatal — Check tolerates a nil-ish config and reports it as a failed check.
func (s *DoctorService) Run() (DoctorReportDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var cfg *config.Config
	if path, err := config.DefaultPath(); err == nil {
		cfg, _ = config.Load(path)
	}
	if cfg == nil {
		cfg = &config.Config{} // Check dereferences cfg fields; never hand it nil
	}

	rep := doctor.Check(ctx, cfg)
	out := DoctorReportDTO{Summary: rep.Summary(), OK: rep.OK()}
	for _, r := range rep.Results {
		out.Results = append(out.Results, DoctorResultDTO{
			Name:     r.Name,
			OK:       r.OK,
			Detail:   r.Detail,
			Critical: r.Critical,
		})
	}
	return out, nil
}
