package qualitybench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResultsDir(t *testing.T) {
	t.Setenv("L80_QUALITYBENCH_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/x/state")
	if got := ResultsDir(); got != "/x/state/L80/qualitybench" {
		t.Errorf("XDG: %s", got)
	}
	d := t.TempDir()
	t.Setenv("L80_QUALITYBENCH_DIR", filepath.Join(d, "qb"))
	p := ResultsPath("r.json")
	if p != filepath.Join(d, "qb", "r.json") {
		t.Errorf("override: %s", p)
	}
	if info, err := os.Stat(filepath.Dir(p)); err != nil || !info.IsDir() {
		t.Error("ResultsPath creates the directory")
	}
}
