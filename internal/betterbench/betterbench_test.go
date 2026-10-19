package betterbench

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
)

// Reference values computed with numpy on the same sample (see testdata
// generator): percentile method "linear", low1 = mean of the worst
// max(1, round(n*0.01)) values.
func TestSummarizeMatchesNumpy(t *testing.T) {
	s := []float64{3.2, 1.1, 9.8, 4.4, 4.4, 7.0, 2.2, 8.8, 5.5, 6.1, 0.3, 12.7}
	d := summarize(s, true)
	want := map[string]float64{"median": 4.95, "p99": 12.381, "iqr": 4.5, "low1": 0.3}
	got := map[string]float64{"median": d.Median, "p99": d.P99, "iqr": d.IQR, "low1": d.Low1}
	for k, w := range want {
		if math.Abs(got[k]-w) > 1e-9 {
			t.Errorf("%s = %v, want %v", k, got[k], w)
		}
	}
	if d.N != 12 {
		t.Errorf("n = %d", d.N)
	}
	if e := summarize(nil, true); e.N != 0 {
		t.Errorf("empty summary should have n=0, got %+v", e)
	}
	if e := summarize([]float64{math.NaN(), math.Inf(1)}, false); e.N != 0 {
		t.Errorf("non-finite samples must be dropped, got %+v", e)
	}
}

func TestClassifyChunking(t *testing.T) {
	cases := []struct {
		comp, n int
		want    string
	}{
		{0, 10, "unknown"}, {10, 1, "unknown"}, {5, 10, "unknown"}, // tpu < 0.9
		{11, 10, "per_token"},   // +1 EOS
		{102, 100, "per_token"}, // within 2% tolerance
		{390, 100, "batched"},
	}
	for _, c := range cases {
		if got := classifyChunking(c.comp, c.n); got != c.want {
			t.Errorf("classifyChunking(%d,%d) = %s, want %s", c.comp, c.n, got, c.want)
		}
	}
}

func TestEnoughSamples(t *testing.T) {
	if enoughSamplesForPercentile(20, 99) {
		t.Error("20 samples must not be enough for a p99")
	}
	if !enoughSamplesForPercentile(500, 99) {
		t.Error("500 samples is enough for a p99")
	}
	if samplesNeeded(99) != 500 {
		t.Errorf("need = %d", samplesNeeded(99))
	}
}

func loadFixture(t *testing.T) (*Results, map[string]any) {
	t.Helper()
	data, err := os.ReadFile("testdata/results_schema2.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := os.ReadFile("testdata/expected_schema2.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(exp, &want); err != nil {
		t.Fatal(err)
	}
	return r, want
}

func TestCategoryOrderIsFileOrder(t *testing.T) {
	r, _ := loadFixture(t)
	got := r.Categories()
	want := []string{"code", "reasoning", "prose", "json", "file_edit", "summarization", "chat", "math"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("categories = %v, want %v", got, want)
	}
}

// The mapped rows must carry exactly the figures BetterBench's own report
// prints, formatted the same way. Expected strings come from a numpy port of
// report.py run over the same fixture.
func TestPayloadMatchesReferenceRows(t *testing.T) {
	r, want := loadFixture(t)
	p, err := Build(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Template != "bench.report.v1" {
		t.Errorf("template = %q", p.Template)
	}

	wantCats := want["categories"].(map[string]any)
	if len(p.Categories) != len(wantCats) {
		t.Fatalf("got %d categories, want %d", len(p.Categories), len(wantCats))
	}
	for _, c := range p.Categories {
		w := wantCats[c.Name].(map[string]any)
		check := func(field, got string, want any) {
			if got != want.(string) {
				t.Errorf("%s.%s = %q, want %q", c.Name, field, got, want)
			}
		}
		if c.Runs != itoa(int(w["runs"].(float64))) {
			t.Errorf("%s.runs = %s, want %v", c.Name, c.Runs, w["runs"])
		}
		check("ttft_p50", c.TTFTP50, w["ttft_p50"])
		check("ttft_p99", c.TTFTP99, w["ttft_p99"])
		check("decode", c.Decode, w["decode"])
		check("decode_iqr", c.DecodeIQR, w["decode_iqr"])
		check("itl_low1", c.ITLLow1, w["itl_low1"])
		check("itl_med", c.ITLMed, w["itl_med"])
		check("itl_high99", c.ITLHigh99, w["itl_high99"])
		if c.UpdateP50 != "" || c.TokPerUpdate != "" {
			t.Errorf("%s: per-token run must not carry update columns: %+v", c.Name, c)
		}
		if c.Undersampled != w["undersampled"].(bool) {
			t.Errorf("%s.undersampled = %v, want %v", c.Name, c.Undersampled, w["undersampled"])
		}
	}

	wantComb := want["combined"].(map[string]any)
	byLabel := map[string]Metric{}
	for _, m := range p.Headline {
		byLabel[m.Label] = m
	}
	if m := byLabel["Combined decode"]; m.Value != wantComb["decode"] || m.Unit != "t/s" {
		t.Errorf("combined decode = %+v, want %v", m, wantComb["decode"])
	}
	if m := byLabel["Combined ITL 1% low"]; m.Value != wantComb["itl_low1"] {
		t.Errorf("combined itl_low1 = %+v, want %v", m, wantComb["itl_low1"])
	}
	if m := byLabel["Combined TTFT p50"]; m.Value != wantComb["ttft_p50"] || m.Unit != "ms" {
		t.Errorf("combined ttft = %+v, want %v", m, wantComb["ttft_p50"])
	}
	if _, ok := byLabel["Peak aggregate"]; !ok {
		t.Error("headline should carry the concurrency peak")
	}
	if _, ok := byLabel["Prefill"]; !ok {
		t.Error("headline should carry the prefill peak")
	}

	wantConc := want["concurrency"].([]any)
	if len(p.Concurrency) != len(wantConc) {
		t.Fatalf("got %d concurrency rows, want %d", len(p.Concurrency), len(wantConc))
	}
	for i, c := range p.Concurrency {
		w := wantConc[i].(map[string]any)
		got := map[string]string{"level": c.Level, "aggregate": c.Aggregate, "ttft_p50": c.TTFTP50, "ttft_p99": c.TTFTP99, "decode": c.Decode}
		for k, v := range w {
			if got[k] != v.(string) {
				t.Errorf("concurrency[%d].%s = %q, want %q", i, k, got[k], v)
			}
		}
	}

	wantPre := want["prefill"].([]any)
	if len(p.Prefill) != len(wantPre) {
		t.Fatalf("got %d prefill rows, want %d (skipped depth must be dropped)", len(p.Prefill), len(wantPre))
	}
	for i, d := range p.Prefill {
		w := wantPre[i].(map[string]any)
		if d.Depth != w["depth"] || d.PrefillTPS != w["prefill_tps"] || d.TTFT != w["ttft"] {
			t.Errorf("prefill[%d] = %+v, want %v", i, d, w)
		}
	}
}

func TestSystemAndHarnessFromEnv(t *testing.T) {
	r, _ := loadFixture(t)
	p, err := Build(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p.System.Model != "Qwen3-8B" || p.System.Engine != "vLLM 0.9.3" {
		t.Errorf("system = %+v", p.System)
	}
	if p.System.Hardware != "NVIDIA GeForce RTX 4090" {
		t.Errorf("hardware = %q; should come from the nvidia-smi name column", p.System.Hardware)
	}
	// The engine note was promoted to system.engine and must not repeat as a chip.
	for _, n := range p.System.Notes {
		if n.Label == "engine" {
			t.Errorf("engine note duplicated in notes: %+v", p.System.Notes)
		}
	}
	if len(p.System.Notes) != 2 || p.System.Notes[0].Label != "quant" || p.System.Notes[1].Label != "tp" {
		t.Errorf("notes = %+v, want quant and tp in sorted order", p.System.Notes)
	}
	if p.Harness.Name != "BetterBench" || p.Harness.Version != "0.4.0" || p.Harness.CorpusVersion != "1.0" ||
		p.Harness.Passes != "20 measured passes after 3 warmup" {
		t.Errorf("harness = %+v", p.Harness)
	}
	if p.Title != "Qwen3-8B on vLLM 0.9.3" {
		t.Errorf("title = %q", p.Title)
	}
	if !strings.Contains(p.Subtitle, "RTX 4090") || !strings.Contains(p.Subtitle, "20 passes per category") {
		t.Errorf("subtitle = %q", p.Subtitle)
	}
	if !strings.Contains(p.Summary, "Combined decode") || len(p.Summary) > maxSummary {
		t.Errorf("summary = %q", p.Summary)
	}
}

// Nothing that identifies the machine or its network may reach a public page.
func TestNoEndpointOrHostLeaks(t *testing.T) {
	r, _ := loadFixture(t)
	p, err := Build(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	for _, secret := range []string{"192.168", "rig01", "http://", "corpus_hash", "abc123"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("payload leaks %q", secret)
		}
	}
}

func TestCaveatsFromFixture(t *testing.T) {
	r, _ := loadFixture(t)
	p, err := Build(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(p.Caveats, "\n")
	for _, want := range []string{
		"Under-sampled: TTFT p99 in 8 categories (n≤20)", "TTFT p99 at concurrency 1, 2, 4, 8, 16 (n≤48)",
		"prefill p99 at prefill depth 1024, 4096, 8192 (n≤8)", "A p99 needs 500 samples; read these as roughly the worst observed.",
		"6 of 159 runs stopped at max_tokens",
		"1 single-stream request(s) failed",
		"level 16: 46/48 ok",
		"Prefill depths skipped: 32768 tokens (exceed the model context of 16384)",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("caveats missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "Quick smoke run") {
		t.Error("a 20-pass run is not quick")
	}
	// ~1,800 gaps per category is enough for a p99, so ITL must not be flagged
	// even though TTFT (20 samples) is. The gate is per metric, not per run count.
	if strings.Contains(joined, "ITL 99% high") {
		t.Errorf("ITL wrongly flagged as under-sampled:\n%s", joined)
	}
	if len(p.Caveats) > maxCaveats {
		t.Errorf("%d caveats exceeds the template limit", len(p.Caveats))
	}
	for _, c := range p.Caveats {
		if len(c) > maxCaveat {
			t.Errorf("caveat over %d chars: %q", maxCaveat, c)
		}
	}
}

func TestQuickRunIsFlaggedFirst(t *testing.T) {
	r, _ := loadFixture(t)
	r.Config.RunsPerCategory, r.Config.Warmup = 5, 1
	p, err := Build(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Caveats) == 0 || !strings.HasPrefix(p.Caveats[0], "Quick run: 5 passes per category after 1 warmup") {
		t.Errorf("first caveat = %v", p.Caveats)
	}
	if !strings.HasPrefix(p.Summary, "Quick run, verifiable as a quick run") {
		t.Errorf("summary = %q", p.Summary)
	}
	// Below the quick floor it is only a smoke test.
	r.Config.RunsPerCategory, r.Config.Warmup = 3, 0
	p, _ = Build(r, Options{})
	if !strings.HasPrefix(p.Caveats[0], "Quick smoke run: 3 passes per category after 0 warmup") ||
		!strings.HasPrefix(p.Summary, "Quick smoke run, not a publishable measurement.") {
		t.Errorf("smoke run: %q / %v", p.Summary, p.Caveats)
	}
}

func TestBatchedServerUsesUpdateColumns(t *testing.T) {
	r, _ := loadFixture(t)
	// Make one category batched: 3 tokens per chunk.
	for i := range r.SingleStream["code"] {
		run := &r.SingleStream["code"][i]
		if run.CompletionTokens != nil {
			run.NChunks = *run.CompletionTokens / 3
		}
	}
	p, err := Build(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Categories {
		if c.ITLLow1 != "" || c.ITLMed != "" {
			t.Errorf("%s: ITL must not be reported once the report is batched", c.Name)
		}
		if c.UpdateP50 == "" || c.UpdateP99 == "" || c.TokPerUpdate == "" {
			t.Errorf("%s: missing update columns: %+v", c.Name, c)
		}
	}
	found := false
	for _, m := range p.Headline {
		if m.Label == "Combined update p99" && m.Unit == "ms" {
			found = true
		}
		if m.Label == "Combined ITL 1% low" {
			t.Error("ITL headline must be replaced by update p99 on a batched report")
		}
	}
	if !found {
		t.Error("headline lacks combined update p99")
	}
	if !strings.Contains(strings.Join(p.Caveats, "\n"), "streamed several tokens per update") {
		t.Errorf("caveats = %v", p.Caveats)
	}
}

// A schema-1 file (0.2.x) stored itl_ms scaled by n_chunks/completion_tokens;
// the gaps must be recovered exactly, as report.run_gaps_ms does.
func TestSchema1GapsAreRecovered(t *testing.T) {
	gaps := []float64{20, 25, 1296.72, 18}
	comp, n := 390, 100
	scale := float64(n) / float64(comp)
	itl := make([]float64, len(gaps))
	for i, g := range gaps {
		itl[i] = g * scale
	}
	run := Run{OK: true, CompletionTokens: &comp, NChunks: n, ITLMs: itl}
	got := runGaps(run)
	for i := range gaps {
		if math.Abs(got[i]-gaps[i]) > 1e-9 {
			t.Errorf("gap[%d] = %v, want %v", i, got[i], gaps[i])
		}
	}
	if runChunking(run) != "batched" {
		t.Errorf("390 tokens over 100 chunks is batched, got %s", runChunking(run))
	}

	data := []byte(`{"schema":1,"betterbench_version":"0.2.3","env":{"model":"m"},"config":{"runs_per_category":20},
		"single_stream":{"prose":[{"ok":true,"ttft_ms":50,"decode_tps":100,"completion_tokens":390,"n_chunks":100,
		"itl_ms":[5.128,6.41,230.76,4.615],"finish_reason":"length"}]}}`)
	r, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Build(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Caveats, "\n"), "schema 1") {
		t.Errorf("schema-1 caveat missing: %v", p.Caveats)
	}
	if p.Categories[0].TokPerUpdate != "3.90" {
		t.Errorf("tok_per_update = %q, want 3.90", p.Categories[0].TokPerUpdate)
	}
}

func TestParseRejectsNonBetterBenchFiles(t *testing.T) {
	for _, in := range []string{
		`{"$template":"bench.report.v1","title":"x"}`,
		`{"title":"x","summary":"y"}`,
		`{"betterbench_version":"0.4.0","env":{}}`, // no model
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%s) should fail", in)
		}
	}
	if _, err := Parse([]byte(`not json`)); err == nil || !errors.Is(err, ErrSyntax) {
		t.Errorf("invalid JSON must fail with ErrSyntax, got %v", err)
	}
	// A mapped payload, including one with string-typed concurrency levels
	// that would otherwise trip the typed decode, is named for what it is.
	payload := []byte(`{"$template":"bench.report.v1","title":"x","concurrency":[{"level":"8"}]}`)
	if _, err := Parse(payload); !errors.Is(err, ErrAlreadyPayload) {
		t.Errorf("template payload should yield ErrAlreadyPayload, got %v", err)
	}
	if _, err := Parse([]byte(`[1,2]`)); !errors.Is(err, ErrNotBetterBench) {
		t.Errorf("array should yield ErrNotBetterBench, got %v", err)
	}
}

func TestUserTextIsValidatedNotTruncated(t *testing.T) {
	r, _ := loadFixture(t)
	_, err := Build(r, Options{Title: strings.Repeat("x", 121)})
	var ie *InputError
	if err == nil || !errorsAs(err, &ie) || !strings.Contains(err.Error(), "--title") {
		t.Fatalf("expected an InputError naming --title, got %v", err)
	}
	_, err = Build(r, Options{Sections: []Section{{Heading: "h", Body: ""}}})
	if err == nil {
		t.Fatal("empty section body must be refused")
	}
	p, err := Build(r, Options{Title: "My title", Summary: "My summary.", Engine: "llama.cpp b1234",
		Sections: []Section{{Heading: "Setup", Body: "Plain text."}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "My title" || p.Summary != "My summary." || p.System.Engine != "llama.cpp b1234" {
		t.Errorf("overrides not applied: %+v", p)
	}
	if len(p.Sections) != 2 || p.Sections[0].Heading != "Method" || p.Sections[1].Heading != "Setup" {
		t.Errorf("sections = %+v", p.Sections)
	}
}

// Every payload this package builds must be under the publish limit, with
// room to spare, even from a maximal file: 12 categories, 10 levels, 10 depths.
func TestPayloadStaysUnderLimit(t *testing.T) {
	r, _ := loadFixture(t)
	for i := 0; i < 4; i++ {
		r.SingleStream["extra"+itoa(i)] = r.SingleStream["code"]
		r.categoryOrder = append(r.categoryOrder, "extra"+itoa(i))
	}
	for len(r.Concurrency) < 10 {
		r.Concurrency = append(r.Concurrency, r.Concurrency[0])
	}
	for len(r.Prefill) < 12 {
		r.Prefill = append(r.Prefill, r.Prefill[0])
	}
	p, err := Build(r, Options{Sections: []Section{
		{Heading: "A", Body: strings.Repeat("x", 4000)}, {Heading: "B", Body: strings.Repeat("x", 4000)},
		{Heading: "C", Body: strings.Repeat("x", 4000)}, {Heading: "D", Body: strings.Repeat("x", 4000)},
		{Heading: "E", Body: strings.Repeat("x", 4000)}, {Heading: "F", Body: strings.Repeat("x", 4000)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	if len(b) > 65536 {
		t.Errorf("payload is %d bytes", len(b))
	}
	if len(p.Categories) != 12 || len(p.Concurrency) != 10 || len(p.Prefill) != 10 || len(p.Sections) != 6 {
		t.Errorf("limits not applied: %d cats, %d conc, %d prefill, %d sections",
			len(p.Categories), len(p.Concurrency), len(p.Prefill), len(p.Sections))
	}
}

func errorsAs(err error, target **InputError) bool {
	ie, ok := err.(*InputError)
	if ok {
		*target = ie
	}
	return ok
}

// A BetterBench 0.6.1 file: cached_tokens on every request, the 0.6.0
// default concurrency levels, and one prefill request served from the
// server's prefix cache although the run claims a cold one.
func TestV061FixtureCacheHits(t *testing.T) {
	data, err := os.ReadFile("testdata/results_schema2_v061.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if r.BetterBenchVersion != "0.6.1" || !r.CacheReported() || r.CacheHits() != 1 {
		t.Fatalf("version %s reported %v hits %d", r.BetterBenchVersion, r.CacheReported(), r.CacheHits())
	}
	p, err := Build(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Concurrency) != 4 {
		t.Errorf("want the 4 levels the run contains, got %d", len(p.Concurrency))
	}
	joined := strings.Join(p.Caveats, "\n")
	if !strings.Contains(joined, "1 request(s) were served partly from the server's prefix cache") {
		t.Errorf("cache-hit caveat missing:\n%s", joined)
	}
	body, err := PrepareRaw(data, Prose{})
	if err != nil || !strings.Contains(string(body), `"cached_tokens":[3072,0,0`) {
		t.Errorf("raw payload must keep cached_tokens as written: %v", err)
	}

	// The 0.4.0 fixture reports nothing, which is "unknown", not "cold".
	old, _ := loadFixture(t)
	if old.CacheReported() || old.CacheHits() != 0 {
		t.Error("0.4.0 fixture has no cached_tokens")
	}
}
