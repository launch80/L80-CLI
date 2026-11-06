package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/qualitybench"
)

const toolEstimateJSON = `{"source":"probe","total_min":38.2,"range_min":[27,57],"tasks":[{"id":"gsm8k","label":"GSM8K","min":6.1,"items":132,"source":"probe"},{"id":"ifeval","label":"IFEval","min":3,"items":55,"source":"probe"}],"probe":{"decode_tps":2140,"prefill_tps":9800,"speed":1.9},"setup_min":0,"basis":"measured on this server just now"}`

// fakeQuality020 behaves like QualityBench 0.2.0: it answers `estimate` and
// records every argv.
func fakeQuality020(t *testing.T) (argsFile string) {
	t.Helper()
	argsFile = filepath.Join(t.TempDir(), "args")
	fakeTool(t, "L80_QUALITYBENCH", `echo "$*" >> `+argsFile+`
if [ "$1" = estimate ]; then echo '`+toolEstimateJSON+`'; exit 0; fi
while [ $# -gt 0 ]; do [ "$1" = --out ] && out=$2; shift; done
cp `+qualityFixture(t)+` "$out"
echo "<html></html>" > "${out%.json}.html"
`)
	return argsFile
}

// fakeQuality011 is the pinned 0.1.1: `estimate` is an unknown command.
func fakeQuality011(t *testing.T) (argsFile string) {
	t.Helper()
	argsFile = filepath.Join(t.TempDir(), "args")
	fakeTool(t, "L80_QUALITYBENCH", `echo "$*" >> `+argsFile+`
if [ "$1" = estimate ]; then echo "qualitybench: error: argument cmd: invalid choice: 'estimate'" >&2; exit 2; fi
while [ $# -gt 0 ]; do [ "$1" = --out ] && out=$2; shift; done
cp `+qualityFixture(t)+` "$out"
echo "<html></html>" > "${out%.json}.html"
`)
	return argsFile
}

func isolateSettings(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("L80_QUALITYBENCH_CONFIRM", "")
}

func withTTY(t *testing.T, answer string) {
	t.Helper()
	oldIn, oldErr, oldPrompt := stdinIsTTY, stderrIsTTY, promptIn
	stdinIsTTY = func() bool { return true }
	stderrIsTTY = func() bool { return false }
	promptIn = strings.NewReader(answer)
	t.Cleanup(func() { stdinIsTTY, stderrIsTTY, promptIn = oldIn, oldErr, oldPrompt })
}

func runLines(argsFile string) []string {
	got, _ := os.ReadFile(argsFile)
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(got)), "\n") {
		if strings.HasPrefix(l, "run ") {
			out = append(out, l)
		}
	}
	return out
}

func TestGateAsksOnATerminalAndStopsOnNo(t *testing.T) {
	isolateSettings(t)
	argsFile := fakeQuality020(t)
	withTTY(t, "n\n")
	out := filepath.Join(t.TempDir(), "r.json")
	code, _, stderr := runCmd(t, runQualitybench, "--endpoint", "http://127.0.0.1:9/v1", "--model", "M", "--out", out, "--no-publish")
	if code != 0 || !strings.Contains(stderr, "Start QualityBench (38 min)? [y/N]") || !strings.Contains(stderr, "not started") {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "about 38 min (27–57 min), measured on this server just now") || !strings.Contains(stderr, "GSM8K 6 · IFEval 3") {
		t.Errorf("estimate not shown:\n%s", stderr)
	}
	if r := runLines(argsFile); len(r) != 0 {
		t.Errorf("ran after a no: %q", r)
	}
}

func TestGateYesRunsAndPassesYesToANewTool(t *testing.T) {
	isolateSettings(t)
	argsFile := fakeQuality020(t)
	withTTY(t, "y\n")
	out := filepath.Join(t.TempDir(), "r.json")
	code, _, stderr := runCmd(t, runQualitybench, "--endpoint", "http://127.0.0.1:9/v1", "--model", "M", "--out", out, "--no-publish")
	r := runLines(argsFile)
	if code != 0 || len(r) != 1 || !strings.HasSuffix(r[0], " --yes") {
		t.Fatalf("exit %d runs %q\n%s", code, r, stderr)
	}
}

func TestGateSkippedBySettingAndFlag(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T) []string{
		"never":    func(t *testing.T) []string { config.Set(config.KeyQualityConfirm, "never"); return nil },
		"over:2h":  func(t *testing.T) []string { config.Set(config.KeyQualityConfirm, "over:2h"); return nil },
		"env":      func(t *testing.T) []string { t.Setenv("L80_QUALITYBENCH_CONFIRM", "never"); return nil },
		"--yes":    func(t *testing.T) []string { return []string{"--yes"} },
		"no stdin": func(t *testing.T) []string { stdinIsTTY = func() bool { return false }; return nil },
	} {
		t.Run(name, func(t *testing.T) {
			isolateSettings(t)
			argsFile := fakeQuality020(t)
			withTTY(t, "") // a question would read EOF and refuse
			extra := setup(t)
			out := filepath.Join(t.TempDir(), "r.json")
			args := append([]string{"--endpoint", "http://127.0.0.1:9/v1", "--model", "M", "--out", out, "--no-publish"}, extra...)
			code, _, stderr := runCmd(t, runQualitybench, args...)
			if code != 0 || strings.Contains(stderr, "[y/N]") || len(runLines(argsFile)) != 1 {
				t.Fatalf("exit %d\n%s", code, stderr)
			}
		})
	}
}

func TestGateOverAsksForLongerRuns(t *testing.T) {
	isolateSettings(t)
	config.Set(config.KeyQualityConfirm, "over:30m") // the estimate's upper bound is 57 min
	fakeQuality020(t)
	withTTY(t, "n\n")
	code, _, stderr := runCmd(t, runQualitybench, "--endpoint", "http://127.0.0.1:9/v1", "--model", "M",
		"--out", filepath.Join(t.TempDir(), "r.json"), "--no-publish")
	if code != 0 || !strings.Contains(stderr, "[y/N]") {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
}

func TestOldToolFallsBackToTheReferenceTableWithoutYes(t *testing.T) {
	isolateSettings(t)
	argsFile := fakeQuality011(t)
	withTTY(t, "y\n")
	code, _, stderr := runCmd(t, runQualitybench, "--endpoint", "http://127.0.0.1:9/v1", "--model", "M", "--quick",
		"--out", filepath.Join(t.TempDir(), "r.json"), "--no-publish")
	r := runLines(argsFile)
	if code != 0 || len(r) != 1 || strings.Contains(r[0], "--yes") {
		t.Fatalf("exit %d runs %q\n%s", code, r, stderr)
	}
	if !strings.Contains(stderr, "on the reference run") || !strings.Contains(stderr, "no estimate command") {
		t.Errorf("no fallback note:\n%s", stderr)
	}
}

func TestEstimateOnlyJSON(t *testing.T) {
	isolateSettings(t)
	argsFile := fakeQuality020(t)
	code, stdout, stderr := runCmd(t, runQualitybench, "--endpoint", "http://127.0.0.1:9/v1", "--model", "M", "--estimate-only", "--json")
	if code != 0 || !strings.Contains(stdout, `"total_min": 38.2`) || !strings.Contains(stdout, `"source": "probe"`) {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	if len(runLines(argsFile)) != 0 {
		t.Error("--estimate-only ran the suite")
	}
}

func TestStatusWriterRewritesOneLine(t *testing.T) {
	tr := qualitybench.NewTracker([]string{"gsm8k", "ifeval"})
	var buf bytes.Buffer
	w := newStatusWriter(&buf, tr)
	w.now = func() time.Time { return time.Unix(0, 0) }
	w.Write([]byte("[log] gsm8k: start\nqb:progress {\"task\":\"gsm8k\",\"done\":10,\"total\":100}\n"))
	w.Write([]byte("qb:progress {\"task\":\"gsm8k\",\"state\":\"scored\",\"correct\":70,\"total\":100}\n"))
	w.Close()
	s := buf.String()
	if strings.Contains(s, "qb:progress") || !strings.Contains(s, "[log] gsm8k: start\n") || !strings.Contains(s, "\r\033[K  ") || !strings.Contains(s, "GSM8K 10/100") {
		t.Errorf("%q", s)
	}
}

func TestSettingsCommand(t *testing.T) {
	isolateSettings(t)
	if code, out, _ := runCmd(t, runSettings, "get", "qualitybench.confirm"); code != 0 || strings.TrimSpace(out) != "always" {
		t.Errorf("default: %d %q", code, out)
	}
	if code, _, errOut := runCmd(t, runSettings, "set", "qualitybench.confirm", "sometimes"); code != 2 {
		t.Errorf("bad value accepted: %d %s", code, errOut)
	}
	if code, _, _ := runCmd(t, runSettings, "set", "qualitybench.confirm", "over:45m"); code != 0 {
		t.Fatal("set failed")
	}
	if code, out, _ := runCmd(t, runSettings, "list", "--json"); code != 0 || !strings.Contains(out, `"value": "over:45m"`) || !strings.Contains(out, `"source": "file"`) {
		t.Errorf("list: %s", out)
	}
	t.Setenv("L80_QUALITYBENCH_CONFIRM", "never")
	if _, out, _ := runCmd(t, runSettings, "get", "qualitybench.confirm"); strings.TrimSpace(out) != "never" {
		t.Errorf("env did not win: %q", out)
	}
	if code, _, _ := runCmd(t, runSettings, "get", "nope"); code != 2 {
		t.Error("unknown key accepted")
	}
}
