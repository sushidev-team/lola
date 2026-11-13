package goalcheck

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"slices"
	"time"
)

// These limits are also used by containerArgs. They describe allocated ceilings,
// not measured CPU or memory consumption or a monetary charge.
const (
	CPULimit                   = 2
	MemoryLimitBytes     int64 = 2 << 30
	ScratchLimitBytes    int64 = 512 << 20
	PIDLimit                   = 256
	maxUsageReceiptBytes int64 = 4 << 20
	maxUsageReadBytes    int64 = 64 << 20
)

var errUsageReadLimit = errors.New("validation usage read budget reached")

type UsageRef struct {
	ID, Operation string
	Spec          Spec
}
type RunUsage struct {
	ID               string    `json:"id"`
	Operation        string    `json:"operation"`
	Check            string    `json:"check"`
	Member           string    `json:"member,omitempty"`
	State            string    `json:"state"`
	Stopped          bool      `json:"stopped"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at,omitempty"`
	ElapsedMillis    *int64    `json:"elapsed_millis"` // null when interrupted or still running
	TimeoutSeconds   int       `json:"timeout_seconds"`
	MemoryLimitBytes int64     `json:"memory_limit_bytes"`
}
type UsageReport struct {
	Expected          int        `json:"expected_checks"`
	Inspected         int        `json:"inspected_checks"`
	Missing           int        `json:"not_started_or_missing"`
	Invalid           int        `json:"invalid_receipts"`
	UnknownDuration   int        `json:"unknown_duration_runs"`
	ElapsedMillis     int64      `json:"observed_elapsed_millis"`
	CPULimit          int        `json:"cpu_limit"`
	MemoryLimitBytes  int64      `json:"memory_limit_bytes"`
	ScratchLimitBytes int64      `json:"scratch_limit_bytes"`
	PIDLimit          int        `json:"pid_limit"`
	Partial           bool       `json:"partial"`
	CostStatus        string     `json:"cost_status"`
	Note              string     `json:"note"`
	Runs              []RunUsage `json:"runs"`
}

// Usage reads only owned, persisted check receipts. It never starts, stops or
// inspects containers and cannot turn missing accounting into verification proof.
// Input identities come from durable goal operations, not directory discovery.
func (r *Runner) Usage(ctx context.Context, goal, policy string, refs []UsageRef) UsageReport {
	out := UsageReport{CPULimit: CPULimit, MemoryLimitBytes: MemoryLimitBytes, ScratchLimitBytes: ScratchLimitBytes, PIDLimit: PIDLimit,
		CostStatus: "unavailable", Runs: []RunUsage{}, Note: "Elapsed time includes container launch and cleanup; interrupted and running durations are unknown. Resource limits are ceilings, not measured CPU or memory usage. Missing receipts do not establish zero consumption."}
	refs = slices.Clone(refs)
	slices.SortFunc(refs, func(a, b UsageRef) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	refs = slices.CompactFunc(refs, func(a, b UsageRef) bool { return a.ID == b.ID })
	out.Expected = len(refs)
	for _, ref := range refs {
		if ref.Spec.Validate() == nil {
			out.MemoryLimitBytes = max(out.MemoryLimitBytes, ref.Spec.MemoryLimitBytes())
		}
	}
	if r.Workspaces == nil {
		out.Partial = out.Expected > 0
		return out
	}
	var readBytes int64
	for _, ref := range refs {
		if ctx.Err() != nil || readBytes >= maxUsageReadBytes {
			out.Partial = true
			break
		}
		out.Inspected++
		dir, err := r.Workspaces.ValidationDirectory(goal, ref.ID)
		if err != nil {
			out.Invalid++
			continue
		}
		receipt, n, err := readUsageReceipt(dir, maxUsageReadBytes-readBytes)
		readBytes += n
		if errors.Is(err, errUsageReadLimit) {
			out.Inspected--
			out.Partial = true
			break
		}
		if errors.Is(err, os.ErrNotExist) {
			out.Missing++
			continue
		}
		if err != nil || receipt.ID != ref.ID || receipt.Goal != goal || receipt.Operation != ref.Operation || receipt.Policy != policy || !reflect.DeepEqual(receipt.Spec, ref.Spec) || receipt.StartedAt.IsZero() || !slices.Contains([]string{"running", "pass", "fail", "error", "interrupted"}, receipt.State) {
			out.Invalid++
			continue
		}
		v := RunUsage{ID: ref.ID, Operation: ref.Operation, Check: ref.Spec.ID, Member: ref.Spec.Member, State: receipt.State, Stopped: receipt.Stopped, StartedAt: receipt.StartedAt, FinishedAt: receipt.FinishedAt, TimeoutSeconds: ref.Spec.TimeoutSeconds}
		v.MemoryLimitBytes = ref.Spec.MemoryLimitBytes()
		// Recovery's finish timestamp can include a whole daemon outage. Do not
		// pretend that interval was measured container runtime. A backwards clock or
		// a duration beyond the command+cleanup bound is similarly inconclusive.
		elapsed := receipt.FinishedAt.Sub(receipt.StartedAt)
		if receipt.Stopped && slices.Contains([]string{"pass", "fail", "error"}, receipt.State) && !receipt.FinishedAt.IsZero() && elapsed >= 0 && elapsed <= time.Duration(ref.Spec.TimeoutSeconds+30)*time.Second {
			ms := elapsed.Milliseconds()
			v.ElapsedMillis = &ms
			out.ElapsedMillis += ms
		} else {
			out.UnknownDuration++
		}
		out.Runs = append(out.Runs, v)
	}
	out.Partial = out.Partial || out.Missing > 0 || out.Invalid > 0 || out.UnknownDuration > 0
	return out
}

func readUsageReceipt(dir string, remaining int64) (Receipt, int64, error) {
	var out Receipt
	root, err := os.OpenRoot(dir)
	if err != nil {
		return out, 0, err
	}
	defer root.Close()
	st, err := root.Lstat("check.json")
	if err != nil {
		return out, 0, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxUsageReceiptBytes {
		return out, 0, errors.New("invalid check receipt file")
	}
	if st.Size() > remaining {
		return out, 0, errUsageReadLimit
	}
	f, err := root.Open("check.json")
	if err != nil {
		return out, 0, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, min(maxUsageReceiptBytes, remaining)+1))
	if err != nil {
		return out, int64(len(body)), err
	}
	if int64(len(body)) > remaining {
		return out, int64(len(body)), errUsageReadLimit
	}
	if int64(len(body)) > maxUsageReceiptBytes {
		return out, int64(len(body)), errors.New("check receipt too large")
	}
	err = json.Unmarshal(body, &out)
	return out, int64(len(body)), err
}
