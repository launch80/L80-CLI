package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/tui/input"
)

// A QualityBench run without the logprobs task (BFCL only) is publishable,
// and the Jobs screen offers it once the run is done: p asks, then publishes.
func TestJobsPublishesAFinishedQualityBenchRun(t *testing.T) {
	data, err := os.ReadFile("../qualitybench/testdata/results-bf16.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	json.Unmarshal(data, &doc)
	delete(doc, "logprobs")
	delete(doc, "eval_meta")
	var bfcl []any
	for _, x := range doc["tasks"].([]any) {
		if x.(map[string]any)["id"] == "bfcl" {
			bfcl = append(bfcl, x)
		}
	}
	doc["tasks"] = bfcl
	data, _ = json.Marshal(doc)

	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/artifacts" {
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &posted)
			w.Write([]byte(`{"ok":true,"data":{"guid":"g1","url":"https://launch80.com/a/g1","template_id":"bench.qualitybench.v1"}}`))
			return
		}
		w.Write([]byte(`{"ok":true,"data":{}}`))
	}))
	defer srv.Close()

	c := openQuality(t, swap)
	c.d.Config = config.Config{BaseURL: srv.URL, Token: "L80_k1_secret", KeyID: "k1"}
	c.d.Client = api.NewClient(srv.URL, c.d.Config.Token, "test")
	j := newJob("QualityBench Launch80/Qwopus-Int4-baked")
	c.qb.job, c.qb.phase = j, 1
	c.track(j)
	c.running++
	res := &app.QualityResult{Path: "/tmp/qb.json", Report: "/tmp/qb.html", Data: data}
	c.paneFocus = false
	jumpTo(t, c, scrJobs) // the run finishes while another screen is open
	feed(c, jobDone{j: j, result: res})
	if !strings.Contains(c.flash, "p publishes it to launch80") {
		t.Errorf("done flash = %q", c.flash)
	}
	feed(c, code(input.Down)) // any key clears the flash and shows the key hints
	if v := screen(t, c); !strings.Contains(v, "p publish to launch80") {
		t.Fatalf("jobs offers no publish:\n%s", v)
	}
	feed(c, key('p'))
	if c.dialog == nil || c.dialog.Title != "Publish" || c.dialog.Choice != 1 {
		t.Fatalf("publish must ask first, cancel focused: %+v", c.dialog)
	}
	feed(c, input.Key{Code: input.Esc})
	if posted != nil {
		t.Fatal("published without a yes")
	}
	feed(c, key('p'))
	feed(c, code(input.Left))
	feed(c, code(input.Enter))
	if posted == nil || posted["$template"] != "bench.qualitybench.v1" {
		t.Fatalf("posted %v; flash %q", posted, c.flash)
	}
	if _, ok := posted["logprobs"]; ok {
		t.Error("no logprobs to send")
	}
	if j.url != "https://launch80.com/a/g1" || c.qb.url != j.url || !strings.Contains(c.flash, "published: https://launch80.com/a/g1") {
		t.Errorf("url job %q screen %q flash %q", j.url, c.qb.url, c.flash)
	}
	v := screen(t, c)
	if strings.Contains(v, "p publish") {
		t.Errorf("published job still offers publish:\n%s", v)
	}
	feed(c, code(input.Enter))
	if v := screen(t, c); !strings.Contains(v, "published: https://launch80.com/a/g1") {
		t.Errorf("job log view does not show the page:\n%s", v)
	}
}
