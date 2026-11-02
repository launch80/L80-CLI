// Package models lists the models a user is likely to want to run, so the
// console can offer them as a list instead of asking for a repo id: ones
// used before, ones already on disk, and a Hugging Face search.
package models

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Sources, as shown beside each model.
const (
	SrcRecent = "recent"
	SrcCache  = "HF cache"
	SrcLocal  = "~/models"
	SrcSearch = "Hugging Face"
)

// Model is one candidate.
type Model struct {
	ID     string // org/name, or a bare directory name for SrcLocal
	Source string
	Detail string
}

// IsRepo reports whether ID has the org/name shape a rental needs.
func (m Model) IsRepo() bool {
	org, name, ok := strings.Cut(m.ID, "/")
	return ok && org != "" && name != "" && !strings.Contains(name, "/")
}

// HubDir is the Hugging Face hub cache: HF_HUB_CACHE, then HF_HOME/hub, then
// ~/.cache/huggingface/hub.
func HubDir() string {
	if d := os.Getenv("HF_HUB_CACHE"); d != "" {
		return d
	}
	if d := os.Getenv("HF_HOME"); d != "" {
		return filepath.Join(d, "hub")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "huggingface", "hub")
}

// HFCache lists the model repos in a hub cache directory: each
// models--org--name directory is org/name. Datasets and spaces are skipped.
func HFCache(hub string) []Model {
	entries, err := os.ReadDir(hub)
	if err != nil {
		return nil
	}
	var out []Model
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), "models--")
		if !ok || !e.IsDir() {
			continue
		}
		org, name, ok := strings.Cut(rest, "--")
		if !ok || org == "" || name == "" {
			continue
		}
		out = append(out, Model{ID: org + "/" + name, Source: SrcCache})
	}
	return out
}

// LocalDir lists model directories directly under dir: those holding a
// config.json, safetensors or GGUF files. A directory name is not a repo id,
// so the console offers to look each one up on Hugging Face.
func LocalDir(dir string) []Model {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Model
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if looksLikeModel(filepath.Join(dir, e.Name())) {
			out = append(out, Model{ID: e.Name(), Source: SrcLocal})
		}
	}
	return out
}

func looksLikeModel(dir string) bool {
	files, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, f := range files {
		n := f.Name()
		if n == "config.json" || strings.HasSuffix(n, ".safetensors") || strings.HasSuffix(n, ".gguf") {
			return true
		}
	}
	return false
}

// SpecRepos reads model.hf_repo from the rental specs `L80 vast spec` wrote,
// newest first. It reads the one key by line rather than parsing YAML: the
// specs are L80's own output.
func SpecRepos(specsDir string) []Model {
	paths, _ := filepath.Glob(filepath.Join(specsDir, "*.yaml"))
	type spec struct {
		repo string
		mod  time.Time
	}
	var specs []spec
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if r := hfRepo(p); r != "" {
			specs = append(specs, spec{r, info.ModTime()})
		}
	}
	sort.SliceStable(specs, func(i, j int) bool { return specs[i].mod.After(specs[j].mod) })
	var out []Model
	for _, s := range specs {
		out = append(out, Model{ID: s.repo, Source: SrcRecent, Detail: "rented before"})
	}
	return out
}

func hfRepo(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "hf_repo:")
		if !ok {
			continue
		}
		v, _, _ = strings.Cut(v, "#")
		return strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return ""
}

// FromTitles picks repo ids out of published page titles ("org/name on vLLM
// 0.9", "org/name: BF16 quality reference"), newest first as given.
func FromTitles(titles []string) []Model {
	var out []Model
	for _, t := range titles {
		id := strings.TrimSpace(t)
		for _, sep := range []string{" on ", ": ", " · "} {
			id, _, _ = strings.Cut(id, sep)
		}
		if m := (Model{ID: id, Source: SrcRecent, Detail: "published before"}); m.IsRepo() && !strings.Contains(id, " ") {
			out = append(out, m)
		}
	}
	return out
}

// Merge concatenates lists, keeping the first of each id (case-insensitive).
func Merge(lists ...[]Model) []Model {
	seen := map[string]bool{}
	var out []Model
	for _, l := range lists {
		for _, m := range l {
			k := strings.ToLower(m.ID)
			if m.ID == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, m)
		}
	}
	return out
}

// HFBase is the Hugging Face API root; tests point it at a local server.
var HFBase = "https://huggingface.co"

// Search asks Hugging Face for models matching q, most downloaded first.
func Search(ctx context.Context, c *http.Client, q string, limit int) ([]Model, error) {
	if c == nil {
		c = &http.Client{Timeout: 8 * time.Second}
	}
	u := fmt.Sprintf("%s/api/models?search=%s&limit=%d&sort=downloads", HFBase, url.QueryEscape(q), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Hugging Face search: %s", resp.Status)
	}
	var rows []struct {
		ID        string `json:"id"`
		ModelID   string `json:"modelId"`
		Downloads int    `json:"downloads"`
		Pipeline  string `json:"pipeline_tag"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rows); err != nil {
		return nil, fmt.Errorf("Hugging Face search: %w", err)
	}
	var out []Model
	for _, r := range rows {
		id := r.ID
		if id == "" {
			id = r.ModelID
		}
		if id == "" {
			continue
		}
		d := fmt.Sprintf("%s downloads", humanCount(r.Downloads))
		if r.Pipeline != "" {
			d += " · " + r.Pipeline
		}
		out = append(out, Model{ID: id, Source: SrcSearch, Detail: d})
	}
	return out, nil
}

func humanCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}
