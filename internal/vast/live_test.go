package vast

import "testing"

func TestReconcileDropsInstancesVastNoLongerLists(t *testing.T) {
	id := func(n int64) *int64 { return &n }
	runs := []RunState{
		{RunID: "live", Status: "rented", InstanceID: id(1)},
		{RunID: "stale", Status: "rented", InstanceID: id(2)},
		{RunID: "done", Status: "torn_down", InstanceID: id(3)},
		{RunID: "never", Status: "failed"},
	}
	live, err := ParseLive([]byte("$ bbvastl80 list --json\nnote: x\n[{\"id\": 1, \"status\": \"running\", \"label\": \"l\", \"dph\": 0.3}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	Reconcile(runs, live)
	b := Billing(runs)
	if len(b) != 1 || b[0].RunID != "live" {
		t.Fatalf("billing = %+v", b)
	}
	if runs[1].ShownStatus() != "gone" || runs[0].ShownStatus() != "rented" || runs[2].ShownStatus() != "torn_down" {
		t.Fatalf("statuses: %s %s %s", runs[0].ShownStatus(), runs[1].ShownStatus(), runs[2].ShownStatus())
	}
	if l, err := ParseLive([]byte("[]")); err != nil || len(l) != 0 {
		t.Fatalf("empty list: %v %v", l, err)
	}
	if _, err := ParseLive([]byte("Usage: bbvastl80 list [OPTIONS]\nNo such option: --json")); err == nil {
		t.Fatal("an older bbvastl80 must be an error, not an empty account")
	}
}
