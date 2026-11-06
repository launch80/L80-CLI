package tui

import (
	"context"
	"encoding/json"
	"github.com/launch80/L80-CLI/internal/qualitybench"
	"github.com/launch80/L80-CLI/internal/vast"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launch80/L80-CLI/internal/discover"
	"github.com/launch80/L80-CLI/internal/models"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
)

func key(r rune) input.Key        { return input.Key{Code: input.Rune, Rune: r} }
func code(c input.Code) input.Key { return input.Key{Code: c} }

// openQuality jumps to the QualityBench screen with fake servers on the machine.
func openQuality(t *testing.T, eps []discover.Endpoint) *Console {
	t.Helper()
	c := newTestConsole(t)
	c.qb.scan = func(context.Context) []discover.Endpoint { return eps }
	feed(c, SizeMsg{120, 44})
	feed(c, key('8'))
	if c.scr != scrQuality || !c.paneFocus {
		t.Fatalf("scr=%v focus=%v", c.scr, c.paneFocus)
	}
	return c
}

func screen(t *testing.T, c *Console) string {
	t.Helper()
	return style.Strip(checkFrame(t, c.View(120, 44), 120, 44))
}

var swap = []discover.Endpoint{{URL: "http://127.0.0.1:8000/v1", Engine: discover.EngineLlamaSwap, Models: []discover.Model{
	{ID: "Qwen/Qwen3-0.6B", Backend: "vllm", GPU: "b65"},
	{ID: "Launch80/Qwopus-Int4-baked", Backend: "vllm", GPU: "b65", Loaded: true},
}}}

func TestQualityScreenScopeAndChecklist(t *testing.T) {
	c := openQuality(t, swap)
	view := screen(t, c)
	for _, want := range []string{"QualityBench", "Scope", "‹Full›", "Target", "‹Local GPUs›", "Run QualityBench", "about 2h41 with thinking off"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "[x] GSM8K") {
		t.Error("the checklist shows only for Custom")
	}
	feed(c, code(input.Right))
	feed(c, code(input.Right))
	view = screen(t, c)
	for _, want := range []string{"‹Custom›", "Tasks", "[x] GSM8K", "1319 items", "19m", "[ ] Spec parity", "9 tasks"} {
		if !strings.Contains(view, want) {
			t.Errorf("custom missing %q:\n%s", want, view)
		}
	}
	// Into the checklist: untick GSM8K, and the estimate follows.
	feed(c, code(input.Down))
	if c.qb.focus != qsTasks {
		t.Fatalf("focus %v", c.qb.focus)
	}
	feed(c, key(' '))
	if view = screen(t, c); !strings.Contains(view, "[ ] GSM8K") || !strings.Contains(view, "8 tasks") {
		t.Errorf("untick:\n%s", view)
	}
	// The custom form is taller than a small terminal: it scrolls to keep the
	// focused row in view and never overflows the frame.
	c.qb.focus = qsSubmit
	for _, sz := range [][2]int{{80, 24}, {60, 16}} {
		feed(c, SizeMsg{sz[0], sz[1]})
		if got := style.Strip(checkFrame(t, c.View(sz[0], sz[1]), sz[0], sz[1])); !strings.Contains(got, "Run QualityBench") {
			t.Errorf("%dx%d: submit scrolled away:\n%s", sz[0], sz[1], got)
		}
	}
}

func TestQualityCustomNeedsATask(t *testing.T) {
	c := openQuality(t, swap)
	c.qb.scope.Choice = scopeCustom
	for i := range c.qb.tasks.Items {
		c.qb.tasks.Items[i].On = false
	}
	c.qb.focus = qsSubmit
	feed(c, code(input.Enter))
	if c.qb.job != nil || !c.bad || !strings.Contains(c.flash, "tick at least one task") {
		t.Errorf("job=%v flash=%q", c.qb.job, c.flash)
	}
}

func TestQualityLocalPickers(t *testing.T) {
	c := openQuality(t, swap)
	view := screen(t, c)
	for _, want := range []string{"http://127.0.0.1:8000/v1", "llama-swap · 2 models", "Other…", "Launch80/Qwopus-Int4-baked", "not loaded: the first request pays the load time"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	ep, model, err := c.qb.localTarget()
	if err != nil || ep != "http://127.0.0.1:8000/v1" || model != "Launch80/Qwopus-Int4-baked" {
		t.Errorf("loaded model is the default pick: %q %q %v", ep, model, err)
	}
	// Other… opens a URL field, and a model id field with it.
	c.qb.focus = qsEndpoint
	feed(c, code(input.Down))
	secs := c.qb.sections()
	if !strings.Contains(screen(t, c), "URL") || secs[4] != qsEndpointURL || secs[5] != qsModelID {
		t.Errorf("other: %v", secs)
	}
	if _, _, err := c.qb.localTarget(); err == nil {
		t.Error("an empty URL must be refused")
	}
}

func TestQualityNoServerSaysSo(t *testing.T) {
	c := openQuality(t, nil)
	if view := screen(t, c); !strings.Contains(view, "no server found on the usual ports") {
		t.Errorf("view:\n%s", view)
	}
}

func TestQualityRunShowsProgressAndSummary(t *testing.T) {
	fixture, _ := filepath.Abs("../qualitybench/testdata/results-bf16.json")
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$*\" > " + argsFile + "\n" +
		"echo 'qb:progress {\"task\":\"gsm8k\",\"done\":10,\"total\":1319}'\necho 'loading datasets'\n" +
		"echo 'qb:progress {\"task\":\"gsm8k\",\"state\":\"scored\",\"correct\":1147,\"total\":1319}'\n" +
		"out=''; while [ $# -gt 0 ]; do [ \"$1\" = --out ] && out=$2; shift; done\ncp " + fixture + " \"$out\"\necho '<html>' > \"${out%.json}.html\"\n"
	os.WriteFile(filepath.Join(dir, "qualitybench"), []byte(script), 0o755)
	t.Setenv("L80_QUALITYBENCH", filepath.Join(dir, "qualitybench"))
	wd, _ := os.Getwd()
	os.Chdir(t.TempDir())
	defer os.Chdir(wd)
	var opened string
	old := openFile
	openFile = func(p string) error { opened = p; return nil }
	defer func() { openFile = old }()

	c := openQuality(t, swap)
	c.qb.scope.Choice = scopeCustom
	for i := range c.qb.tasks.Items {
		c.qb.tasks.Items[i].On = i == 0
	}
	c.qb.thinking.Choice = 2
	c.qb.focus = qsSubmit
	feed(c, code(input.Enter))
	if c.qb.job == nil {
		t.Fatalf("no job; flash %q", c.flash)
	}
	deadline := time.Now().Add(5 * time.Second)
	for c.qb.job.running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := os.ReadFile(argsFile)
	for _, want := range []string{"--endpoint http://127.0.0.1:8000/v1", "--model Launch80/Qwopus-Int4-baked", "--tasks gsm8k", "--thinking on"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("tool argv %q missing %q", got, want)
		}
	}
	view := screen(t, c)
	for _, want := range []string{"GSM8K", "1319/1319", "scored 87.0%", "loading datasets", "GSM8K: 87.0% (1147/1319)", "report", "p publish", "o open report"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "qb:progress") {
		t.Error("progress lines belong in the table, not the log")
	}
	feed(c, key('o'))
	if !strings.HasSuffix(opened, ".html") {
		t.Errorf("opened %q", opened)
	}
	feed(c, key('p'))
	if !c.bad || !strings.Contains(c.flash, "needs a token") {
		t.Errorf("flash = %q", c.flash)
	}
}

func TestQualityToolUnavailable(t *testing.T) {
	t.Setenv("L80_QUALITYBENCH", "")
	t.Setenv("PATH", t.TempDir())
	c := openQuality(t, swap)
	c.qb.focus = qsSubmit
	feed(c, code(input.Enter))
	deadline := time.Now().Add(5 * time.Second)
	for c.qb.job != nil && c.qb.job.running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !c.bad || !strings.Contains(c.flash, "E_TOOL_UNAVAILABLE") || !strings.Contains(c.flash, "L80_QUALITYBENCH") {
		t.Errorf("flash = %q", c.flash)
	}
}

func TestQualityVastPlansButDoesNotRent(t *testing.T) {
	c := newTestConsole(t)
	c.qb.sources = func(*Console) []models.Model {
		return []models.Model{{ID: "Qwen/Qwen3-8B", Source: models.SrcRecent, Detail: "rented before"}, {ID: "Qwopus-dir", Source: models.SrcLocal}}
	}
	var asked string
	c.qb.find = func(_ context.Context, q string) ([]models.Model, error) {
		asked = q
		return []models.Model{{ID: "Jackrong/Qwopus-dir", Source: models.SrcSearch, Detail: "12 downloads"}}, nil
	}
	feed(c, SizeMsg{120, 44})
	feed(c, key('8'))
	c.qb.focus = qsTarget
	feed(c, code(input.Right))
	view := screen(t, c)
	for _, want := range []string{"‹Rent via Vast›", "Engine", "‹vLLM›", "Search", "Qwen/Qwen3-8B", "recent · rented before", "not a repo id", "Set up the rental"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	// A ~/models directory name is looked up on Hugging Face.
	c.qb.focus = qsVastModel
	feed(c, code(input.Down))
	feed(c, code(input.Enter))
	if asked != "Qwopus-dir" || !strings.Contains(screen(t, c), "Jackrong/Qwopus-dir") {
		t.Errorf("search %q:\n%s", asked, screen(t, c))
	}
	c.qb.vmods.Cursor = 0
	c.qb.engine.Choice = 1
	c.qb.focus = qsSubmit
	feed(c, code(input.Enter))
	// The rental is handed to Rent with the model and a QualityBench step.
	if c.scr != scrVast || c.vast.tab != 2 || c.vast.wiz.page != pgHardware {
		t.Fatalf("screen %v tab %d page %d", c.scr, c.vast.tab, c.vast.wiz.page)
	}
	if c.vast.wiz.val(pgModel, "model") != "Jackrong/Qwopus-dir" || c.vast.wiz.val(pgModel, "engine") != "llamacpp" {
		t.Fatalf("model %q engine %q", c.vast.wiz.val(pgModel, "model"), c.vast.wiz.val(pgModel, "engine"))
	}
	if h := c.vast.wiz.handed; len(h) != 1 || h[0].Type != vast.StepQualityBench {
		t.Fatalf("handed %+v", h)
	}
	if c.vast.job != nil || c.qb.job != nil || c.dialog != nil {
		t.Error("handing over must not start anything")
	}
}

func TestQualityCompareTwoRuns(t *testing.T) {
	fixture, _ := filepath.Abs("../qualitybench/testdata/results-bf16.json")
	dir := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(dir, name+".json")
		var d map[string]any
		data, _ := os.ReadFile(fixture)
		json.Unmarshal(data, &d)
		delete(d, "arms") // a plain run, not a comparison
		data, _ = json.Marshal(d)
		os.WriteFile(p, data, 0o644)
		os.Mkdir(filepath.Join(dir, name+".qb"), 0o755)
		return p
	}
	ref, test := mk("bf16"), mk("int4")
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$*\" > " + argsFile + "\n" +
		"out=''; while [ $# -gt 0 ]; do [ \"$1\" = --out ] && out=$2; shift; done\ncp " + fixture + " \"$out\"\necho '<html>' > \"${out%.json}.html\"\necho 'KLD 0.01'\n"
	os.WriteFile(filepath.Join(dir, "qualitybench"), []byte(script), 0o755)
	t.Setenv("L80_QUALITYBENCH", filepath.Join(dir, "qualitybench"))

	c := openQuality(t, swap)
	c.qb.findResults = func(*Console) []qualitybench.ResultFile { return qualitybench.FindResults([]string{ref, test}) }
	drain(c, c.qb.refreshResults(c), 0)
	c.qb.mode.Choice = modeCompare
	if secs := c.qb.sections(); len(secs) != 5 || secs[1] != qsCmpRef {
		t.Fatalf("sections %v", secs)
	}
	c.qb.cmpRef.Cursor, c.qb.cmpTest.Cursor = 0, 1
	refPath, testPath := listPath(&c.qb.cmpRef), listPath(&c.qb.cmpTest)
	c.qb.focus = qsSubmit
	feed(c, code(input.Enter))
	if c.qb.job == nil {
		t.Fatalf("no job; flash %q", c.flash)
	}
	deadline := time.Now().Add(5 * time.Second)
	for c.qb.job.running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	feed(c, jobDone{j: c.qb.job, result: nil})
	got, _ := os.ReadFile(argsFile)
	want := "compare " + refPath + " " + testPath
	if !strings.Contains(string(got), want) || !strings.Contains(string(got), "-vs-") {
		t.Errorf("argv %q, want %q", got, want)
	}
	if !c.qb.comparing {
		t.Error("a compare job")
	}
}
