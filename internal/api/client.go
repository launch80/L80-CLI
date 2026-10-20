package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"time"
)

// MaxPayloadBytes is the server's default per-payload limit, which applies to
// every hand-written report template.
const MaxPayloadBytes = 65536

// rawResultsMaxBytes mirrors the server's raised limit for
// bench.betterbench.v1, which stores a harness's results file as written.
const rawResultsMaxBytes = 4 * 1024 * 1024

// scanMaxBytes mirrors the server's limit for scan.pwdet.v1, whose body
// carries the scrubbed pwdet report (pwdet.ScanMaxPayloadBytes).
const scanMaxBytes = 262144

// MaxPayloadBytesFor is the client-side limit for a template. The server is
// the authority; this only lets the CLI fail fast with the right number.
func MaxPayloadBytesFor(template string) int {
	if template == "bench.betterbench.v1" {
		return rawResultsMaxBytes
	}
	if template == "scan.pwdet.v1" {
		return scanMaxBytes
	}
	return MaxPayloadBytes
}

type Client struct {
	BaseURL string
	Token   string
	Version string
	http    *http.Client
}

func NewClient(baseURL, token, version string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		Version: version,
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) userAgent() string {
	return fmt.Sprintf("L80/%s (%s/%s)", c.Version, runtime.GOOS, runtime.GOARCH)
}

// envelope matches the server's uniform {ok, data|error} response shape.
type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *Error          `json:"error"`
}

type Artifact struct {
	GUID            string `json:"guid"`
	URL             string `json:"url"`
	TemplateID      string `json:"template_id"`
	TemplateVersion int    `json:"template_version"`
	TrustTier       string `json:"trust_tier"`
	ContentHash     string `json:"content_hash"`
	ByteSize        int    `json:"byte_size"`
	CreatedAt       string `json:"created_at"`
}

type Template struct {
	ID        string `json:"id"`
	Version   int    `json:"version"`
	Label     string `json:"label"`
	SchemaURL string `json:"schema_url"`
}

type Health struct {
	Service   string   `json:"service"`
	Version   string   `json:"version"`
	Templates []string `json:"templates"`
}

// Publish POSTs a payload verbatim. The CLI does not rewrite the body: the
// bytes the caller wrote are the bytes that get validated and stored.
func (c *Client) Publish(payload []byte) (*Artifact, *Error) {
	var out Artifact
	if err := c.do(http.MethodPost, "/api/v1/artifacts", payload, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HistoryPage is the server's listing of artifacts published under the
// caller's key, newest first.
type HistoryPage struct {
	KeyID      string         `json:"key_id"`
	Artifacts  []HistoryEntry `json:"artifacts"`
	NextBefore *string        `json:"next_before"`
}

type HistoryEntry struct {
	GUID            string `json:"guid"`
	URL             string `json:"url"`
	TemplateID      string `json:"template_id"`
	TemplateVersion int    `json:"template_version"`
	TrustTier       string `json:"trust_tier"`
	RunClass        string `json:"run_class,omitempty"` // full | quick, BetterBench pages (server 1.1+)
	Title           string `json:"title"`
	ByteSize        int    `json:"byte_size"`
	CreatedAt       string `json:"created_at"`
}

// History lists what this key has published. `before` pages backwards from a
// created_at timestamp; empty means the newest page.
func (c *Client) History(limit int, before string) (*HistoryPage, *Error) {
	path := fmt.Sprintf("/api/v1/artifacts?limit=%d", limit)
	if before != "" {
		path += "&before=" + url.QueryEscape(before)
	}
	var out HistoryPage
	if err := c.do(http.MethodGet, path, nil, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Templates() ([]Template, *Error) {
	var out []Template
	if err := c.do(http.MethodGet, "/api/v1/templates", nil, false, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) Health() (*Health, *Error) {
	var out Health
	if err := c.do(http.MethodGet, "/api/v1/health", nil, false, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) do(method, path string, body []byte, auth bool, out any) *Error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return Newf("E_USAGE", "Check the --api-base value.", "could not build request: %v", err)
	}

	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		if c.Token == "" {
			return Newf(
				"E_TOKEN_MISSING",
				"Set L80_TOKEN, or run `L80 auth status` to see where the CLI looks. Do not paste a token into a chat.",
				"no publish token is configured",
			)
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Never interpolate the request: the Authorization header must not be
		// able to reach stderr through an error string.
		return Newf("E_NETWORK", "Check connectivity and the --api-base value, then retry.",
			"could not reach %s", c.BaseURL)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Newf("E_NETWORK", "Retry the command.", "could not read the response body")
	}

	var env envelope
	if jsonErr := json.Unmarshal(raw, &env); jsonErr != nil {
		return Newf("E_SERVER", "Retry once. If it persists, the API may be down.",
			"server returned a non-JSON %d response", resp.StatusCode)
	}

	if !env.OK || env.Error != nil {
		if env.Error != nil {
			return env.Error
		}
		return Newf("E_SERVER", "Retry once with backoff.", "server returned %d", resp.StatusCode)
	}

	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return Newf("E_SERVER", "Upgrade the CLI with `L80 update`.",
				"could not decode the response: %v", err)
		}
	}
	return nil
}
