package qualitybench

import (
	"os"
	"path/filepath"
)

// ResultsDir is where runs write their results unless --out says otherwise,
// so the console finds them whichever directory it starts in:
// $L80_QUALITYBENCH_DIR, else $XDG_STATE_HOME/L80/qualitybench, else
// ~/.local/state/L80/qualitybench (beside the rental runs).
func ResultsDir() string {
	if d := os.Getenv("L80_QUALITYBENCH_DIR"); d != "" {
		if abs, err := filepath.Abs(d); err == nil {
			return abs
		}
		return d
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "L80", "qualitybench")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "L80-qualitybench")
	}
	return filepath.Join(home, ".local", "state", "L80", "qualitybench")
}

// ResultsPath is name inside ResultsDir, creating the directory.
func ResultsPath(name string) string {
	dir := ResultsDir()
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, name)
}
