// Package pwdet reads a Poisoned Weights Detector report (schema/report.v1
// in the pwdet repository) and maps it onto launch80's generic.report.v1
// template for publishing.
//
// A pwdet report is written for the machine that ran it: it records the
// hostname, the platform, absolute snapshot paths and sometimes a LAN
// endpoint. None of that may reach a public page, so the mapping builds the
// page only from fields that describe the model and the checks, and then
// scrubs every string it emits.
package pwdet

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Report is the subset of pwdet's Report model the mapping reads.
type Report struct {
	SchemaVersion string     `json:"schema_version"`
	ToolVersion   string     `json:"tool_version"`
	Started       string     `json:"started"`
	Finished      string     `json:"finished"`
	Host          Host       `json:"host"`
	Profile       string     `json:"profile"`
	Subject       ModelRef   `json:"subject"`
	References    []ModelRef `json:"references"`
	Lineage       []Hop      `json:"lineage"`
	Findings      []Finding  `json:"findings"`
	Verdict       *Verdict   `json:"verdict"`
}

// Host is read only so its values can be scrubbed from every other string.
type Host struct {
	Hostname string `json:"hostname"`
	Platform string `json:"platform"`
}

type ModelRef struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Repo        string `json:"repo"`
	Revision    string `json:"revision"`
	Path        string `json:"path"`
	Endpoint    string `json:"endpoint"`
	ServedModel string `json:"served_model"`
}

// Label names a model without saying where it lives on this machine: the Hub
// repo, else the served model name, else the last path element, else the key.
func (m ModelRef) Label() string {
	switch {
	case m.Repo != "":
		return m.Repo
	case m.ServedModel != "":
		return m.ServedModel
	case m.Path != "":
		return path.Base(strings.TrimRight(m.Path, "/"))
	default:
		return m.Key
	}
}

type Hop struct {
	Hop      string   `json:"hop"`
	Ref      ModelRef `json:"ref"`
	Relation string   `json:"relation"`
	Verified bool     `json:"verified"`
}

type Finding struct {
	CheckID     string     `json:"check_id"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	Severity    string     `json:"severity"`
	Confidence  float64    `json:"confidence"`
	Declared    bool       `json:"declared"`
	Hop         string     `json:"hop"`
	Remediation string     `json:"remediation"`
	Evidence    []Evidence `json:"evidence"`
}

// Evidence is what a check saw: a file, a Hub record, a metric or a note.
type Evidence struct {
	Kind    string `json:"kind"`
	Locator string `json:"locator"`
	Summary string `json:"summary"`
}

var severityOrder = map[string]int{"info": 0, "low": 1, "medium": 2, "high": 3, "critical": 4}

// EffectiveSeverity mirrors pwdet: a disclosed modification is downgraded
// one step, never hidden.
func (f Finding) EffectiveSeverity() string {
	if !f.Declared {
		return f.Severity
	}
	names := []string{"info", "low", "medium", "high", "critical"}
	if i := severityOrder[f.Severity]; i > 0 {
		return names[i-1]
	}
	return "info"
}

type Verdict struct {
	Label     string                    `json:"label"`
	Rationale []string                  `json:"rationale"`
	Coverage  map[string]map[string]int `json:"coverage"`
}

// ErrNotReport means the JSON is not a pwdet report.v1.
var ErrNotReport = errors.New("not a pwdet report.v1")

// Parse decodes a report and checks it is one.
func Parse(data []byte) (*Report, error) {
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotReport, err)
	}
	if r.SchemaVersion != "1" || r.ToolVersion == "" {
		return nil, fmt.Errorf("%w: schema_version %q", ErrNotReport, r.SchemaVersion)
	}
	return &r, nil
}

// Counts totals findings by status across every layer.
func (r *Report) Counts() map[string]int {
	out := map[string]int{}
	for _, f := range r.Findings {
		out[f.Status]++
	}
	return out
}

// ToReview is the warn and fail findings, worst first.
func (r *Report) ToReview() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Status == "warn" || f.Status == "fail" {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Status == "fail") != (b.Status == "fail") {
			return a.Status == "fail"
		}
		if sa, sb := severityOrder[a.EffectiveSeverity()], severityOrder[b.EffectiveSeverity()]; sa != sb {
			return sa > sb
		}
		return a.Confidence > b.Confidence
	})
	return out
}

// Ordered is every finding, the ones to review first (as ToReview), then
// passes, then skipped and errored checks.
func (r *Report) Ordered() []Finding {
	rank := map[string]int{"fail": 0, "warn": 1, "pass": 2}
	out := append([]Finding(nil), r.Findings...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		ra, ok := rank[a.Status]
		if !ok {
			ra = 3
		}
		rb, ok := rank[b.Status]
		if !ok {
			rb = 3
		}
		if ra != rb {
			return ra < rb
		}
		if sa, sb := severityOrder[a.EffectiveSeverity()], severityOrder[b.EffectiveSeverity()]; sa != sb {
			return sa > sb
		}
		return a.Confidence > b.Confidence
	})
	return out
}

// --- scrubbing ---------------------------------------------------------------

var (
	// An absolute POSIX path of two or more components, or a Windows one.
	absPathRE = regexp.MustCompile(`(?:~|/(?:home|Users|root|tmp|var|mnt|opt|srv|data|media|private|run))(?:/[^\s"'<>|,;)\]]+)+|[A-Za-z]:\\(?:[^\s"'<>|\\]+\\)*[^\s"'<>|\\]*`)
	// A URL whose host is loopback or a private range.
	localURLRE = regexp.MustCompile(`https?://(?:localhost|127\.\d+\.\d+\.\d+|0\.0\.0\.0|10\.\d+\.\d+\.\d+|192\.168\.\d+\.\d+|172\.(?:1[6-9]|2\d|3[01])\.\d+\.\d+|\[::1\]|[A-Za-z0-9-]+\.(?:local|lan|internal|home))(?::\d+)?[^\s"'<>]*`)
)

// Scrubber removes machine-identifying text from strings bound for a page.
type Scrubber struct{ secrets []string }

// NewScrubber builds a scrubber that also removes the report's own hostname.
func NewScrubber(r *Report) Scrubber {
	var s []string
	if h := strings.TrimSpace(r.Host.Hostname); len(h) >= 3 {
		s = append(s, h)
	}
	return Scrubber{secrets: s}
}

// Clean replaces local paths with their last element, local URLs with a
// placeholder, and the hostname with "<host>".
func (s Scrubber) Clean(text string) string {
	text = localURLRE.ReplaceAllString(text, "<local endpoint>")
	text = absPathRE.ReplaceAllStringFunc(text, func(p string) string {
		p = strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
		return "…/" + path.Base(p)
	})
	for _, h := range s.secrets {
		text = strings.ReplaceAll(text, h, "<host>")
	}
	return text
}

// Leaks reports which identifying fragments survive in text: used by tests
// and as a last check before a body is sent.
func (s Scrubber) Leaks(text string) []string {
	var out []string
	if m := absPathRE.FindString(text); m != "" {
		out = append(out, m)
	}
	if m := localURLRE.FindString(text); m != "" {
		out = append(out, m)
	}
	for _, h := range s.secrets {
		if strings.Contains(text, h) {
			out = append(out, h)
		}
	}
	return out
}
