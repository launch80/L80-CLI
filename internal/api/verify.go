package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Verification endpoints. The contract is docs/verification-contract.md;
// the server side is implemented separately.

// ClientCheck is a pre-check result the CLI sends with a request, so the
// reviewer sees what the publisher's machine saw.
type ClientCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// VerificationRequest is the body of POST /api/v1/artifacts/{guid}/verification.
type VerificationRequest struct {
	Note         string        `json:"note,omitempty"`
	ClientChecks []ClientCheck `json:"client_checks"`
	// Provenance from the local ledger: the sha256 of the results file as
	// read, of the exact bytes sent, and the content hash the server returned
	// at publish time.
	SourceSHA256 string `json:"source_sha256,omitempty"`
	BodySHA256   string `json:"body_sha256,omitempty"`
	ContentHash  string `json:"content_hash,omitempty"`
	L80Version   string `json:"l80_version"`
}

// Verification is a request's state.
type Verification struct {
	ID           string `json:"id"`
	ArtifactGUID string `json:"artifact_guid"`
	URL          string `json:"url,omitempty"`
	Title        string `json:"title,omitempty"`
	KeyID        string `json:"key_id,omitempty"`
	Status       string `json:"status"` // requested | in_review | verified | rejected
	RequestedAt  string `json:"requested_at,omitempty"`
	ReviewedAt   string `json:"reviewed_at,omitempty"`
	Note         string `json:"note,omitempty"`
	DecisionNote string `json:"decision_note,omitempty"`
	ReviewerID   string `json:"reviewer_id,omitempty"`
	// The labels name the requesting and reviewing keys for other people;
	// the server sends them in place of key ids it keeps private.
	RequesterLabel string `json:"requester_label,omitempty"`
	ReviewerLabel  string `json:"reviewer_label,omitempty"`
	// Mine is true when the request was made with the calling key: a key
	// never sees its own requests in the queue and may not decide on them.
	Mine         bool          `json:"mine,omitempty"`
	Reproduction *Reproduction `json:"reproduction,omitempty"`
	// RunClass is full or quick, derived by the server from the stored page
	// (servers before 1.1 leave it out).
	RunClass     string        `json:"run_class,omitempty"`
	TrustTier    string        `json:"trust_tier,omitempty"`
	ClientChecks []ClientCheck `json:"client_checks,omitempty"`
	ServerChecks []ClientCheck `json:"server_checks,omitempty"`
}

// Checks are the server's checks, then the client's that the server did not
// also run: the server recomputes most of them from the stored page, and
// showing both would list each warning twice.
func (v Verification) Checks() []ClientCheck {
	out := append([]ClientCheck{}, v.ServerChecks...)
	seen := map[string]bool{}
	for _, c := range v.ServerChecks {
		seen[c.Name] = true
	}
	for _, c := range v.ClientChecks {
		if !seen[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// Requester names who asked for the review: the key's label, else its id.
func (v Verification) Requester() string { return firstOf(v.RequesterLabel, v.KeyID) }

// Reviewer names the verifier that decided, once one has.
func (v Verification) Reviewer() string { return firstOf(v.ReviewerLabel, v.ReviewerID) }

func firstOf(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func (c *Client) RequestVerification(guid string, req VerificationRequest) (*Verification, *Error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, Newf("E_INTERNAL", "Retry once.", "could not encode request: %v", err)
	}
	var out Verification
	if e := c.do(http.MethodPost, "/api/v1/artifacts/"+url.PathEscape(guid)+"/verification", body, true, &out); e != nil {
		return nil, e
	}
	return &out, nil
}

func (c *Client) VerificationStatus(guid string) (*Verification, *Error) {
	var out Verification
	if e := c.do(http.MethodGet, "/api/v1/artifacts/"+url.PathEscape(guid)+"/verification", nil, true, &out); e != nil {
		return nil, e
	}
	return &out, nil
}

// Key is the calling key as the server sees it. IsVerifier mirrors
// publisher_keys.is_verifier: only those keys may stamp a page verified.
type Key struct {
	KeyID      string `json:"key_id"`
	Label      string `json:"label,omitempty"`
	IsVerifier bool   `json:"is_verifier"`
}

// Me reports the calling key and whether it is a verifier.
func (c *Client) Me() (*Key, *Error) {
	var out Key
	if e := c.do(http.MethodGet, "/api/v1/keys/me", nil, true, &out); e != nil {
		return nil, e
	}
	return &out, nil
}

// Verifications lists requests. scope "queue" is every other key's
// requests, open to any valid key so anyone can look and reproduce; scope
// "mine" is the calling key's own. status filters (requested, in_review,
// verified, rejected); empty means all.
func (c *Client) Verifications(scope, status string) ([]Verification, *Error) {
	q := url.Values{"scope": {scope}}
	if status != "" {
		q.Set("status", status)
	}
	var out []Verification
	if e := c.do(http.MethodGet, "/api/v1/verifications?"+q.Encode(), nil, true, &out); e != nil {
		return nil, e
	}
	return out, nil
}

// Verification is one request by id.
func (c *Client) Verification(id string) (*Verification, *Error) {
	var out Verification
	if e := c.do(http.MethodGet, "/api/v1/verifications/"+url.PathEscape(id), nil, true, &out); e != nil {
		return nil, e
	}
	return &out, nil
}

// ArtifactPayload is the body a page was published with, exactly as stored:
// for a BetterBench page, the results.json a verifier reproduces.
func (c *Client) ArtifactPayload(guid string) ([]byte, *Error) {
	var out json.RawMessage
	if e := c.do(http.MethodGet, "/api/v1/artifacts/"+url.PathEscape(guid)+"/payload", nil, false, &out); e != nil {
		return nil, e
	}
	return out, nil
}

// Reproduction is a verifier's own run of a requested benchmark, compared
// with the published one. It is optional: a verifier may stamp on review
// alone, and when they did reproduce the run it travels with the decision as
// evidence.
type Reproduction struct {
	BetterBenchVersion string            `json:"betterbench_version"`
	Model              string            `json:"model"`
	Notes              map[string]string `json:"notes,omitempty"`
	RunsPerCategory    int               `json:"runs_per_category"`
	RunClass           string            `json:"run_class,omitempty"` // full | quick
	Phases             []string          `json:"phases"`
	Metrics            []MetricDelta     `json:"metrics"`
	WithinTolerance    bool              `json:"within_tolerance"`
	ResultsSHA256      string            `json:"results_sha256"`
	RanAt              string            `json:"ran_at"`
	L80Version         string            `json:"l80_version"`
	// Template is the page's template; empty means bench.betterbench.v1.
	Template string `json:"template,omitempty"`
	// Scan is set for a scan.pwdet.v1 page: pwdet reproduce's comparison of
	// the verifier's re-run with the published report. ResultsSHA256 is then
	// the hash of the re-run's report.json.
	Scan *ScanComparison `json:"scan,omitempty"`
}

// ScanComparison is `pwdet reproduce --json`: a re-run is within tolerance
// when the files hash the same, the deterministic findings match the
// published digest, and the same behavioural findings are significant.
// Pairs are [published, re-run].
type ScanComparison struct {
	WithinTolerance   bool                  `json:"within_tolerance"`
	Subject           string                `json:"subject,omitempty"` // repo@revision re-scanned
	Profile           string                `json:"profile,omitempty"`
	Verdict           [2]*string            `json:"verdict"`
	Level             [2]*string            `json:"level"`
	FilesMatch        bool                  `json:"files_match"`
	FileDifferences   []string              `json:"file_differences,omitempty"`
	DigestMatch       bool                  `json:"digest_match"`
	CheckDifferences  map[string][2]*string `json:"check_differences,omitempty"`
	Behaviour         map[string][]string   `json:"behaviour,omitempty"`
	ToolVersion       [2]*string            `json:"tool_version"`
	InputsHashesMatch bool                  `json:"inputs_hashes_match"`
}

// MetricDelta compares one headline figure.
type MetricDelta struct {
	Label        string  `json:"label"`
	Unit         string  `json:"unit,omitempty"`
	Published    float64 `json:"published"`
	Reproduced   float64 `json:"reproduced"`
	DeltaPct     float64 `json:"delta_pct"`
	TolerancePct float64 `json:"tolerance_pct"`
	Within       bool    `json:"within"`
}

// Decision is a verifier's ruling. A "rejected" one must carry a note; a
// "verified" one carries the verifier's reproduction when there is one.
type Decision struct {
	Decision     string        `json:"decision"`
	Note         string        `json:"note,omitempty"`
	Reproduction *Reproduction `json:"reproduction,omitempty"`
}

// Decide records a verifier's decision on a request.
func (c *Client) Decide(id string, d Decision) (*Verification, *Error) {
	switch {
	case d.Decision != "verified" && d.Decision != "rejected":
		return nil, Newf("E_USAGE", "Use verified or rejected.", "unknown decision %q", d.Decision)
	case d.Decision == "rejected" && strings.TrimSpace(d.Note) == "":
		return nil, Newf("E_USAGE", "Say why; the publisher sees it.", "a rejection needs a reason")
	}
	body, _ := json.Marshal(d)
	var out Verification
	if e := c.do(http.MethodPost, fmt.Sprintf("/api/v1/verifications/%s/decision", url.PathEscape(id)), body, true, &out); e != nil {
		return nil, e
	}
	return &out, nil
}

// NotSupported reports whether an error means the server has no such
// endpoint: a server that predates verification answers with a non-JSON page.
func NotSupported(e *Error) bool {
	return e != nil && e.Code == "E_SERVER" && strings.Contains(e.Message, "non-JSON")
}
