package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/pwdet"
	"github.com/launch80/L80-CLI/internal/pytool"
)

// A pwdet scan is published as scan.pwdet.v1 when the server offers it: the
// page plus the scrubbed report, which is what makes it verifiable. A
// verifier re-runs the scan with `pwdet reproduce` from the inputs the
// report records, and the comparison goes with the stamp.

// ServerHasTemplate reports whether the server lists a template. A server
// that cannot be asked counts as not having it.
func ServerHasTemplate(client *api.Client, id string) bool {
	ts, apiErr := client.Templates()
	if apiErr != nil {
		return false
	}
	for _, t := range ts {
		if t.ID == id {
			return true
		}
	}
	return false
}

// PwdetPage is a report ready to publish.
type PwdetPage struct {
	Body     []byte
	Template string
	Title    string
	Report   *pwdet.Report
	// Why is set when the page is generic.report.v1 although the report could
	// have been verifiable, saying why.
	Why string
}

// BuildPwdetPage builds the page for a report: scan.pwdet.v1 when scan is
// set (the server lists it) and the report records its inputs, else
// generic.report.v1.
func BuildPwdetPage(raw []byte, opt pwdet.Options, scan bool) (*PwdetPage, error) {
	rep, err := pwdet.Parse(raw)
	if err != nil {
		return nil, err
	}
	pg := &PwdetPage{Report: rep}
	if scan && rep.Inputs != nil {
		sp, err := pwdet.BuildScan(rep, raw, opt)
		if err != nil {
			return nil, err
		}
		body, _ := json.Marshal(sp)
		if len(body) <= api.MaxPayloadBytesFor(pwdet.ScanTemplateID) {
			pg.Body, pg.Template, pg.Title = body, pwdet.ScanTemplateID, sp.Title
			return pg, nil
		}
		pg.Why = fmt.Sprintf("the report is too large for %s (%d bytes), so the page cannot be verified", pwdet.ScanTemplateID, len(body))
	} else if scan {
		pg.Why = "the report was written by pwdet before 0.2 and records no inputs, so the page cannot be verified"
	} else if rep.Inputs != nil {
		pg.Why = "the launch80 server did not list " + pwdet.ScanTemplateID + " (older server, or not reachable), so the page cannot be verified"
	}
	p, err := pwdet.Build(rep, opt)
	if err != nil {
		return nil, err
	}
	pg.Body, _ = json.Marshal(p)
	pg.Template, pg.Title = pwdet.TemplateID, p.Title
	if len(pg.Body) > api.MaxPayloadBytesFor(pwdet.TemplateID) {
		return nil, fmt.Errorf("the page is %d bytes, over the %d limit", len(pg.Body), api.MaxPayloadBytesFor(pwdet.TemplateID))
	}
	return pg, nil
}

// ScanPlan is how to reproduce a published scan.
type ScanPlan struct {
	Subject     string   `json:"subject"` // repo@revision
	References  []string `json:"references,omitempty"`
	Profile     string   `json:"profile"`
	Level       string   `json:"level"`
	Verdict     string   `json:"verdict"`
	Digest      string   `json:"digest"`
	ToolVersion string   `json:"pwdet_version"`
	Serve       bool     `json:"serve"` // the scan served models for behavioural probes
	Checks      []pwdet.Check
	report      []byte
}

// ScanPlanFrom reads a published scan.pwdet.v1 body.
func ScanPlanFrom(payload []byte) (ScanPlan, error) {
	sp, r, err := pwdet.ParseScan(payload)
	if err != nil {
		return ScanPlan{}, err
	}
	p := ScanPlan{Level: r.Level(), Digest: r.Digest, ToolVersion: r.ToolVersion, Checks: pwdet.Prechecks(r), report: sp.Report}
	if r.Verdict != nil {
		p.Verdict = r.Verdict.Label
	}
	if in := r.Inputs; in != nil {
		p.Profile = in.Profile
		if in.Subject.Repo != "" {
			p.Subject = in.Subject.Repo + "@" + in.Subject.Revision
		}
		hops := make([]string, 0, len(in.References))
		for hop := range in.References {
			hops = append(hops, hop)
		}
		sort.Strings(hops)
		for _, hop := range hops {
			ref := in.References[hop]
			p.References = append(p.References, fmt.Sprintf("%s %s@%s", hop, ref.Repo, ref.Revision))
		}
		p.Serve, _ = in.Options["serve"].(bool)
	}
	return p, nil
}

// ScanOptions point a re-run at local copies when the files are not where
// the Hub has them.
type ScanOptions struct {
	Model, Base string // local folders, passed to pwdet reproduce as --model/--base
	Dir         string // where the report and the re-run go; default beside the ledger
}

// ReproduceScan re-runs a published scan with the pinned pwdet and returns
// the comparison as a reproduction.
func ReproduceScan(ctx context.Context, id string, payload []byte, o ScanOptions, version string, s Sink) (*api.Reproduction, *api.Error) {
	plan, err := ScanPlanFrom(payload)
	if err != nil {
		return nil, api.Newf("E_INPUT_INVALID", "Only scan.pwdet.v1 pages can be re-scanned.", "%v", err)
	}
	if pwdet.Blocking(plan.Checks) {
		for _, c := range plan.Checks {
			if c.Status == "fail" {
				return nil, api.Newf("E_VERIFY_INELIGIBLE", "Ask the publisher to scan the Hub repo at Standard or above with pwdet 0.2+, and publish again.", "this scan cannot be re-run: %s", c.Detail)
			}
		}
	}
	t, err := pytool.Lookup("pwdet")
	if err != nil {
		return nil, api.Newf("E_TOOL_UNAVAILABLE", "Run `L80 tools check pwdet`.", "%v", err)
	}
	runner, err := pytool.Resolve(t)
	if err != nil {
		return nil, api.Newf("E_TOOL_UNAVAILABLE", "Run `L80 tools check pwdet`.", "%v", err)
	}
	dir := o.Dir
	if dir == "" {
		dir = filepath.Join(filepath.Dir(reproductionPath(id)), strings.TrimSuffix(filepath.Base(reproductionPath(id)), ".json")+"-"+time.Now().Format("20060102-150405"))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, api.Newf("E_INPUT_INVALID", "Pass a writable directory.", "%v", err)
	}
	reportPath := filepath.Join(dir, "published-report.json")
	if err := os.WriteFile(reportPath, plan.report, 0o600); err != nil {
		return nil, api.Newf("E_INPUT_INVALID", "Pass a writable directory.", "%v", err)
	}
	args := []string{"reproduce", reportPath, "--json", "--out", filepath.Join(dir, "rerun")}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Base != "" {
		args = append(args, "--base", o.Base)
	}
	s.Note(Step, "re-scanning %s (%s) with pwdet via %s", orNone(plan.Subject), plan.Profile, runner.Source)
	if plan.Serve {
		s.Note(Step, "this scan probed behaviour: pwdet serves the model and its reference on this machine's GPU, which takes minutes per model")
	}
	s.Note(Step, "%s", pytool.FormatArgv(append(append([]string{}, runner.Argv...), args...)))
	var stdout bytes.Buffer
	code, err := pytool.Exec(ctx, runner, args, pytool.ExecOpts{Stdout: &stdout, Stderr: s.Output()})
	if err != nil {
		return nil, api.Newf("E_TOOL_UNAVAILABLE", "Run `L80 tools check pwdet`.", "pwdet reproduce: %v", err)
	}
	cmp, rerun, perr := parseScanComparison(stdout.String())
	if perr != nil || (code != 0 && code != 1) {
		fmt.Fprint(s.Output(), stdout.String())
		return nil, api.Newf("E_INPUT_INVALID", "Fix what pwdet reported above and retry.", "pwdet reproduce exited %d without a comparison", code)
	}
	cmp.Subject, cmp.Profile = plan.Subject, plan.Profile
	rep := &api.Reproduction{Model: plan.Subject, Template: pwdet.ScanTemplateID, Scan: cmp, WithinTolerance: cmp.WithinTolerance,
		RanAt: time.Now().UTC().Format(time.RFC3339), L80Version: version, Phases: []string{}, Metrics: []api.MetricDelta{}}
	if data, err := os.ReadFile(rerun); err == nil {
		rep.ResultsSHA256 = sha256Hex(data)
	}
	return rep, nil
}

// parseScanComparison reads `pwdet reproduce --json`: a status line, then
// the comparison as an indented JSON object.
func parseScanComparison(out string) (*api.ScanComparison, string, error) {
	i := strings.Index(out, "\n{")
	if strings.HasPrefix(out, "{") {
		i = -1
	} else if i < 0 {
		return nil, "", fmt.Errorf("no JSON in pwdet output")
	}
	raw := []byte(out[i+1:])
	var cmp api.ScanComparison
	if err := json.Unmarshal(raw, &cmp); err != nil {
		return nil, "", err
	}
	var extra struct {
		Rerun string `json:"rerun_report"`
	}
	_ = json.Unmarshal(raw, &extra)
	return &cmp, extra.Rerun, nil
}

// FormatScan renders a scan comparison as lines.
func FormatScan(c *api.ScanComparison) []string {
	s := func(p *string) string {
		if p == nil {
			return "-"
		}
		return *p
	}
	yes := func(b bool, ok, bad string) string {
		if b {
			return ok
		}
		return bad
	}
	out := []string{
		fmt.Sprintf("verdict    %s -> %s", s(c.Verdict[0]), s(c.Verdict[1])),
		fmt.Sprintf("level      %s -> %s", s(c.Level[0]), s(c.Level[1])),
		"files      " + yes(c.FilesMatch, "same hashes", "differ: "+strings.Join(c.FileDifferences, "; ")),
		"findings   " + yes(c.DigestMatch, "deterministic findings match the digest", "deterministic findings differ"),
	}
	keys := make([]string, 0, len(c.CheckDifferences))
	for k := range c.CheckDifferences {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d := c.CheckDifferences[k]
		out = append(out, fmt.Sprintf("  %s: %s -> %s", k, s(d[0]), s(d[1])))
	}
	if b := c.Behaviour; len(b["only_in_report"])+len(b["only_in_rerun"]) > 0 {
		out = append(out, fmt.Sprintf("behaviour  only published: %s; only re-run: %s", orNone(strings.Join(b["only_in_report"], ", ")), orNone(strings.Join(b["only_in_rerun"], ", "))))
	} else if len(b["both"]) > 0 {
		out = append(out, "behaviour  same significant probe findings: "+strings.Join(b["both"], ", "))
	}
	if !c.InputsHashesMatch {
		out = append(out, fmt.Sprintf("note       suites, thresholds or calibration differ (pwdet %s -> %s)", s(c.ToolVersion[0]), s(c.ToolVersion[1])))
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
