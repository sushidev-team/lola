// Package sysload samples how busy THIS machine is, for the [load] dispatch
// hold: the 1-minute load average and the share of memory the OS reports free.
//
// It answers with what the platform itself publishes rather than computing a
// CPU percentage from deltas — a dispatch decision every poll interval needs a
// level, not a rate, and the load average is the number a user already reads
// in `uptime`. Memory is the kernel's own "free" figure (macOS
// kern.memorystatus_level, which is exactly what `memory_pressure` prints as
// "System-wide memory free percentage"; Linux MemAvailable/MemTotal), because
// raw free pages on macOS are near zero by design.
//
// Every value that cannot be read is reported as unknown (negative), never as
// a guess: the caller holds dispatch only on a value it actually has.
// Stdlib-only leaf.
package sysload

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"
)

// Sample is one reading. Load1 and FreeMemPercent are -1 when unknown.
type Sample struct {
	Load1          float64
	CPUs           int
	FreeMemPercent float64
}

// LoadPerCPU is Load1 / CPUs, or -1 when either is unknown.
func (s Sample) LoadPerCPU() float64 {
	if s.Load1 < 0 || s.CPUs <= 0 {
		return -1
	}
	return s.Load1 / float64(s.CPUs)
}

// probeTimeout bounds each sysctl exec: a dispatch tick must never wait on it.
const probeTimeout = 2 * time.Second

// Read samples the machine. It never fails: what it cannot read is unknown.
func Read(ctx context.Context) Sample {
	s := Sample{Load1: -1, CPUs: goruntime.NumCPU(), FreeMemPercent: -1}
	switch goruntime.GOOS {
	case "darwin":
		if out, err := sysctl(ctx, "vm.loadavg"); err == nil {
			s.Load1 = parseDarwinLoadavg(out)
		}
		if out, err := sysctl(ctx, "kern.memorystatus_level"); err == nil {
			if v, err := strconv.ParseFloat(strings.TrimSpace(out), 64); err == nil && v >= 0 && v <= 100 {
				s.FreeMemPercent = v
			}
		}
	case "linux":
		if b, err := os.ReadFile("/proc/loadavg"); err == nil {
			s.Load1 = parseLinuxLoadavg(string(b))
		}
		if b, err := os.ReadFile("/proc/meminfo"); err == nil {
			s.FreeMemPercent = parseMeminfo(b)
		}
	}
	return s
}

func sysctl(ctx context.Context, name string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "sysctl", "-n", name).Output()
	return string(out), err
}

// parseDarwinLoadavg reads `sysctl -n vm.loadavg`: "{ 2.34 2.10 1.90 }".
func parseDarwinLoadavg(s string) float64 {
	f := strings.Fields(strings.Trim(strings.TrimSpace(s), "{}"))
	if len(f) == 0 {
		return -1
	}
	return parseNonNeg(f[0])
}

// parseLinuxLoadavg reads /proc/loadavg: "0.52 0.58 0.59 1/389 12345".
func parseLinuxLoadavg(s string) float64 {
	f := strings.Fields(s)
	if len(f) == 0 {
		return -1
	}
	return parseNonNeg(f[0])
}

func parseNonNeg(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return -1
	}
	return v
}

// parseMeminfo derives the free percentage from MemAvailable/MemTotal.
func parseMeminfo(b []byte) float64 {
	var total, avail float64 = -1, -1
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total = parseNonNeg(f[1])
		case "MemAvailable:":
			avail = parseNonNeg(f[1])
		}
	}
	if total <= 0 || avail < 0 {
		return -1
	}
	return 100 * avail / total
}

// Limits is the [load] table as plain numbers (0 = that check off), so this
// leaf need not import config.
type Limits struct {
	MaxLoadPerCPU        float64
	MinFreeMemoryPercent float64
}

// Busy explains why s is over lim, or "" when it is not (or the relevant value
// is unknown). The text is the poll's LastError, so it names the number, the
// threshold and the key that sets it.
func Busy(s Sample, lim Limits) string {
	if lim.MaxLoadPerCPU > 0 {
		if per := s.LoadPerCPU(); per > lim.MaxLoadPerCPU {
			return fmt.Sprintf("machine busy: load %.2f on %d CPUs (%.2f/CPU) is above load.max_load_per_cpu %.2f",
				s.Load1, s.CPUs, per, lim.MaxLoadPerCPU)
		}
	}
	if lim.MinFreeMemoryPercent > 0 && s.FreeMemPercent >= 0 && s.FreeMemPercent < lim.MinFreeMemoryPercent {
		return fmt.Sprintf("machine busy: %.0f%% memory free is below load.min_free_memory_percent %.0f%%",
			s.FreeMemPercent, lim.MinFreeMemoryPercent)
	}
	return ""
}
