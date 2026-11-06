package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/launch80/L80-CLI/internal/vast"
)

func staleRun(t *testing.T, dir, id string, inst int) {
	t.Helper()
	d := filepath.Join(dir, id)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"run_id":"` + id + `","created_at":1,"status":"rented","instance_id":` + itoaTest(inst) + `,"dph":3.61,"started_billing_at":1}`
	if err := os.WriteFile(filepath.Join(d, "state.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoaTest(n int) string { return strconv.Itoa(n) }

func rentView(t *testing.T, c *Console) string {
	t.Helper()
	m := c.vast.loadRuns(c)()
	c.vast.update(c, m)
	lines, _ := c.vast.view(c, 120, 40)
	return strings.Join(lines, "\n")
}

func TestRentScreenChecksStaleRunsWithVast(t *testing.T) {
	c := newTestConsole(t)
	staleRun(t, c.d.RunsDir, "old-a", 1)
	staleRun(t, c.d.RunsDir, "old-b", 2)
	staleRun(t, c.d.RunsDir, "live-c", 3)
	orig := liveCheck
	t.Cleanup(func() { liveCheck = orig })

	// Vast lists only instance 3: the other two were destroyed elsewhere.
	liveCheck = func(context.Context, []string) (vast.Live, error) { return vast.Live{3: true}, nil }
	v := rentView(t, c)
	if !strings.Contains(v, "1 instance(s) still billing on Vast") {
		t.Fatalf("want one billing instance:\n%s", v)
	}
	if strings.Count(v, "gone") != 2 {
		t.Fatalf("the two stale runs should show as gone:\n%s", v)
	}

	// Nothing on the account: no banner at all.
	liveCheck = func(context.Context, []string) (vast.Live, error) { return vast.Live{}, nil }
	if v := rentView(t, c); strings.Contains(v, "billing") {
		t.Fatalf("no instance is billing, yet:\n%s", v)
	}

	// Vast can't be asked: keep the warning, and say why it is unsure.
	liveCheck = func(context.Context, []string) (vast.Live, error) { return nil, errors.New("no Vast key") }
	v = rentView(t, c)
	if !strings.Contains(v, "3 instance(s) may still be billing") || !strings.Contains(v, "no Vast key") {
		t.Fatalf("an unchecked run must still warn, with the reason:\n%s", v)
	}
}
