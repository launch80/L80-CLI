package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkdirs(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, p)
		if strings.HasSuffix(p, "/") {
			os.MkdirAll(full, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte("{}"), 0o644)
	}
}

func ids(ms []Model) string {
	var s []string
	for _, m := range ms {
		s = append(s, m.ID)
	}
	return strings.Join(s, ",")
}

func TestHFCache(t *testing.T) {
	hub := t.TempDir()
	mkdirs(t, hub, "models--Qwen--Qwen3-0.6B/", "models--Wrapzii--Swift-1.5-v1-embed-int8/", "datasets--x--y/", "models--broken/", "CACHEDIR.TAG")
	got := HFCache(hub)
	if ids(got) != "Qwen/Qwen3-0.6B,Wrapzii/Swift-1.5-v1-embed-int8" || got[0].Source != SrcCache {
		t.Errorf("got %v", got)
	}
	if HFCache(filepath.Join(hub, "absent")) != nil {
		t.Error("a missing cache is empty, not an error")
	}
}

func TestHubDirEnv(t *testing.T) {
	t.Setenv("HF_HUB_CACHE", "")
	t.Setenv("HF_HOME", "/x/hf")
	if HubDir() != "/x/hf/hub" {
		t.Errorf("HF_HOME: %s", HubDir())
	}
	t.Setenv("HF_HUB_CACHE", "/y")
	if HubDir() != "/y" {
		t.Errorf("HF_HUB_CACHE: %s", HubDir())
	}
}

func TestLocalDir(t *testing.T) {
	d := t.TempDir()
	mkdirs(t, d, "Qwopus3.8-27B-Coder-Flash/config.json", "gguf/model-Q4_K_M.gguf", "bake/logs/", ".hidden/config.json", "notes.txt")
	got := LocalDir(d)
	if ids(got) != "Qwopus3.8-27B-Coder-Flash,gguf" || got[0].IsRepo() {
		t.Errorf("got %v", got)
	}
}

func TestSpecRepos(t *testing.T) {
	d := t.TempDir()
	old := filepath.Join(d, "a.yaml")
	os.WriteFile(old, []byte("model:\n  id: \"x\"\n  hf_repo: \"Org/Old\"   # comment\n"), 0o644)
	os.WriteFile(filepath.Join(d, "b.yaml"), []byte("model:\n  hf_repo: Org/New\n"), 0o644)
	os.WriteFile(filepath.Join(d, "c.yaml"), []byte("model:\n  source: url\n"), 0o644)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(old, past, past)
	if got := SpecRepos(d); ids(got) != "Org/New,Org/Old" || got[0].Source != SrcRecent {
		t.Errorf("got %v", got)
	}
}

func TestFromTitlesAndMerge(t *testing.T) {
	got := FromTitles([]string{"Qwen/Qwen3-8B on vLLM 0.9.3", "Jackrong/Qwopus3.8-27B-Coder-Flash: BF16 quality reference", "Project 608 roadmap", "a/b/c", ""})
	if ids(got) != "Qwen/Qwen3-8B,Jackrong/Qwopus3.8-27B-Coder-Flash" {
		t.Errorf("titles: %v", got)
	}
	m := Merge(got, []Model{{ID: "qwen/qwen3-8b", Source: SrcCache}, {ID: "Org/New", Source: SrcCache}})
	if ids(m) != "Qwen/Qwen3-8B,Jackrong/Qwopus3.8-27B-Coder-Flash,Org/New" || m[0].Source != SrcRecent {
		t.Errorf("merge keeps the first of each id: %v", m)
	}
}

func TestSearch(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`[{"id":"Qwen/Qwen3-8B","downloads":1234567,"pipeline_tag":"text-generation"},{"modelId":"x/y","downloads":12}]`))
	}))
	defer srv.Close()
	old := HFBase
	HFBase = srv.URL
	defer func() { HFBase = old }()
	got, err := Search(context.Background(), nil, "qwen3 8b", 20)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "search=qwen3+8b&limit=20&sort=downloads" {
		t.Errorf("query %q", gotQuery)
	}
	if ids(got) != "Qwen/Qwen3-8B,x/y" || got[0].Detail != "1.2M downloads · text-generation" || got[1].Detail != "12 downloads" {
		t.Errorf("got %+v", got)
	}

	HFBase = srv.URL + "/nope"
	if _, err := Search(context.Background(), nil, "q", 5); err == nil {
		t.Error("a non-200 answer is an error")
	}
}
