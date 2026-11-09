// Package history keeps a local record of every artifact this machine
// published, so `L80 history` works offline and can show what file each
// page came from, which the server never sees.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Entry is one published artifact. Field names match the server's listing so
// the two sources print the same way.
type Entry struct {
	GUID            string `json:"guid"`
	URL             string `json:"url"`
	TemplateID      string `json:"template_id"`
	TemplateVersion int    `json:"template_version"`
	Title           string `json:"title,omitempty"`
	ByteSize        int    `json:"byte_size"`
	CreatedAt       string `json:"created_at"`
	// Local-only context.
	SourcePath string `json:"source_path,omitempty"`
	APIBase    string `json:"api_base,omitempty"`
	// Provenance, recorded at publish time so a later verification request can
	// show the page was produced from a file that is still on this machine,
	// unchanged. SourceSHA256 hashes the file as read; BodySHA256 the exact
	// bytes sent; ContentHash is what the server reported for them.
	SourceSHA256 string `json:"source_sha256,omitempty"`
	BodySHA256   string `json:"body_sha256,omitempty"`
	ContentHash  string `json:"content_hash,omitempty"`
	// TrustTier is the server's tier for the page (e.g. "unverified").
	TrustTier string `json:"trust_tier,omitempty"`
	// RunClass is full or quick for a BetterBench page, as the server reports it.
	RunClass string `json:"run_class,omitempty"`
}

// Path is ~/.config/L80/history.jsonl, honouring XDG_CONFIG_HOME, beside the
// credentials file.
func Path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "L80", "history.jsonl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "L80", "history.jsonl")
}

// Append records an entry. One JSON object per line, appended, so a crash
// mid-write can lose at most the line being written and never the file.
func Append(e Entry) error {
	p := Path()
	if p == "" {
		return errors.New("no home directory")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if e.CreatedAt == "" {
		e.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// Read returns every entry, newest first. A missing file is an empty history,
// not an error. Malformed lines are skipped rather than failing the listing.
func Read() ([]Entry, error) {
	p := Path()
	if p == "" {
		return nil, errors.New("no home directory")
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.GUID != "" {
			out = append(out, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
