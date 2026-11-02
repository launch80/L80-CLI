// Package discover finds OpenAI-compatible inference servers already running
// on this machine, so a benchmark can be pointed at one by picking it from a
// list instead of typing a URL and a model name.
//
// It only reads: GET /v1/models and each engine's own status route. It never
// loads a model (on llama-swap, listing does not load; a request would).
package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultPorts are where the common engines listen out of the box: vLLM and
// llama-swap 8000, llama.cpp 8080, SGLang 30000, Ollama 11434, LM Studio 1234,
// TGI and others 8001/8002/5000.
var DefaultPorts = []int{8000, 8001, 8002, 8080, 30000, 11434, 1234, 5000}

// Engine labels.
const (
	EngineVLLM      = "vLLM"
	EngineLlamaCpp  = "llama.cpp"
	EngineLlamaSwap = "llama-swap"
	EngineSGLang    = "SGLang"
	EngineOllama    = "Ollama"
	EngineLMStudio  = "LM Studio"
	EngineUnknown   = "OpenAI-compatible"
)

// Model is one model an endpoint serves.
type Model struct {
	ID string `json:"id"`
	// Backend and GPU come from llama-swap's metadata when it has them.
	Backend string `json:"backend,omitempty"`
	GPU     string `json:"gpu,omitempty"`
	// Loaded is false for a llama-swap model that is configured but not
	// running: the first request pays its load time, which a benchmark must
	// not count.
	Loaded bool `json:"loaded"`
}

// Endpoint is one server found.
type Endpoint struct {
	// URL is the base URL including /v1, as the benchmarks take it.
	URL     string  `json:"url"`
	Engine  string  `json:"engine"`
	Version string  `json:"version,omitempty"`
	Models  []Model `json:"models"`
}

// Options control a scan.
type Options struct {
	Hosts   []string // default 127.0.0.1
	Ports   []int    // default DefaultPorts
	Extra   []string // more base URLs or host:port, probed as given
	Timeout time.Duration
	Client  *http.Client
}

// Scan probes every host:port concurrently and returns the servers that
// answered /v1/models, sorted by URL. Unreachable ports are not errors.
func Scan(ctx context.Context, o Options) []Endpoint {
	if len(o.Hosts) == 0 {
		o.Hosts = []string{"127.0.0.1"}
	}
	if len(o.Ports) == 0 {
		o.Ports = DefaultPorts
	}
	if o.Timeout == 0 {
		o.Timeout = 1500 * time.Millisecond
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: o.Timeout}
	}
	var bases []string
	for _, h := range o.Hosts {
		for _, p := range o.Ports {
			bases = append(bases, fmt.Sprintf("http://%s:%d", h, p))
		}
	}
	for _, x := range o.Extra {
		bases = append(bases, normalize(x))
	}

	var (
		mu   sync.Mutex
		out  []Endpoint
		seen = map[string]bool{}
		wg   sync.WaitGroup
	)
	for _, b := range bases {
		if seen[b] {
			continue
		}
		seen[b] = true
		wg.Add(1)
		go func(base string) {
			defer wg.Done()
			if ep, ok := Probe(ctx, o.Client, base); ok {
				mu.Lock()
				out = append(out, ep)
				mu.Unlock()
			}
		}(b)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}

// normalize turns "host:port", "http://host:port" or ".../v1" into a base
// URL without /v1.
func normalize(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return strings.TrimSuffix(s, "/v1")
}

type modelList struct {
	Data []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
		Meta    struct {
			LlamaSwap *struct {
				Backend string `json:"backend"`
				GPU     string `json:"gpu"`
			} `json:"llamaswap"`
		} `json:"meta"`
		Status *struct {
			Value string `json:"value"`
		} `json:"status"`
	} `json:"data"`
}

// Probe checks one base URL. ok is false unless /v1/models answers with a
// model list.
func Probe(ctx context.Context, c *http.Client, base string) (Endpoint, bool) {
	var ml modelList
	if getJSON(ctx, c, base+"/v1/models", &ml) != nil {
		return Endpoint{}, false
	}
	ep := Endpoint{URL: base + "/v1", Models: []Model{}}
	owners := map[string]bool{}
	swap := false
	for _, d := range ml.Data {
		if d.ID == "" {
			continue
		}
		m := Model{ID: d.ID, Loaded: true}
		owners[strings.ToLower(d.OwnedBy)] = true
		if ls := d.Meta.LlamaSwap; ls != nil {
			swap = true
			m.Backend, m.GPU = ls.Backend, ls.GPU
		}
		if d.Status != nil && d.Status.Value != "" {
			m.Loaded = d.Status.Value == "ready" || d.Status.Value == "loaded" || d.Status.Value == "running"
		}
		ep.Models = append(ep.Models, m)
	}

	switch {
	case swap || owners["llama-swap"]:
		ep.Engine = EngineLlamaSwap
		markRunning(ctx, c, base, &ep)
	case owners["vllm"]:
		ep.Engine = EngineVLLM
		var v struct {
			Version string `json:"version"`
		}
		if getJSON(ctx, c, base+"/version", &v) == nil {
			ep.Version = v.Version
		}
	case owners["sglang"]:
		ep.Engine = EngineSGLang
		var v struct {
			Version string `json:"version"`
		}
		if getJSON(ctx, c, base+"/get_server_info", &v) == nil {
			ep.Version = v.Version
		}
	case owners["llamacpp"]:
		ep.Engine = EngineLlamaCpp
	case owners["library"]:
		ep.Engine = EngineOllama
		var v struct {
			Version string `json:"version"`
		}
		if getJSON(ctx, c, base+"/api/version", &v) == nil {
			ep.Version = v.Version
		}
	case owners["organization_owner"]:
		ep.Engine = EngineLMStudio
	default:
		ep.Engine = sniff(ctx, c, base)
	}
	return ep, true
}

// markRunning uses llama-swap's /running list, which is authoritative when the
// model list carries no status field (older llama-swap).
func markRunning(ctx context.Context, c *http.Client, base string, ep *Endpoint) {
	var r struct {
		Running []struct {
			Model string `json:"model"`
			State string `json:"state"`
		} `json:"running"`
	}
	if getJSON(ctx, c, base+"/running", &r) != nil {
		return
	}
	up := map[string]bool{}
	for _, x := range r.Running {
		if x.State == "" || x.State == "ready" {
			up[x.Model] = true
		}
	}
	for i := range ep.Models {
		ep.Models[i].Loaded = up[ep.Models[i].ID]
	}
}

// sniff tells engines apart by their status routes when owned_by did not.
func sniff(ctx context.Context, c *http.Client, base string) string {
	var props struct {
		BuildInfo string `json:"build_info"`
		ModelPath string `json:"model_path"`
	}
	if getJSON(ctx, c, base+"/props", &props) == nil && (props.BuildInfo != "" || props.ModelPath != "") {
		return EngineLlamaCpp
	}
	var v struct {
		Version string `json:"version"`
	}
	if getJSON(ctx, c, base+"/api/version", &v) == nil && v.Version != "" {
		return EngineOllama
	}
	return EngineUnknown
}

func getJSON(ctx context.Context, c *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

// FindModel returns the endpoints that serve a model id, loaded ones first.
func FindModel(eps []Endpoint, id string) []Endpoint {
	var loaded, cold []Endpoint
	for _, ep := range eps {
		for _, m := range ep.Models {
			if m.ID == id {
				if m.Loaded {
					loaded = append(loaded, ep)
				} else {
					cold = append(cold, ep)
				}
				break
			}
		}
	}
	return append(loaded, cold...)
}
