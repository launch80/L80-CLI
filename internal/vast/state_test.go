package vast

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, dir, id, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id, "state.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListAndBilling(t *testing.T) {
	dir := t.TempDir()
	now := float64(time.Now().Unix())
	write(t, dir, "a-1", `{"run_id":"a-1","created_at":100,"status":"torn_down","instance_id":11,"dph":2.5}`)
	write(t, dir, "b-2", `{"run_id":"b-2","created_at":`+ftoa(now-3600)+`,"status":"benchmarked","instance_id":22,"dph":1.5,"started_billing_at":`+ftoa(now-3600)+`}`)
	write(t, dir, "c-3", `{"run_id":"c-3","created_at":300,"status":"searched","instance_id":null}`)
	write(t, dir, "junk", `not json`)
	runs, err := List(dir)
	if err != nil || len(runs) != 3 || runs[0].RunID != "b-2" {
		t.Fatalf("runs = %+v %v", runs, err)
	}
	live := Billing(runs)
	if len(live) != 1 || live[0].RunID != "b-2" {
		t.Fatalf("billing = %+v", live)
	}
	msg := live[0].Reminder(time.Now())
	if !strings.Contains(msg, "instance 22") || !strings.Contains(msg, "$1.50/hr") || !strings.Contains(msg, "L80 vast destroy --run b-2") || !strings.Contains(msg, "about $1.50 so far") {
		t.Errorf("reminder = %s", msg)
	}
	if got := Since(runs, time.Unix(int64(now)-60, 0)); len(got) != 0 {
		t.Errorf("since = %+v", got)
	}
	if runs, err := List(filepath.Join(dir, "missing")); err != nil || runs != nil {
		t.Error("missing dir is empty, not an error")
	}
}

func TestRunsDirResolution(t *testing.T) {
	t.Setenv("BBVASTL80_RUNS_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/x/state")
	if RunsDir() != "/x/state/L80/vast/runs" {
		t.Errorf("default = %s", RunsDir())
	}
	t.Setenv("BBVASTL80_RUNS_DIR", "/y/runs")
	if RunsDir() != "/y/runs" {
		t.Errorf("override = %s", RunsDir())
	}
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', 0, 64) }
