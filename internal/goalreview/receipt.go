// Package goalreview isolates goal specialists from the host, workers, upstream
// credentials and authoritative Git stores. Receipts bind results to the exact
// review input and require confirmed process cleanup before they can pass gates.
package goalreview

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/reviewprofile"
	"github.com/sushidev-team/lola/internal/usage"
)

type Receipt struct {
	Usage      *usage.Observation   `json:"usage,omitempty"`
	Goal       string               `json:"goal"`
	ID         string               `json:"id"`
	Key        string               `json:"key"`
	Agent      string               `json:"agent"`
	Model      string               `json:"model,omitempty"`
	Image      string               `json:"image"`
	ImageID    string               `json:"image_id"`
	State      string               `json:"state"`
	Stopped    bool                 `json:"stopped"`
	StartedAt  time.Time            `json:"started_at"`
	FinishedAt time.Time            `json:"finished_at,omitempty"`
	Result     reviewprofile.Result `json:"result"`
	Error      string               `json:"error,omitempty"`
}

// Usage recovers numeric telemetry separately from the review verdict. A
// failed or interrupted review may still have consumed model resources.
func (r Runner) Usage(goalID, id string) (*usage.Observation, error) {
	old, _, err := r.load(goalID, id)
	if err != nil {
		return nil, err
	}
	if old.Usage == nil {
		return nil, nil
	}
	if err := goal.ValidateUsageObservation(*old.Usage); err != nil {
		return nil, err
	}
	if old.Usage.Agent != old.Agent || old.Usage.At.Before(old.StartedAt) {
		return nil, errors.New("review usage ownership mismatch")
	}
	return old.Usage, nil
}

func (r Runner) load(goal, id string) (Receipt, string, error) {
	if r.Workspaces == nil {
		return Receipt{}, "", errors.New("review workspace manager unavailable")
	}
	dir, err := r.Workspaces.ReviewDirectory(goal, id)
	if err != nil {
		return Receipt{}, "", err
	}
	path := filepath.Join(dir, "review.json")
	info, err := os.Lstat(path)
	if err != nil {
		return Receipt{}, path, err
	}
	if !info.Mode().IsRegular() || info.Size() > 128<<10 {
		return Receipt{}, path, errors.New("invalid review receipt file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, path, err
	}
	var receipt Receipt
	if err := json.Unmarshal(b, &receipt); err != nil {
		return receipt, path, err
	}
	if receipt.Goal != goal || receipt.ID != id || receipt.Key == "" || !imageID.MatchString(receipt.ImageID) || receipt.StartedAt.IsZero() {
		return receipt, path, errors.New("review receipt ownership mismatch")
	}
	return receipt, path, nil
}

func save(path string, r Receipt) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".review-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Evidence is read-only: publication never repairs or relaunches a review.
func (r Runner) Evidence(goal, id, key string, result reviewprofile.Result) error {
	old, _, err := r.load(goal, id)
	if err != nil {
		return err
	}
	if old.Key != key || !old.Result.Complete || old.FinishedAt.IsZero() || old.State != "complete" || !old.Stopped || old.FinishedAt.Before(old.StartedAt) || old.Agent != r.Agent || old.Model != r.Model || old.Image != r.Image || !reflect.DeepEqual(old.Result, result) {
		return errors.New("isolated specialist evidence is incomplete or changed")
	}
	return nil
}
