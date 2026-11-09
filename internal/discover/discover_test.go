package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// server answers each path with a fixed body; anything else is a 404.
func server(t *testing.T, routes map[string]string) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s.URL
}

func probe(t *testing.T, base string) Endpoint {
	t.Helper()
	ep, ok := Probe(context.Background(), http.DefaultClient, base)
	if !ok {
		t.Fatalf("%s: not detected", base)
	}
	return ep
}

func TestLlamaSwap(t *testing.T) {
	// Shape captured from llama-swap on the B65 box, 2026-11-02.
	base := server(t, map[string]string{
		"/v1/models": `{"object":"list","data":[
			{"id":"Launch80/Qwen3.8-27B-GPTQ-Int4-baked","object":"model","owned_by":"llama-swap",
			 "meta":{"llamaswap":{"aliases":["launch80/qwen3.8-27b-gptq-int4-baked"],"backend":"vllm","gpu":"b65","type":"model"}},
			 "status":{"value":"unloaded"}},
			{"id":"Qwen/Qwen3-0.6B-GGUF@b580","object":"model","owned_by":"llama-swap",
			 "meta":{"llamaswap":{"backend":"llama.cpp","gpu":"b580","type":"model"}},"status":{"value":"unloaded"}}]}`,
		"/running": `{"running":[{"model":"Qwen/Qwen3-0.6B-GGUF@b580","state":"ready"}]}`,
	})
	ep := probe(t, base)
	if ep.Engine != EngineLlamaSwap || ep.URL != base+"/v1" || len(ep.Models) != 2 {
		t.Fatalf("got %+v", ep)
	}
	m0, m1 := ep.Models[0], ep.Models[1]
	if m0.Backend != "vllm" || m0.GPU != "b65" || m0.Loaded {
		t.Errorf("model 0 = %+v", m0)
	}
	if !m1.Loaded || m1.Backend != "llama.cpp" {
		t.Errorf("model 1 should be loaded per /running: %+v", m1)
	}
}

func TestEnginesByOwner(t *testing.T) {
	cases := []struct {
		name, engine, version string
		routes                map[string]string
	}{
		{"vllm", EngineVLLM, "0.30.0", map[string]string{
			"/v1/models": `{"data":[{"id":"Qwen/Qwen3-8B","owned_by":"vllm"}]}`, "/version": `{"version":"0.30.0"}`}},
		{"sglang", EngineSGLang, "0.5.1", map[string]string{
			"/v1/models": `{"data":[{"id":"m","owned_by":"sglang"}]}`, "/get_server_info": `{"version":"0.5.1"}`}},
		{"llama.cpp", EngineLlamaCpp, "", map[string]string{
			"/v1/models": `{"data":[{"id":"qwen.gguf","owned_by":"llamacpp"}]}`}},
		{"ollama", EngineOllama, "0.12.0", map[string]string{
			"/v1/models": `{"data":[{"id":"qwen3:8b","owned_by":"library"}]}`, "/api/version": `{"version":"0.12.0"}`}},
		{"lmstudio", EngineLMStudio, "", map[string]string{
			"/v1/models": `{"data":[{"id":"m","owned_by":"organization_owner"}]}`}},
		{"llama.cpp by props", EngineLlamaCpp, "", map[string]string{
			"/v1/models": `{"data":[{"id":"m","owned_by":"someone"}]}`, "/props": `{"build_info":"b6000"}`}},
		{"unknown", EngineUnknown, "", map[string]string{
			"/v1/models": `{"data":[{"id":"m"}]}`}},
	}
	for _, c := range cases {
		ep := probe(t, server(t, c.routes))
		if ep.Engine != c.engine || ep.Version != c.version {
			t.Errorf("%s: engine %q version %q, want %q %q", c.name, ep.Engine, ep.Version, c.engine, c.version)
		}
		if len(ep.Models) != 1 || !ep.Models[0].Loaded {
			t.Errorf("%s: models %+v", c.name, ep.Models)
		}
	}
}

func TestNotAnEndpoint(t *testing.T) {
	base := server(t, map[string]string{"/": `{}`})
	if _, ok := Probe(context.Background(), http.DefaultClient, base); ok {
		t.Error("a server without /v1/models must not be listed")
	}
}

func TestScanExtraAndFind(t *testing.T) {
	a := server(t, map[string]string{"/v1/models": `{"data":[{"id":"m","owned_by":"vllm"}]}`})
	b := server(t, map[string]string{"/v1/models": `{"data":[{"id":"m","owned_by":"llama-swap","status":{"value":"unloaded"}},{"id":"n","owned_by":"llama-swap"}]}`})
	// Ports nobody listens on cost nothing; the test servers come in as extras,
	// one of them in the ".../v1" form users paste.
	eps := Scan(context.Background(), Options{Ports: []int{1}, Extra: []string{a, strings.TrimPrefix(b, "http://") + "/v1/"}})
	if len(eps) != 2 {
		t.Fatalf("found %d endpoints: %+v", len(eps), eps)
	}
	hits := FindModel(eps, "m")
	if len(hits) != 2 || hits[0].URL != a+"/v1" {
		t.Errorf("loaded server first: %+v", hits)
	}
	if len(FindModel(eps, "zzz")) != 0 {
		t.Error("unknown model matched")
	}
}

func TestTokenizerGuess(t *testing.T) {
	cases := []struct {
		m    Model
		want string
	}{
		{Model{ID: "my-alias", Root: "Qwen/Qwen3-8B"}, "Qwen/Qwen3-8B"},
		{Model{ID: "x", Root: "/models/qwen"}, "/models/qwen"},
		{Model{ID: "Qwen/Qwen3-8B"}, "Qwen/Qwen3-8B"},
		{Model{ID: "Qwen/Qwen3-0.6B-GGUF@b580"}, ""},
		{Model{ID: "qwen.gguf"}, ""},
	}
	for _, c := range cases {
		if got := c.m.Tokenizer(); got != c.want {
			t.Errorf("%+v: got %q want %q", c.m, got, c.want)
		}
	}
}
