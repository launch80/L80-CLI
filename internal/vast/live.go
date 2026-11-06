package vast

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Live is the set of instance ids Vast lists for the account right now.
type Live map[int64]bool

// ParseLive reads the output of `bbvastl80 list --json` (bbvastl80 0.2.2+):
// a JSON array of {"id", "status", "label", "dph"}. Lines before it (L80's
// own "$ command" echo, notes) are skipped.
func ParseLive(out []byte) (Live, error) {
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		l := bytes.TrimSpace(lines[i])
		if !bytes.HasPrefix(l, []byte("[")) {
			continue
		}
		var rows []struct {
			ID *int64 `json:"id"`
		}
		if err := json.Unmarshal(l, &rows); err != nil {
			return nil, err
		}
		live := Live{}
		for _, r := range rows {
			if r.ID != nil {
				live[*r.ID] = true
			}
		}
		return live, nil
	}
	return nil, errors.New("no instance list in the output (bbvastl80 0.2.2 or later prints one with list --json)")
}

// Reconcile marks the runs whose instance Vast no longer lists as Gone, so
// they stop counting as billing. A run's state file says "rented" until
// bbvastl80 tears the box down itself; a box destroyed any other way (the
// Vast website, another machine, a crash before teardown) leaves it behind.
func Reconcile(runs []RunState, live Live) {
	for i := range runs {
		r := &runs[i]
		if r.InstanceID != nil && r.Status != "torn_down" && !live[*r.InstanceID] {
			r.Gone = true
		}
	}
}
