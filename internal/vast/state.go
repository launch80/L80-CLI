// Package vast reads the run state bbvastl80 keeps on disk, so L80 can tell
// the user which rented GPUs may still be billing after a run stops, from
// any working directory.
package vast

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RunState mirrors bbvastl80's state.RunState (runs/<run-id>/state.json).
type RunState struct {
	RunID            string   `json:"run_id"`
	CreatedAt        float64  `json:"created_at"`
	Status           string   `json:"status"`
	SpecName         string   `json:"spec_name,omitempty"`
	InstanceID       *int64   `json:"instance_id"`
	PublicIP         string   `json:"public_ip,omitempty"`
	Endpoint         string   `json:"endpoint,omitempty"`
	TunnelPID        *int     `json:"tunnel_pid,omitempty"`
	ResultsPath      string   `json:"results_path,omitempty"`
	PublishURL       string   `json:"publish_url,omitempty"`
	DPH              *float64 `json:"dph"`
	StartedBillingAt *float64 `json:"started_billing_at"`

	// Dir is the run directory, filled in by List.
	Dir string `json:"-"`
}

// MayBeBilling reports whether the run rented an instance it has not torn
// down. That is the state in which a box keeps costing money.
func (r RunState) MayBeBilling() bool {
	return r.InstanceID != nil && r.Status != "torn_down"
}

// Created is CreatedAt as a time.
func (r RunState) Created() time.Time { return unix(r.CreatedAt) }

// BillingFor is how long the instance has been billing, as of now.
func (r RunState) BillingFor(now time.Time) time.Duration {
	if r.StartedBillingAt == nil {
		return 0
	}
	return now.Sub(unix(*r.StartedBillingAt)).Round(time.Minute)
}

// Reminder is the line L80 prints for a run that may still be billing.
func (r RunState) Reminder(now time.Time) string {
	cost := ""
	if r.DPH != nil {
		cost = fmt.Sprintf(" at $%.2f/hr", *r.DPH)
		if d := r.BillingFor(now); d > 0 {
			cost += fmt.Sprintf(" (about $%.2f so far over %s)", *r.DPH*d.Hours(), d)
		}
	}
	return fmt.Sprintf("instance %d from run %s may still be billing%s; stop it with: L80 vast destroy --run %s",
		*r.InstanceID, r.RunID, cost, r.RunID)
}

func unix(f float64) time.Time {
	sec := int64(f)
	return time.Unix(sec, int64((f-float64(sec))*1e9))
}

// DefaultRunsDir is where L80 keeps bbvastl80 runs unless BBVASTL80_RUNS_DIR
// says otherwise: $XDG_STATE_HOME/L80/vast/runs, else ~/.local/state/L80/vast/runs.
func DefaultRunsDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "L80", "vast", "runs")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "runs"
	}
	return filepath.Join(home, ".local", "state", "L80", "vast", "runs")
}

// RunsDir resolves the runs directory the child will use.
func RunsDir() string {
	if d := os.Getenv("BBVASTL80_RUNS_DIR"); d != "" {
		if abs, err := filepath.Abs(d); err == nil {
			return abs
		}
		return d
	}
	return DefaultRunsDir()
}

// List reads every runs/<id>/state.json under dir, newest first. A missing
// directory is an empty list; unreadable files are skipped.
func List(dir string) ([]RunState, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []RunState
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name(), "state.json")
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var r RunState
		if json.Unmarshal(data, &r) != nil || r.RunID == "" {
			continue
		}
		r.Dir = filepath.Join(dir, e.Name())
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// Billing filters runs to those that may still be billing.
func Billing(runs []RunState) []RunState {
	var out []RunState
	for _, r := range runs {
		if r.MayBeBilling() {
			out = append(out, r)
		}
	}
	return out
}

// Since filters runs created at or after t.
func Since(runs []RunState, t time.Time) []RunState {
	var out []RunState
	for _, r := range runs {
		if !r.Created().Before(t.Add(-2 * time.Second)) {
			out = append(out, r)
		}
	}
	return out
}
