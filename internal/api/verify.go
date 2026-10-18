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
	ID           string        `json:"id"`
	ArtifactGUID string        `json:"artifact_guid"`
	URL          string        `json:"url,omitempty"`
	Title        string        `json:"title,omitempty"`
	KeyID        string        `json:"key_id,omitempty"`
	Status       string        `json:"status"` // requested | in_review | verified | rejected
	RequestedAt  string        `json:"requested_at,omitempty"`
	ReviewedAt   string        `json:"reviewed_at,omitempty"`
	Note         string        `json:"note,omitempty"`
	DecisionNote string        `json:"decision_note,omitempty"`
	TrustTier    string        `json:"trust_tier,omitempty"`
	ClientChecks []ClientCheck `json:"client_checks,omitempty"`
	ServerChecks []ClientCheck `json:"server_checks,omitempty"`
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

// ReviewQueue lists verification requests for a reviewer-role key.
func (c *Client) ReviewQueue(status string) ([]Verification, *Error) {
	path := "/api/v1/review/queue"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var out []Verification
	if e := c.do(http.MethodGet, path, nil, true, &out); e != nil {
		return nil, e
	}
	return out, nil
}

// ReviewDecision records a reviewer's decision: "verified" or "rejected".
func (c *Client) ReviewDecision(id, decision, note string) (*Verification, *Error) {
	if decision != "verified" && decision != "rejected" {
		return nil, Newf("E_USAGE", "Use verified or rejected.", "unknown decision %q", decision)
	}
	body, _ := json.Marshal(map[string]string{"decision": decision, "note": note})
	var out Verification
	if e := c.do(http.MethodPost, fmt.Sprintf("/api/v1/review/%s/decision", url.PathEscape(id)), body, true, &out); e != nil {
		return nil, e
	}
	return &out, nil
}

// NotSupported reports whether an error means the server has no such
// endpoint: a server that predates verification answers with a non-JSON page.
func NotSupported(e *Error) bool {
	return e != nil && e.Code == "E_SERVER" && strings.Contains(e.Message, "non-JSON")
}
