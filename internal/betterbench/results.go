package betterbench

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Results is the subset of a BetterBench results.json this mapping reads.
// Field names follow betterbench/cli.py, runner.py and client.RunResult.
// Everything is optional except env.model, because a file can carry any
// subset of the three phases and older schemas lack some keys.
type Results struct {
	Schema             int                `json:"schema"`
	BetterBenchVersion string             `json:"betterbench_version"`
	CorpusVersion      string             `json:"corpus_version"`
	Config             Config             `json:"config"`
	Env                Env                `json:"env"`
	SingleStream       map[string][]Run   `json:"single_stream"`
	Concurrency        []ConcurrencyLevel `json:"concurrency"`
	Prefill            []PrefillDepth     `json:"prefill"`
	RecordedGate       *SampleGate        `json:"sample_gate"`

	// The order categories appear in the file. Go maps do not preserve it and
	// the report prints them in file order, so it is recovered at decode time.
	categoryOrder []string
}

// SampleGate is BetterBench's record of under-sampled percentiles.
type SampleGate struct {
	Rule         string `json:"rule"`
	UnderSampled []struct {
		Section string `json:"section"`
		Metric  string `json:"metric"`
		N       int    `json:"n"`
		Need    int    `json:"need"`
	} `json:"under_sampled"`
}

type Config struct {
	Temperature         *float64           `json:"temperature"`
	TopP                *float64           `json:"top_p"`
	TopK                *int               `json:"top_k"`
	Greedy              bool               `json:"greedy"`
	Warmup              int                `json:"warmup"`
	RunsPerCategory     int                `json:"runs_per_category"`
	UniqueNonce         bool               `json:"unique_nonce"`
	ConcurrencyLevels   []int              `json:"concurrency_levels"`
	ConcurrencyRequests int                `json:"concurrency_requests"`
	Weights             map[string]float64 `json:"weights"`
	PrefillDepths       []int              `json:"prefill_depths"`
	PrefillRuns         int                `json:"prefill_runs"`
	PrefillWarmup       int                `json:"prefill_warmup"`
	PrefillMaxTokens    int                `json:"prefill_max_tokens"`
}

type Env struct {
	Model       string            `json:"model"`
	Notes       map[string]string `json:"notes"`
	GPU         GPU               `json:"gpu"`
	MaxModelLen *int              `json:"max_model_len"`
	// endpoint and host are deliberately not read: the template has no place
	// for them, and a LAN URL must never reach a public page.
}

type GPU struct {
	Vendor             string   `json:"vendor"`
	NvidiaSMI          []string `json:"nvidia_smi"`
	ROCmSMIProductName []string `json:"rocm_smi_productname"`
}

// Run is one single-stream request (client.RunResult.as_dict()).
type Run struct {
	OK               bool     `json:"ok"`
	TTFTMs           *float64 `json:"ttft_ms"`
	DecodeTPS        *float64 `json:"decode_tps"`
	CompletionTokens *int     `json:"completion_tokens"`
	NChunks          int      `json:"n_chunks"`
	FinishReason     *string  `json:"finish_reason"`
	Error            *string  `json:"error"`
	// Schema 2 records the measured gaps directly. A pointer distinguishes
	// "key absent" (schema 1) from "present but empty" (a failed run), which
	// report.run_gaps_ms treats differently.
	UpdateGapsMs *[]float64 `json:"update_gaps_ms"`
	// Schema 1 stored itl_ms = gap * n_chunks / completion_tokens, one scalar
	// per run, so the gaps are exactly recoverable (see test_migration.py).
	ITLMs              []float64 `json:"itl_ms"`
	ChunkTokenMismatch bool      `json:"chunk_token_mismatch"`
	// CachedTokens is the server's usage.prompt_tokens_details.cached_tokens
	// (BetterBench 0.6.1+). nil means the server did not report it, which is
	// not the same as a verified 0.
	CachedTokens *int `json:"cached_tokens"`
}

type ConcurrencyLevel struct {
	Level        int       `json:"level"`
	Requests     int       `json:"requests"`
	OK           int       `json:"ok"`
	AggregateTPS float64   `json:"aggregate_tps"`
	TTFTMs       []float64 `json:"ttft_ms"`
	DecodeTPS    []float64 `json:"decode_tps"`
	// CachedTokens holds one entry per request that reported the field
	// (BetterBench 0.6.1+).
	CachedTokens []int `json:"cached_tokens"`
}

type PrefillDepth struct {
	TargetDepth  int       `json:"target_depth"`
	Skipped      bool      `json:"skipped"`
	Reason       string    `json:"reason"`
	PromptTokens []float64 `json:"prompt_tokens"`
	TTFTMs       []float64 `json:"ttft_ms"`
	PPTPS        []float64 `json:"pp_tps"`
	CachedTokens []int     `json:"cached_tokens"`
}

// CacheHits counts requests the server answered partly from its prefix
// cache, as BetterBench's report.cache_hits does. On a run that claims a cold
// cache (unique_nonce) any hit means the timings measured a cache lookup,
// not prompt processing.
func (r *Results) CacheHits() int {
	n := 0
	for _, runs := range r.SingleStream {
		for _, run := range runs {
			if run.CachedTokens != nil && *run.CachedTokens > 0 {
				n++
			}
		}
	}
	for _, p := range r.Prefill {
		n += countPositive(p.CachedTokens)
	}
	for _, c := range r.Concurrency {
		n += countPositive(c.CachedTokens)
	}
	return n
}

// CacheReported says whether any request in the file carried cached_tokens,
// i.e. whether a zero from CacheHits means "verified cold" or "unknown".
func (r *Results) CacheReported() bool {
	for _, runs := range r.SingleStream {
		for _, run := range runs {
			if run.CachedTokens != nil {
				return true
			}
		}
	}
	for _, p := range r.Prefill {
		if len(p.CachedTokens) > 0 {
			return true
		}
	}
	for _, c := range r.Concurrency {
		if len(c.CachedTokens) > 0 {
			return true
		}
	}
	return false
}

func countPositive(xs []int) int {
	n := 0
	for _, x := range xs {
		if x > 0 {
			n++
		}
	}
	return n
}

// ErrNotBetterBench is returned for JSON that is not a BetterBench results
// file, so the CLI can tell the user what it was actually given.
var ErrNotBetterBench = errors.New("not a BetterBench results file")

// ErrSyntax wraps a JSON syntax error, so the CLI can report it as such
// rather than as the wrong kind of file.
var ErrSyntax = errors.New("invalid JSON")

// ErrAlreadyPayload means the file carries a $template: it is a template
// payload, not harness output, and belongs to `L80 publish`.
var ErrAlreadyPayload = fmt.Errorf("%w: it already carries $template", ErrNotBetterBench)

// Parse decodes a results.json and recovers category order.
func Parse(data []byte) (*Results, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		if _, isType := err.(*json.UnmarshalTypeError); isType {
			return nil, fmt.Errorf("%w: top level is not an object", ErrNotBetterBench)
		}
		return nil, fmt.Errorf("%w: %v", ErrSyntax, err)
	}
	if _, ok := top["$template"]; ok {
		return nil, ErrAlreadyPayload
	}
	_, hasVersion := top["betterbench_version"]
	_, hasEnv := top["env"]
	if !hasVersion && !hasEnv {
		return nil, fmt.Errorf("%w: no betterbench_version or env field", ErrNotBetterBench)
	}
	var r Results
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("does not match the BetterBench results shape: %w", err)
	}
	if r.Env.Model == "" {
		return nil, fmt.Errorf("%w: env.model is missing", ErrNotBetterBench)
	}
	r.categoryOrder = keyOrder(data, "single_stream")
	return &r, nil
}

// keyOrder returns the keys of the top-level object field `name` in the order
// they appear in the document. json.Decoder tokens are the cheapest way to
// see order without a third-party parser.
func keyOrder(data []byte, name string) []string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil
	}
	raw, ok := top[name]
	if !ok {
		return nil
	}
	dec := json.NewDecoder(bytesReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return keys
		}
		if s, ok := k.(string); ok {
			keys = append(keys, s)
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}

// Categories returns single-stream category names in file order.
func (r *Results) Categories() []string {
	if len(r.categoryOrder) == len(r.SingleStream) {
		return r.categoryOrder
	}
	// Fallback for a Results built in code rather than parsed.
	keys := make([]string, 0, len(r.SingleStream))
	for k := range r.SingleStream {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}
