package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVastSpec(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("BBVASTL80_RUNS_DIR", "")
	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := runVast(env{stdout: &out, stderr: &errOut}, append([]string{"spec"}, args...))
		return code, out.String(), errOut.String()
	}
	if code, _, errOut := run(); code == 0 || !strings.Contains(errOut, "--model is required") {
		t.Errorf("no model: %d %s", code, errOut)
	}
	if code, out, _ := run("--model", "Qwen/Qwen3-8B", "--gpu", "RTX_4090", "--print"); code != 0 || !strings.Contains(out, `names: ["RTX 4090"]`) {
		t.Errorf("print: %d %s", code, out)
	}
	dest := filepath.Join(t.TempDir(), "run.yaml")
	if code, out, errOut := run("--model", "Qwen/Qwen3-8B", "--max-dph", "0.8", "-o", dest); code != 0 || !strings.Contains(out, "vast run --spec "+dest) {
		t.Fatalf("write: %d %s %s", code, out, errOut)
	}
	if data, _ := os.ReadFile(dest); !strings.Contains(string(data), "max_dph: 0.8") {
		t.Errorf("spec:\n%s", data)
	}
	if code, _, errOut := run("--model", "Qwen/Qwen3-8B", "-o", dest); code == 0 || !strings.Contains(errOut, "--force") {
		t.Errorf("overwrite: %d %s", code, errOut)
	}
	if code, _, errOut := run("--model", "Qwen/Qwen3-8B", "--max-dph", "0"); code == 0 || !strings.Contains(errOut, "price ceiling") {
		t.Errorf("bad dph: %d %s", code, errOut)
	}
}
