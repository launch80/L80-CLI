package pwdet

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ScanTemplateID is the launch80 template for a verifiable scan: the
// generic page fields plus the scrubbed report, so the server can recompute
// the checks and a verifier can re-run the scan from its recorded inputs.
// It is used only when the server lists it; older servers get TemplateID.
const ScanTemplateID = "scan.pwdet.v1"

// ScanMaxPayloadBytes mirrors the server's limit for ScanTemplateID.
const ScanMaxPayloadBytes = 262144

// ScanPayload is a scan.pwdet.v1 body.
type ScanPayload struct {
	Payload
	Report json.RawMessage `json:"report"`
}

// BuildScan maps a report to a scan.pwdet.v1 payload. raw is the report as
// pwdet wrote it; it is carried scrubbed, without the host block, local
// paths, endpoints or the bulky evidence data.
func BuildScan(r *Report, raw []byte, opt Options) (*ScanPayload, error) {
	p, err := Build(r, opt)
	if err != nil {
		return nil, err
	}
	p.Template = ScanTemplateID
	rep, err := ScrubReport(r, raw)
	if err != nil {
		return nil, err
	}
	return &ScanPayload{Payload: *p, Report: rep}, nil
}

// ScrubReport returns the report with everything that identifies the
// scanning machine removed. pwdet can still load it: `pwdet reproduce` reads
// repo and revision from inputs, not from the removed paths.
func ScrubReport(r *Report, raw []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotReport, err)
	}
	m["host"] = map[string]any{}
	delete(m, "manifest_path")
	refs := []any{m["subject"]}
	if rs, ok := m["references"].([]any); ok {
		refs = append(refs, rs...)
	}
	if hs, ok := m["lineage"].([]any); ok {
		for _, h := range hs {
			if hm, ok := h.(map[string]any); ok {
				refs = append(refs, hm["ref"])
			}
		}
	}
	for _, ref := range refs {
		if rm, ok := ref.(map[string]any); ok {
			rm["path"], rm["endpoint"] = nil, nil
		}
	}
	if fs, ok := m["findings"].([]any); ok {
		for _, f := range fs {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			if ev, ok := fm["evidence"].([]any); ok {
				for _, e := range ev {
					if em, ok := e.(map[string]any); ok {
						delete(em, "data")
					}
				}
			}
		}
	}
	sc := NewScrubber(r)
	scrubbed := scrubValue(m, sc)
	out, err := json.Marshal(scrubbed)
	if err != nil {
		return nil, err
	}
	if leaks := sc.Leaks(string(out)); len(leaks) > 0 {
		return nil, fmt.Errorf("refusing to publish: the report would still contain %q", leaks[0])
	}
	return out, nil
}

func scrubValue(v any, sc Scrubber) any {
	switch x := v.(type) {
	case string:
		return sc.Clean(x)
	case map[string]any:
		for k, e := range x {
			x[k] = scrubValue(e, sc)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = scrubValue(e, sc)
		}
		return x
	default:
		return v
	}
}

// ParseScan reads a published scan.pwdet.v1 body and the report it carries.
func ParseScan(payload []byte) (*ScanPayload, *Report, error) {
	var p ScanPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, nil, fmt.Errorf("not a scan.pwdet.v1 page: %v", err)
	}
	if p.Template != ScanTemplateID || len(p.Report) == 0 {
		return nil, nil, fmt.Errorf("not a scan.pwdet.v1 page (template %q)", p.Template)
	}
	r, err := Parse(p.Report)
	if err != nil {
		return nil, nil, err
	}
	return &p, r, nil
}

// IsScanPayload reports whether a published body is a scan.pwdet.v1 page.
func IsScanPayload(payload []byte) bool {
	var head struct {
		Template string `json:"$template"`
	}
	return json.Unmarshal(payload, &head) == nil && head.Template == ScanTemplateID
}

// Check is one verification pre-check, as betterbench.Check.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pass | warn | fail
	Detail string `json:"detail"`
}

// verifiableLevels can be verified; a Basic scan compares nothing against a
// reference, so reproducing it proves little.
var verifiableLevels = map[string]bool{"standard": true, "thorough": true, "deep": true}

// Prechecks are what makes a scan verifiable: a re-run on another machine
// must be able to fetch the same files and must have a digest to match.
// The server recomputes them from the stored report.
func Prechecks(r *Report) []Check {
	var out []Check
	add := func(name, status, detail string) { out = append(out, Check{name, status, detail}) }
	if r.SchemaVersion != "2" || r.Inputs == nil || r.Digest == "" {
		add("report", "fail", fmt.Sprintf("written by pwdet %s, which does not record the inputs a re-run needs; scan again with pwdet 0.2 or later", r.ToolVersion))
		return out
	}
	add("report", "pass", fmt.Sprintf("pwdet %s report with recorded inputs and a digest", r.ToolVersion))

	switch lvl := r.Level(); {
	case verifiableLevels[lvl]:
		add("level", "pass", fmt.Sprintf("%s scan", lvl))
	case lvl == "":
		add("level", "fail", "the report states no assurance level")
	default:
		add("level", "fail", fmt.Sprintf("a %s scan compares nothing against a reference; scan at Standard or above (pass --base or --parent)", lvl))
	}

	s := r.Inputs.Subject
	if s.Fetchable() {
		add("subject", "pass", fmt.Sprintf("%s@%s, fetchable by a verifier", s.Repo, shortRev(s.Revision)))
	} else {
		add("subject", "fail", "the scanned model is a local folder or an endpoint, so a verifier cannot get the same files; scan the Hugging Face repo id")
	}

	var bad, refs []string
	for hop, ref := range r.Inputs.References {
		if hop != "parent" && hop != "base" {
			continue
		}
		if ref.Fetchable() {
			refs = append(refs, fmt.Sprintf("%s %s@%s", hop, ref.Repo, shortRev(ref.Revision)))
		} else {
			bad = append(bad, hop)
		}
	}
	switch {
	case len(bad) > 0:
		add("references", "fail", fmt.Sprintf("the %s reference is a local folder; pass it as a Hugging Face repo id", strings.Join(bad, " and ")))
	case len(refs) > 0:
		add("references", "pass", strings.Join(refs, "; "))
	}

	missing := 0
	for _, f := range s.Files {
		if f.SHA256 == "" {
			missing++
		}
	}
	if missing > 0 {
		add("files", "warn", fmt.Sprintf("%d of the model's files were not hashed; a re-run compares their sizes only", missing))
	} else if len(s.Files) > 0 {
		add("files", "pass", fmt.Sprintf("%d files hashed", len(s.Files)))
	}

	switch {
	case r.Verdict == nil:
		add("verdict", "fail", "the report has no verdict")
	case r.Verdict.Label == "inconclusive":
		add("verdict", "warn", "an inconclusive scan can be reproduced, but the stamp then says little")
	}
	return out
}

// Blocking reports whether any check failed.
func Blocking(checks []Check) bool {
	for _, c := range checks {
		if c.Status == "fail" {
			return true
		}
	}
	return false
}
