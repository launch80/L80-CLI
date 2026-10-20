package app

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/history"
	"github.com/launch80/L80-CLI/internal/pwdet"
)

var guidRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// Target is what a verify command is pointed at.
type Target struct {
	GUID   string
	URL    string
	Entry  *history.Entry // the local ledger record, if this machine published it
	File   string         // the results file to check
	Data   []byte
	Checks []betterbench.Check
	Class  string // betterbench.RunClass of the results, once checked
	Passes int
	// Scan is set when the target is a pwdet report or a scan.pwdet.v1 page;
	// Level is then the scan's assurance level.
	Scan  bool
	Level string
}

// Verifiable is whether a template's pages can be verified.
func Verifiable(template string) bool {
	return template == betterbench.RawTemplateID || template == pwdet.ScanTemplateID
}

// ResolveTarget accepts a results.json or pwdet report.json path, a page URL
// or a guid. For a URL or guid it finds the ledger entry this machine wrote
// when publishing, and through it the source file.
func ResolveTarget(arg string) (*Target, *api.Error) {
	if st, err := os.Stat(arg); err == nil && !st.IsDir() {
		data, rerr := os.ReadFile(arg)
		if rerr != nil {
			return nil, api.Newf("E_INPUT_INVALID", "Check the file's permissions.", "could not read %s: %v", arg, rerr)
		}
		t := &Target{File: arg, Data: data}
		if _, perr := pwdet.Parse(data); perr == nil {
			t.Scan = true
		}
		entries, _ := history.Read()
		sum := sha256Hex(data)
		for i := range entries {
			if entries[i].SourceSHA256 == sum && Verifiable(entries[i].TemplateID) {
				t.Entry, t.GUID, t.URL = &entries[i], entries[i].GUID, entries[i].URL
				break
			}
		}
		return t, nil
	}
	guid := guidRE.FindString(strings.ToLower(arg))
	entries, _ := history.Read()
	for i := range entries {
		e := &entries[i]
		if (guid != "" && strings.EqualFold(e.GUID, guid)) || (arg != "" && e.URL == arg) {
			t := &Target{GUID: e.GUID, URL: e.URL, Entry: e, File: e.SourcePath, Scan: e.TemplateID == pwdet.ScanTemplateID || e.TemplateID == pwdet.TemplateID}
			if e.SourcePath != "" {
				t.Data, _ = os.ReadFile(e.SourcePath)
			}
			return t, nil
		}
	}
	if guid == "" {
		return nil, api.Newf("E_USAGE", "Pass a results.json, a launch80 page URL, or its guid (see `L80 history`).",
			"%q is not a file, and contains no page guid", arg)
	}
	return &Target{GUID: guid, URL: arg}, nil
}

// RunChecks fills t.Checks: the content pre-checks on the results file, and
// the provenance check that ties the page to that file.
func (t *Target) RunChecks() {
	var checks []betterbench.Check
	if t.Scan && t.Data != nil {
		if r, err := pwdet.Parse(t.Data); err == nil {
			for _, c := range pwdet.Prechecks(r) {
				checks = append(checks, betterbench.Check{Name: c.Name, Status: c.Status, Detail: c.Detail})
			}
			t.Level = r.Level()
		} else {
			checks = append(checks, betterbench.Check{Name: "report", Status: "fail", Detail: err.Error()})
		}
	} else if t.Data != nil {
		if r, err := betterbench.Parse(t.Data); err == nil {
			checks = betterbench.Prechecks(r)
			t.Class, t.Passes = betterbench.RunClass(r.Config), r.Config.RunsPerCategory
		} else {
			checks = append(checks, betterbench.Check{Name: "results", Status: "fail", Detail: err.Error()})
		}
	} else {
		checks = append(checks, betterbench.Check{Name: "results", Status: "warn",
			Detail: "the source file is not on this machine, so only launch80 can check the content"})
	}
	switch {
	case t.Entry == nil && t.GUID == "":
		// A plain file check: no page yet.
	case t.Entry == nil:
		checks = append(checks, betterbench.Check{Name: "provenance", Status: "warn",
			Detail: "this machine has no record of publishing that page; the reviewer will rely on the page alone"})
	case t.Entry.TemplateID == pwdet.TemplateID:
		checks = append(checks, betterbench.Check{Name: "provenance", Status: "fail",
			Detail: fmt.Sprintf("this scan was published as %s, which carries no report to re-run; publish it again (`L80 pwdet publish`) to get a %s page", pwdet.TemplateID, pwdet.ScanTemplateID)})
	case !Verifiable(t.Entry.TemplateID):
		checks = append(checks, betterbench.Check{Name: "provenance", Status: "fail",
			Detail: fmt.Sprintf("only %s and %s pages can be verified; this is %s", betterbench.RawTemplateID, pwdet.ScanTemplateID, t.Entry.TemplateID)})
	case t.Entry.SourceSHA256 == "":
		checks = append(checks, betterbench.Check{Name: "provenance", Status: "warn",
			Detail: "published by an L80 older than 0.5.0, which did not record the file's hash"})
	case t.Data == nil:
		checks = append(checks, betterbench.Check{Name: "provenance", Status: "warn",
			Detail: fmt.Sprintf("the file the page came from (%s) is gone", t.Entry.SourcePath)})
	case sha256Hex(t.Data) != t.Entry.SourceSHA256:
		checks = append(checks, betterbench.Check{Name: "provenance", Status: "fail",
			Detail: fmt.Sprintf("%s has changed since it was published", t.Entry.SourcePath)})
	default:
		checks = append(checks, betterbench.Check{Name: "provenance", Status: "pass",
			Detail: "the page was published from this unchanged file, by this machine"})
	}
	t.Checks = checks
}

// Request builds the verification request body.
func (t *Target) Request(note, l80Version string) api.VerificationRequest {
	req := api.VerificationRequest{Note: note, L80Version: l80Version}
	for _, c := range t.Checks {
		req.ClientChecks = append(req.ClientChecks, api.ClientCheck{Name: c.Name, Status: c.Status, Detail: c.Detail})
	}
	if t.Entry != nil {
		req.SourceSHA256, req.BodySHA256, req.ContentHash = t.Entry.SourceSHA256, t.Entry.BodySHA256, t.Entry.ContentHash
	}
	return req
}
