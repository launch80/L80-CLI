package betterbench

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Template limits, from launch80's bench.report.v1 schema. The builder trims
// text it generates itself to these and refuses user text that exceeds them,
// so the server never sees a payload this command produced and rejects it.
const (
	TemplateID = "bench.report.v1"

	maxTitle, maxSubtitle, maxSummary = 120, 160, 600
	maxModel, maxEngine, maxHardware  = 120, 64, 120
	maxNoteLabel, maxNoteValue        = 32, 64
	maxNotes, maxHeadline             = 10, 6
	maxCategories, maxCategoryName    = 12, 32
	maxConcurrency, maxPrefill        = 10, 10
	maxCaveats, maxCaveat             = 8, 240
	maxSections, maxHeading, maxBody  = 6, 100, 4000
	maxHeadlineLabel, maxHeadlineVal  = 48, 32
	maxHeadlineUnit, maxHeadlineNote  = 16, 120
	maxHarnessPasses                  = 64
)

// Payload is a bench.report.v1 document. Every displayed number is a string
// formatted here; the renderer never does arithmetic on measured data.
type Payload struct {
	Template    string           `json:"$template"`
	Title       string           `json:"title"`
	Subtitle    string           `json:"subtitle,omitempty"`
	Summary     string           `json:"summary"`
	Harness     *Harness         `json:"harness,omitempty"`
	System      System           `json:"system"`
	Headline    []Metric         `json:"headline,omitempty"`
	Categories  []Category       `json:"categories,omitempty"`
	Concurrency []ConcurrencyOut `json:"concurrency,omitempty"`
	Prefill     []PrefillOut     `json:"prefill,omitempty"`
	Caveats     []string         `json:"caveats,omitempty"`
	Sections    []Section        `json:"sections,omitempty"`
}

type Harness struct {
	Name          string `json:"name"`
	Version       string `json:"version,omitempty"`
	CorpusVersion string `json:"corpus_version,omitempty"`
	Passes        string `json:"passes,omitempty"`
}

type System struct {
	Model    string `json:"model"`
	Engine   string `json:"engine,omitempty"`
	Hardware string `json:"hardware,omitempty"`
	Notes    []Note `json:"notes,omitempty"`
}

type Note struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Metric struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
	Note  string `json:"note,omitempty"`
}

type Category struct {
	Name         string `json:"name"`
	Runs         string `json:"runs,omitempty"`
	TTFTP50      string `json:"ttft_p50,omitempty"`
	TTFTP99      string `json:"ttft_p99,omitempty"`
	Decode       string `json:"decode,omitempty"`
	DecodeIQR    string `json:"decode_iqr,omitempty"`
	ITLLow1      string `json:"itl_low1,omitempty"`
	ITLMed       string `json:"itl_med,omitempty"`
	ITLHigh99    string `json:"itl_high99,omitempty"`
	UpdateP50    string `json:"update_p50,omitempty"`
	UpdateP99    string `json:"update_p99,omitempty"`
	TokPerUpdate string `json:"tok_per_update,omitempty"`
	Undersampled bool   `json:"undersampled,omitempty"`
}

type ConcurrencyOut struct {
	Level     string `json:"level"`
	Aggregate string `json:"aggregate,omitempty"`
	TTFTP50   string `json:"ttft_p50,omitempty"`
	TTFTP99   string `json:"ttft_p99,omitempty"`
	Decode    string `json:"decode,omitempty"`
}

type PrefillOut struct {
	Depth      string `json:"depth"`
	PrefillTPS string `json:"prefill_tps,omitempty"`
	TTFT       string `json:"ttft,omitempty"`
}

type Section struct {
	Heading string   `json:"heading"`
	Body    string   `json:"body"`
	Bullets []string `json:"bullets,omitempty"`
}

// Options are the parts of a page BetterBench cannot know: how to title it,
// what to say about it, and what the box was. Every field is optional; the
// builder derives a factual default from the results file.
type Options struct {
	Title, Subtitle, Summary string
	Engine, Hardware         string
	Sections                 []Section
}

// InputError reports user-supplied text that the template would reject.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func checkLen(what, s string, max int) error {
	if len(s) > max {
		return &InputError{fmt.Sprintf("%s is %d characters; the template allows %d", what, len(s), max)}
	}
	return nil
}

// Formatting helpers, matching report._fmt/_fmt0/_fmt2.
func fmt1(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 1, 64)
}
func fmt0(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 0, 64)
}
func fmt2(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 2, 64)
}
func itoa(i int) string { return strconv.Itoa(i) }

// clip trims generated text to a template limit, on a word boundary where
// one exists, with an ellipsis. Never applied to user-supplied text.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max-1]
	if i := strings.LastIndexByte(cut, ' '); i > max/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// engineNoteKeys are the --note keys people use for the serving stack, in
// preference order. The first present becomes system.engine and leaves notes.
var engineNoteKeys = []string{"engine", "server", "runtime", "backend", "image"}

// hardwareNoteKeys likewise for system.hardware.
var hardwareNoteKeys = []string{"hardware", "gpu", "device"}

// Build maps results onto the template.
func Build(r *Results, opt Options) (*Payload, error) {
	for _, c := range []struct {
		what string
		s    string
		max  int
	}{
		{"--title", opt.Title, maxTitle}, {"--subtitle", opt.Subtitle, maxSubtitle},
		{"--summary", opt.Summary, maxSummary}, {"--engine", opt.Engine, maxEngine},
		{"--hardware", opt.Hardware, maxHardware},
	} {
		if err := checkLen(c.what, c.s, c.max); err != nil {
			return nil, err
		}
	}
	for i, s := range opt.Sections {
		if s.Heading == "" || s.Body == "" {
			return nil, &InputError{fmt.Sprintf("section %d needs both a heading and a body", i+1)}
		}
		if err := checkLen("section heading", s.Heading, maxHeading); err != nil {
			return nil, err
		}
		if err := checkLen("section body", s.Body, maxBody); err != nil {
			return nil, err
		}
	}

	rows := r.SingleRows()
	batched := reportIsBatched(rows)
	comb := combined(rows, r.Config.Weights)
	conc := r.ConcurrencyRows()
	pre := r.PrefillRows()
	gate := r.SampleGate()
	quick := len(r.SingleStream) > 0 && r.Config.RunsPerCategory > 0 && RunClass(r.Config) != ClassFull

	// --- system ------------------------------------------------------------
	notes := map[string]string{}
	for k, v := range r.Env.Notes {
		notes[k] = v
	}
	take := func(keys []string) string {
		for _, k := range keys {
			if v, ok := notes[k]; ok && v != "" {
				delete(notes, k)
				return v
			}
		}
		return ""
	}
	engine := opt.Engine
	if engine == "" {
		engine = clip(take(engineNoteKeys), maxEngine)
	} else {
		take(engineNoteKeys) // the flag wins; do not repeat the note as a chip
	}
	hardware := opt.Hardware
	if hardware == "" {
		hardware = take(hardwareNoteKeys)
	} else {
		take(hardwareNoteKeys)
	}
	if hardware == "" {
		hardware = gpuName(r.Env.GPU)
	}
	hardware = clip(hardware, maxHardware)

	sys := System{Model: clip(r.Env.Model, maxModel), Engine: engine, Hardware: hardware}
	keys := make([]string, 0, len(notes))
	for k := range notes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if len(sys.Notes) == maxNotes {
			break
		}
		sys.Notes = append(sys.Notes, Note{Label: clip(k, maxNoteLabel), Value: clip(notes[k], maxNoteValue)})
	}

	// --- harness -----------------------------------------------------------
	h := &Harness{Name: "BetterBench", Version: r.BetterBenchVersion, CorpusVersion: r.CorpusVersion}
	if len(r.SingleStream) > 0 && r.Config.RunsPerCategory > 0 {
		h.Passes = clip(fmt.Sprintf("%d measured passes after %d warmup", r.Config.RunsPerCategory, r.Config.Warmup), maxHarnessPasses)
	}

	// --- headline ----------------------------------------------------------
	var head []Metric
	if comb != nil {
		if comb.Decode != nil {
			head = append(head, Metric{Label: "Combined decode", Value: fmt1(comb.Decode), Unit: "t/s",
				Note: fmt.Sprintf("weighted median across %d categories", len(rows))})
		}
		if batched && comb.UpdateP99 != nil {
			head = append(head, Metric{Label: "Combined update p99", Value: fmt1(comb.UpdateP99), Unit: "ms",
				Note: "stream-update stutter; server batches tokens per update"})
		} else if !batched && comb.ITLLow1 != nil {
			head = append(head, Metric{Label: "Combined ITL 1% low", Value: fmt1(comb.ITLLow1), Unit: "t/s",
				Note: "slowest 1% of tokens"})
		}
		if comb.TTFTP50 != nil {
			head = append(head, Metric{Label: "Combined TTFT p50", Value: fmt0(comb.TTFTP50), Unit: "ms"})
		}
	}
	if best := peakConcurrency(conc); best != nil {
		head = append(head, Metric{Label: "Peak aggregate", Value: fmt1(&best.AggregateTPS), Unit: "t/s",
			Note: fmt.Sprintf("at concurrency %d, %d/%d requests ok", best.Level, best.OK, best.Requests)})
	}
	if best := peakPrefill(pre); best != nil {
		head = append(head, Metric{Label: "Prefill", Value: fmt0(best.PPMed), Unit: "t/s",
			Note: fmt.Sprintf("median at ~%d prompt tokens", best.TargetDepth)})
	}
	if len(head) > maxHeadline {
		head = head[:maxHeadline]
	}

	// --- categories --------------------------------------------------------
	var cats []Category
	for _, row := range rows {
		if len(cats) == maxCategories {
			break
		}
		c := Category{
			Name: clip(row.Name, maxCategoryName), Runs: itoa(row.Runs),
			TTFTP50: fmt1(row.TTFTP50), TTFTP99: fmt1(row.TTFTP99),
			Decode: fmt1(row.DecodeMed), DecodeIQR: fmt1(row.DecodeIQR),
		}
		if batched {
			// Same rule as the markdown report: once any category batched, the
			// whole table shows update columns, so rows line up.
			c.UpdateP50, c.UpdateP99, c.TokPerUpdate = fmt1(row.UpdateP50), fmt1(row.UpdateP99), fmt2(row.TokPerUpdate)
		} else {
			c.ITLLow1, c.ITLMed, c.ITLHigh99 = fmt1(row.ITLLow1), fmt1(row.ITLMed), fmt1(row.ITLHigh99)
		}
		c.Undersampled = (row.TTFTP99 != nil && !row.TTFTP99OK) || (row.GapN > 0 && !row.TailOK)
		cats = append(cats, c)
	}

	// --- concurrency / prefill --------------------------------------------
	var concOut []ConcurrencyOut
	for _, c := range conc {
		if len(concOut) == maxConcurrency {
			break
		}
		concOut = append(concOut, ConcurrencyOut{
			Level: itoa(c.Level), Aggregate: fmt1(&c.AggregateTPS),
			TTFTP50: fmt1(c.TTFTP50), TTFTP99: fmt1(c.TTFTP99), Decode: fmt1(c.DecodeMed),
		})
	}
	var preOut []PrefillOut
	for _, d := range pre {
		if d.Skipped || len(preOut) == maxPrefill {
			continue
		}
		preOut = append(preOut, PrefillOut{Depth: itoa(d.TargetDepth), PrefillTPS: fmt0(d.PPMed), TTFT: fmt1(d.TTFTP50)})
	}

	// --- caveats -----------------------------------------------------------
	caveats := buildCaveats(r, rows, conc, pre, gate, quick)

	// --- sections ----------------------------------------------------------
	sections := []Section{{Heading: "Method", Body: clip(methodText(r, rows, batched), maxBody)}}
	sections = append(sections, opt.Sections...)
	if len(sections) > maxSections {
		sections = sections[:maxSections]
	}

	// --- title / summary ---------------------------------------------------
	title := opt.Title
	if title == "" {
		title = sys.Model
		if engine != "" {
			title += " on " + engine
		}
		title = clip(title, maxTitle)
	}
	subtitle := opt.Subtitle
	if subtitle == "" {
		subtitle = clip(defaultSubtitle(r, hardware, quick), maxSubtitle)
	}
	summary := opt.Summary
	if summary == "" {
		summary = clip(defaultSummary(r, rows, comb, batched, conc, pre, quick), maxSummary)
	}

	return &Payload{
		Template: TemplateID, Title: title, Subtitle: subtitle, Summary: summary,
		Harness: h, System: sys, Headline: head, Categories: cats,
		Concurrency: concOut, Prefill: preOut, Caveats: caveats, Sections: sections,
	}, nil
}

func gpuName(g GPU) string {
	switch g.Vendor {
	case "nvidia":
		if len(g.NvidiaSMI) > 0 {
			// "name, driver_version, clocks.sm, temperature.gpu, power.draw"
			return strings.TrimSpace(strings.SplitN(g.NvidiaSMI[0], ",", 2)[0])
		}
	case "amd":
		for i := len(g.ROCmSMIProductName) - 1; i >= 0; i-- {
			if s := strings.TrimSpace(g.ROCmSMIProductName[i]); s != "" {
				return s
			}
		}
	}
	return ""
}

func peakConcurrency(rows []ConcurrencyRow) *ConcurrencyRow {
	var best *ConcurrencyRow
	for i := range rows {
		if best == nil || rows[i].AggregateTPS > best.AggregateTPS {
			best = &rows[i]
		}
	}
	return best
}

func peakPrefill(rows []PrefillRow) *PrefillRow {
	var best *PrefillRow
	for i := range rows {
		if rows[i].Skipped || rows[i].PPMed == nil {
			continue
		}
		if best == nil || *rows[i].PPMed > *best.PPMed {
			best = &rows[i]
		}
	}
	return best
}

func defaultSubtitle(r *Results, hardware string, quick bool) string {
	var parts []string
	if hardware != "" {
		parts = append(parts, hardware)
	}
	if len(r.SingleStream) > 0 && r.Config.RunsPerCategory > 0 {
		parts = append(parts, fmt.Sprintf("%d passes per category", r.Config.RunsPerCategory))
	}
	if r.Config.Greedy {
		parts = append(parts, "greedy")
	} else if r.Config.Temperature != nil {
		parts = append(parts, "temp "+strconv.FormatFloat(*r.Config.Temperature, 'f', -1, 64))
	}
	if quick {
		if RunClass(r.Config) == ClassQuick {
			parts = append(parts, "quick run")
		} else {
			parts = append(parts, "quick smoke run")
		}
	}
	return strings.Join(parts, " · ")
}

func defaultSummary(r *Results, rows []CategoryRow, comb *Combined, batched bool,
	conc []ConcurrencyRow, pre []PrefillRow, quick bool) string {
	var b strings.Builder
	if quick {
		if RunClass(r.Config) == ClassQuick {
			b.WriteString("Quick run, verifiable as a quick run (with a reproduction, on medians). ")
		} else {
			b.WriteString("Quick smoke run, not a publishable measurement. ")
		}
	}
	fmt.Fprintf(&b, "%s measured with BetterBench %s", r.Env.Model, r.BetterBenchVersion)
	if len(rows) > 0 {
		fmt.Fprintf(&b, ": %d measured passes per category over %d categories", r.Config.RunsPerCategory, len(rows))
	}
	b.WriteString(".")
	if comb != nil {
		var bits []string
		if comb.Decode != nil {
			bits = append(bits, "combined decode "+fmt1(comb.Decode)+" t/s (weighted median)")
		}
		if comb.TTFTP50 != nil {
			bits = append(bits, "TTFT p50 "+fmt0(comb.TTFTP50)+" ms")
		}
		if batched && comb.UpdateP99 != nil {
			bits = append(bits, "stream-update p99 "+fmt1(comb.UpdateP99)+" ms")
		} else if comb.ITLLow1 != nil {
			bits = append(bits, "ITL 1% low "+fmt1(comb.ITLLow1)+" t/s")
		}
		if len(bits) > 0 {
			b.WriteString(" " + upperFirst(strings.Join(bits, ", ")) + ".")
		}
	}
	if best := peakConcurrency(conc); best != nil {
		fmt.Fprintf(&b, " Aggregate throughput peaked at %s t/s at concurrency %d.", fmt1(&best.AggregateTPS), best.Level)
	}
	if best := peakPrefill(pre); best != nil {
		fmt.Fprintf(&b, " Prefill reached %s t/s at ~%d prompt tokens.", fmt0(best.PPMed), best.TargetDepth)
	}
	return b.String()
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func methodText(r *Results, rows []CategoryRow, batched bool) string {
	var p []string
	cats := make([]string, 0, len(rows))
	for _, row := range rows {
		cats = append(cats, row.Name)
	}
	first := fmt.Sprintf("BetterBench %s", r.BetterBenchVersion)
	if r.CorpusVersion != "" {
		first += fmt.Sprintf(", corpus v%s", r.CorpusVersion)
	}
	if len(cats) > 0 {
		first += fmt.Sprintf(" (%d categories: %s)", len(cats), strings.Join(cats, ", "))
	}
	p = append(p, first+".")

	cfg := r.Config
	if len(r.SingleStream) > 0 {
		s := fmt.Sprintf("Single stream: %d warmup then %d measured passes per category", cfg.Warmup, cfg.RunsPerCategory)
		if cfg.Greedy {
			s += ", greedy sampling"
		} else if cfg.Temperature != nil {
			s += fmt.Sprintf(", temperature %s", strconv.FormatFloat(*cfg.Temperature, 'f', -1, 64))
			if cfg.TopP != nil {
				s += fmt.Sprintf(", top_p %s", strconv.FormatFloat(*cfg.TopP, 'f', -1, 64))
			}
			if cfg.TopK != nil {
				s += fmt.Sprintf(", top_k %d", *cfg.TopK)
			}
		}
		if cfg.UniqueNonce {
			s += ", prefix cache cold (unique nonce per request)"
		} else {
			s += ", prefix cache warm"
		}
		p = append(p, s+".")
	}
	if len(r.Concurrency) > 0 {
		levels := make([]string, 0, len(r.Concurrency))
		for _, c := range r.Concurrency {
			levels = append(levels, itoa(c.Level))
		}
		s := fmt.Sprintf("Concurrency sweep: %d requests at levels %s", cfg.ConcurrencyRequests, strings.Join(levels, ", "))
		if cfg.ConcurrencyRequests == 0 && len(r.Concurrency) > 0 {
			s = fmt.Sprintf("Concurrency sweep: %d requests at levels %s", r.Concurrency[0].Requests, strings.Join(levels, ", "))
		}
		p = append(p, s+".")
	}
	if len(r.Prefill) > 0 {
		depths := make([]string, 0, len(r.Prefill))
		for _, d := range r.Prefill {
			depths = append(depths, itoa(d.TargetDepth))
		}
		s := fmt.Sprintf("Prefill sweep at target depths %s tokens", strings.Join(depths, ", "))
		if cfg.PrefillRuns > 0 {
			s += fmt.Sprintf(", %d runs after %d warmup", cfg.PrefillRuns, cfg.PrefillWarmup)
		}
		if cfg.PrefillMaxTokens > 0 {
			s += fmt.Sprintf(", %d-token decode", cfg.PrefillMaxTokens)
		}
		p = append(p, s+", cold prefix cache. Prefill throughput is prompt tokens divided by TTFT.")
	}
	units := "Units: TTFT in ms; decode, ITL and prefill throughput in tokens per second"
	if batched {
		units += "; update p50/p99 is the wall-clock gap between stream updates in ms, and tok/update how many tokens each update carried"
	} else {
		units += "; ITL columns are instantaneous tokens per second (1% low = slowest tokens, 99% high = fastest)"
	}
	p = append(p, units+".")
	if len(cfg.Weights) > 0 && len(rows) > 0 {
		ws := make([]string, 0, len(cfg.Weights))
		for _, row := range rows {
			if w, ok := cfg.Weights[row.Name]; ok {
				ws = append(ws, fmt.Sprintf("%s %s", row.Name, strconv.FormatFloat(w, 'f', -1, 64)))
			}
		}
		if len(ws) > 0 {
			p = append(p, "Combined figures are weighted by category: "+strings.Join(ws, ", ")+". Categories without a weight are excluded from the combined figures.")
		}
	} else if len(rows) > 1 {
		p = append(p, "Combined figures weight every category equally.")
	}
	p = append(p, "Undersampled rows carry a percentile that rests on fewer samples than n·tail ≥ 5 requires; see caveats.")
	return strings.Join(p, "\n\n")
}

func buildCaveats(r *Results, rows []CategoryRow, conc []ConcurrencyRow, pre []PrefillRow,
	gate []UnderSampled, quick bool) []string {
	var out []string
	add := func(s string) {
		if len(out) < maxCaveats {
			out = append(out, clip(s, maxCaveat))
		}
	}

	if quick {
		if RunClass(r.Config) == ClassQuick {
			add(fmt.Sprintf("Quick run: %d passes per category after %d warmup. Medians are comparable and the run is verifiable as a quick run against a reproduction; p95, p99 and 1%% lows need a full %d-pass run.",
				r.Config.RunsPerCategory, r.Config.Warmup, MinFullPasses))
		} else {
			add(fmt.Sprintf("Quick smoke run: %d passes per category after %d warmup. Too few passes for any percentile to hold; treat every figure as indicative, not as a result.",
				r.Config.RunsPerCategory, r.Config.Warmup))
		}
	}
	if hits := r.CacheHits(); hits > 0 && r.Config.UniqueNonce {
		add(fmt.Sprintf("%d request(s) were served partly from the server's prefix cache although the run asked for a cold cache. Those timings measure a cache lookup, not prompt processing.", hits))
	}
	if len(gate) > 0 {
		add(underSampledCaveat(gate))
	}

	var trunc, tot, batchedRuns, runs, failed int
	for _, row := range rows {
		trunc += row.Truncated
		tot += row.Runs
		batchedRuns += row.BatchedRuns
		runs += row.Runs
		failed += row.Failed
	}
	if tot > 0 && trunc > 0 {
		add(fmt.Sprintf("%d of %d runs stopped at max_tokens (%.0f%%). On a thinking model a truncated run measures the thinking phase, not a complete answer.",
			trunc, tot, float64(trunc)/float64(tot)*100))
	}
	if batchedRuns > 0 {
		add(fmt.Sprintf("%d of %d runs streamed several tokens per update (speculative or batched decoding), so per-token ITL is not reported; update p50/p99 and tok/update are shown instead.",
			batchedRuns, runs))
	}
	if failed > 0 {
		add(fmt.Sprintf("%d single-stream request(s) failed and are excluded from every figure.", failed))
	}
	var concFail []string
	for _, c := range conc {
		if c.OK < c.Requests {
			concFail = append(concFail, fmt.Sprintf("level %d: %d/%d ok", c.Level, c.OK, c.Requests))
		}
	}
	if len(concFail) > 0 {
		add("Concurrency requests failed at " + strings.Join(concFail, "; ") + ". Aggregate throughput counts only completed requests.")
	}
	var skipped []string
	for _, d := range pre {
		if d.Skipped {
			skipped = append(skipped, itoa(d.TargetDepth))
		}
	}
	if len(skipped) > 0 {
		s := "Prefill depths skipped: " + strings.Join(skipped, ", ") + " tokens"
		if r.Env.MaxModelLen != nil {
			s += fmt.Sprintf(" (exceed the model context of %d)", *r.Env.MaxModelLen)
		}
		add(s + ".")
	}
	if r.Schema > 0 && r.Schema < 2 {
		add("Results file uses BetterBench schema 1 (v0.2.x); stream-update gaps were recovered from the stored per-run ITL scalar, as the tool's own report does.")
	}
	return out
}

// underSampledCaveat compresses the gate into one line: which metrics, in
// how many places, and what n they rest on. The full list can run to dozens
// of entries and would blow the caveat limits.
func underSampledCaveat(gate []UnderSampled) string {
	type agg struct {
		keys []string
		n    int
		need int
	}
	label := map[string]string{
		"ttft_p99": "TTFT p99", "itl_high99": "ITL 99% high", "update_p99": "update p99", "pp_p99": "prefill p99",
	}
	order := []string{}
	byMetric := map[string]*agg{}
	for _, u := range gate {
		k := u.Section + "/" + u.Metric
		a, ok := byMetric[k]
		if !ok {
			a = &agg{need: u.Need}
			byMetric[k] = a
			order = append(order, k)
		}
		a.keys = append(a.keys, u.Key)
		if u.N > a.n {
			a.n = u.N
		}
	}
	parts := make([]string, 0, len(order))
	need := 0
	for _, k := range order {
		a := byMetric[k]
		need = a.need
		section, metric, _ := strings.Cut(k, "/")
		where := ""
		switch section {
		case "single_stream":
			where = fmt.Sprintf("in %d categories", len(a.keys))
			if len(a.keys) == 1 {
				where = "in " + a.keys[0]
			}
		case "concurrency":
			where = "at concurrency " + strings.Join(a.keys, ", ")
		case "prefill":
			where = "at prefill depth " + strings.Join(a.keys, ", ")
		}
		parts = append(parts, fmt.Sprintf("%s %s (n≤%d)", label[metric], where, a.n))
	}
	// The closing sentence is the point of the caveat, so the list is what
	// gives way when the whole thing would exceed the template limit.
	tail := fmt.Sprintf(". A p99 needs %d samples; read these as roughly the worst observed.", need)
	head := "Under-sampled: " + strings.Join(parts, "; ")
	return clip(head, maxCaveat-len(tail)) + tail
}
