package betterbench

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNamesGPU(t *testing.T) {
	cases := map[string]bool{
		`{"env":{"gpu":{}}}`: false,
		`{"env":{"gpu":{"nvidia_smi":["NVIDIA RTX PRO 6000, 580"]}}}`: true,
		`{"env":{"gpu":{"vendor":"intel"}}}`:                          true,
		`{"env":{"gpu":{},"notes":{"gpu":"Arc Pro B65 32GB"}}}`:       true,
		`{"env":{"gpu":{},"notes":{"hardware":"2x H200 NVL"}}}`:       true,
		`{"env":{"gpu":{},"notes":{"gpu":"  "}}}`:                     false,
		`not json`: false,
	}
	for in, want := range cases {
		if got := NamesGPU([]byte(in)); got != want {
			t.Errorf("NamesGPU(%s) = %v, want %v", in, got, want)
		}
	}
}

func TestWithGPUNote(t *testing.T) {
	body := []byte(`{"$template":"bench.betterbench.v1","env":{"model":"m","notes":{"engine":"vllm"},"gpu":{}},"single_stream":[1.50]}`)
	out, err := WithGPUNote(body, "  Arc  Pro B65 32GB ")
	if err != nil {
		t.Fatal(err)
	}
	var top struct {
		Env struct {
			Notes map[string]string `json:"notes"`
		} `json:"env"`
	}
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatal(err)
	}
	if top.Env.Notes["gpu"] != "Arc Pro B65 32GB" || top.Env.Notes["engine"] != "vllm" {
		t.Fatalf("notes = %v", top.Env.Notes)
	}
	// Numbers are copied as written, never re-encoded.
	if !strings.Contains(string(out), `[1.50]`) {
		t.Fatalf("number re-encoded: %s", out)
	}
	if !NamesGPU(out) {
		t.Fatal("NamesGPU false after WithGPUNote")
	}
	// No notes object yet, and an empty name.
	out2, err := WithGPUNote([]byte(`{"env":{"model":"m"}}`), "RTX 3090")
	if err != nil || !strings.Contains(string(out2), `"notes":{"gpu":"RTX 3090"}`) {
		t.Fatalf("got %s, %v", out2, err)
	}
	same, _ := WithGPUNote(body, "")
	if string(same) != string(body) {
		t.Fatal("empty name changed the body")
	}
	if _, err := WithGPUNote(body, strings.Repeat("x", MaxGPUNote+1)); err == nil {
		t.Fatal("over-long name accepted")
	}
}
