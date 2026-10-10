package sysload

import (
	"context"
	"strings"
	"testing"
)

func TestParsers(t *testing.T) {
	if got := parseDarwinLoadavg("{ 2.34 2.10 1.90 }\n"); got != 2.34 {
		t.Errorf("darwin = %v", got)
	}
	if got := parseDarwinLoadavg("garbage"); got != -1 {
		t.Errorf("darwin garbage = %v", got)
	}
	if got := parseLinuxLoadavg("0.52 0.58 0.59 1/389 12345\n"); got != 0.52 {
		t.Errorf("linux = %v", got)
	}
	if got := parseLinuxLoadavg(""); got != -1 {
		t.Errorf("linux empty = %v", got)
	}
	mem := []byte("MemTotal:       16000000 kB\nMemFree:  100 kB\nMemAvailable:    4000000 kB\n")
	if got := parseMeminfo(mem); got != 25 {
		t.Errorf("meminfo = %v", got)
	}
	if got := parseMeminfo([]byte("MemTotal: 0 kB\n")); got != -1 {
		t.Errorf("meminfo missing = %v", got)
	}
}

func TestBusy(t *testing.T) {
	s := Sample{Load1: 12, CPUs: 8, FreeMemPercent: 5}
	if msg := Busy(s, Limits{MaxLoadPerCPU: 1.0}); !strings.Contains(msg, "1.50/CPU") || !strings.Contains(msg, "max_load_per_cpu") {
		t.Errorf("load msg = %q", msg)
	}
	if msg := Busy(s, Limits{MaxLoadPerCPU: 2}); msg != "" {
		t.Errorf("under threshold = %q", msg)
	}
	if msg := Busy(s, Limits{MinFreeMemoryPercent: 10}); !strings.Contains(msg, "5% memory free") {
		t.Errorf("mem msg = %q", msg)
	}
	// Unknown values never hold.
	unknown := Sample{Load1: -1, CPUs: 8, FreeMemPercent: -1}
	if msg := Busy(unknown, Limits{MaxLoadPerCPU: 0.01, MinFreeMemoryPercent: 99}); msg != "" {
		t.Errorf("unknown must not hold, got %q", msg)
	}
	if msg := Busy(s, Limits{}); msg != "" {
		t.Errorf("off must not hold, got %q", msg)
	}
}

func TestReadNeverFails(t *testing.T) {
	s := Read(context.Background())
	if s.CPUs <= 0 {
		t.Fatalf("CPUs = %d", s.CPUs)
	}
	t.Logf("sample: %+v", s)
}
