package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/output"
	"github.com/launch80/L80-CLI/internal/pwdet"
	"github.com/launch80/L80-CLI/internal/pytool"
)

// pwdet's own `scan` exit statuses, passed through unchanged.
const (
	pwdetClean    = 0 // clean or inconclusive (also any success)
	pwdetSuspect  = 1 // suspicious, or pwdet itself failed
	pwdetTampered = 2 // likely_tampered or malicious
)

// runPwdet fronts the Poisoned Weights Detector. Every verb is passed
// through; `scan` additionally takes L80's --json, --publish, --dry-run,
// --payload-out, --title and --summary, which read the report pwdet wrote
// and, for --publish, put it on launch80 with the machine's name, paths and
// endpoints removed: as scan.pwdet.v1 (verifiable) when the server offers
// it, else generic.report.v1. `publish` does the same
// for a report from an earlier scan, without running pwdet. Scanning is
// free; publishing needs a token like every publish.
func runPwdet(e env, args []string) int {
	if len(args) > 0 && args[0] == "publish" {
		return pwdetPublish(e, args[1:])
	}
	r, apiErr := resolveTool("pwdet")
	if apiErr != nil {
		return fail(e, apiErr)
	}
	if wantsHelp(args) {
		toolHeader(e, r,
			"L80 pwdet — Poisoned Weights Detector: check a checkpoint or endpoint for tampering",
			"",
			"  L80 pwdet <pwdet command> [args...]                   every pwdet command, passed through",
			"  L80 pwdet scan <model> [pwdet flags] --json           verdict and report path as JSON",
			"  L80 pwdet scan <model> [pwdet flags] --publish        publish the report to launch80 (needs a token)",
			"                         [--dry-run] [--payload-out p.json] [--title t] [--summary s]",
			"  L80 pwdet publish <report.json|report dir> [--dry-run] [--payload-out p.json] [--json]",
			"                                                        publish a report from an earlier scan",
			"",
			"  <model> is a local directory, an HF repo id (org/name[@rev]), or http://host/v1#model.",
			"  --profile basic|standard|thorough|deep: what the scan can see. basic cannot see weight implants;",
			"  standard compares weights with --base; thorough also probes behaviour, serving both on this GPU.",
			"  scan exits 0 clean or inconclusive, 1 suspicious (or pwdet failed), 2 likely tampered or malicious.",
			"  The published page never carries the hostname, local paths or private endpoints.")
		res, err := runChild(e, r, []string{"--help"}, nil, nil)
		return exitFromChild(e, "pwdet", res, err)
	}
	if args[0] != "scan" {
		res, err := runChild(e, r, args, nil, nil)
		return exitFromChild(e, "pwdet", res, err)
	}
	mine, rest, err := splitOwned(args[1:], ownedFlags{
		"json": false, "publish": false, "dry-run": false, "payload-out": true, "title": true, "summary": true, "api-base": true,
	})
	if err != nil {
		return fail(e, api.Newf("E_USAGE", "Run `L80 pwdet --help`.", "%v", err))
	}
	curated := len(mine) > 0
	if !curated {
		res, err := runChild(e, r, args, nil, nil)
		return exitFromChild(e, "pwdet", res, err)
	}
	return pwdetScan(e, r, rest, mine)
}

func pwdetScan(e env, r pytool.Runner, rest []string, mine map[string]string) int {
	argv := append([]string{"scan"}, rest...)
	if !contains(rest, "--json-only") {
		argv = append(argv, "--json-only")
	}
	var captured bytes.Buffer
	res, err := runChild(env{stdout: &captured, stderr: e.stderr}, r, argv, nil, nil)
	if err != nil {
		return exitFromChild(e, "pwdet", res, err)
	}
	line := lastJSONLine(captured.String())
	var summary struct {
		Verdict *string `json:"verdict"`
		Report  string  `json:"report"`
	}
	if line == "" || json.Unmarshal([]byte(line), &summary) != nil || summary.Report == "" {
		io.Copy(e.stderr, &captured)
		return fail(e, api.Newf("E_INPUT_INVALID", "Fix what pwdet reported above and retry.",
			"pwdet scan exited %d without reporting a report path", res.code))
	}
	verdict := ""
	if summary.Verdict != nil {
		verdict = *summary.Verdict
	}
	result := map[string]any{"verdict": summary.Verdict, "report": summary.Report, "exit_status": res.code}

	publish, dry := isTrue(mine["publish"]), isTrue(mine["dry-run"])
	if publish || dry || mine["payload-out"] != "" {
		if code, ok := pwdetPage(e, summary.Report, publish && !dry, mine, result); !ok {
			return code
		}
	}

	if isTrue(mine["json"]) {
		emitJSON(e, result)
		return res.code
	}
	output.Successf(e.stdout, "verdict: %s", orNone(verdict))
	output.Detailf(e.stdout, "report %s", summary.Report)
	if u, ok := result["url"].(string); ok {
		output.Successf(e.stdout, "published: %s", u)
	} else if dry {
		output.Detailf(e.stdout, "dry run: would publish %d bytes as %s", result["byte_size"], result["template"])
	}
	return res.code
}

// pwdetPublish publishes a report pwdet already wrote: a report.json, the
// folder holding it, or a reports folder (its newest scan).
func pwdetPublish(e env, args []string) int {
	if wantsHelp(args) {
		fmt.Fprint(e.stdout, `L80 pwdet publish — publish a report from an earlier scan as a launch80 page

  L80 pwdet publish <report.json|report dir> [--dry-run] [--payload-out p.json]
                    [--title t] [--summary s] [--json] [--api-base URL]

  A folder may be one scan's folder or a whole reports folder; then the newest
  scan in it is published. pwdet itself is not run. The hostname, local
  paths and private endpoints are removed. When the launch80 server offers
  scan.pwdet.v1 the page carries the scrubbed report and can be verified
  (L80 verify request); otherwise it is a generic.report.v1. Publishing needs
  a token, --dry-run does not.
`)
		return api.ExitOK
	}
	mine, rest, err := splitOwned(args, ownedFlags{
		"json": false, "dry-run": false, "payload-out": true, "title": true, "summary": true, "api-base": true,
	})
	if err != nil || len(rest) != 1 {
		return fail(e, api.Newf("E_USAGE", "Run `L80 pwdet publish --help`.", "pass one report.json or report folder"))
	}
	path, perr := findReport(rest[0])
	if perr != nil {
		return fail(e, api.Newf("E_INPUT_INVALID", "Pass the report.json `L80 pwdet scan` wrote, or its folder (default ./reports).", "%v", perr))
	}
	result := map[string]any{"report": path}
	dry := isTrue(mine["dry-run"])
	if code, ok := pwdetPage(e, path, !dry, mine, result); !ok {
		return code
	}
	if isTrue(mine["json"]) {
		return emitJSON(e, result)
	}
	output.Successf(e.stdout, "verdict: %s", orNone(fmt.Sprint(result["verdict"])))
	output.Detailf(e.stdout, "report %s", path)
	if u, ok := result["url"].(string); ok {
		output.Successf(e.stdout, "published: %s", u)
	} else {
		output.Detailf(e.stdout, "dry run: would publish %d bytes as %s", result["byte_size"], result["template"])
	}
	return api.ExitOK
}

// findReport resolves a report file, a scan folder, or a reports folder
// (newest scan first, by name: pwdet names them by start time).
func findReport(p string) (string, error) {
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return p, nil
	}
	if f := filepath.Join(p, "report.json"); exists(f) {
		return f, nil
	}
	dirs, _ := os.ReadDir(p)
	for i := len(dirs) - 1; i >= 0; i-- {
		if f := filepath.Join(p, dirs[i].Name(), "report.json"); dirs[i].IsDir() && exists(f) {
			return f, nil
		}
	}
	return "", fmt.Errorf("no report.json in %s", p)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// pwdetPage builds the page from a report, writes --payload-out, and
// publishes when send is set, filling result. ok is false after a failure,
// with the exit code to return.
func pwdetPage(e env, reportPath string, send bool, mine map[string]string, result map[string]any) (int, bool) {
	data, rerr := os.ReadFile(reportPath)
	if rerr != nil {
		return fail(e, api.Newf("E_INPUT_INVALID", "Check the report path pwdet printed.", "could not read %s: %v", reportPath, rerr)), false
	}
	if _, perr := pwdet.Parse(data); perr != nil {
		return fail(e, api.Newf("E_INPUT_INVALID", "Pass a pwdet report.json; if it is one, update L80 (`L80 update`), this pwdet may be newer than it.", "%s: %v", reportPath, perr)), false
	}
	scan := app.ServerHasTemplate(newClient(config.Load(mine["api-base"])), pwdet.ScanTemplateID)
	pg, berr := app.BuildPwdetPage(data, pwdet.Options{Title: mine["title"], Summary: mine["summary"]}, scan)
	if berr != nil {
		return fail(e, api.Newf("E_INPUT_INVALID", "Shorten --title/--summary, or report this with the report attached.", "%v", berr)), false
	}
	rep, body := pg.Report, pg.Body
	if _, ok := result["verdict"]; !ok && rep.Verdict != nil {
		result["verdict"] = rep.Verdict.Label
	}
	if pg.Why != "" {
		output.Detailf(e.stderr, "note: %s", pg.Why)
	}
	result["template"], result["byte_size"] = pg.Template, len(body)
	if l := rep.Level(); l != "" {
		result["level"] = l
	}
	if po := mine["payload-out"]; po != "" {
		if werr := os.WriteFile(po, append(body, '\n'), 0o644); werr != nil {
			return fail(e, api.Newf("E_INPUT_INVALID", "Pass a writable path.", "could not write %s: %v", po, werr)), false
		}
		output.Detailf(e.stderr, "payload written to %s", po)
	}
	if send {
		artifact, code := publishArtifact(e, body, pg.Template, reportPath, mine["api-base"])
		if artifact == nil {
			return code, false
		}
		result["url"], result["trust_tier"] = artifact.URL, artifact.TrustTier
	}
	return api.ExitOK, true
}

func contains(list []string, s string) bool { return indexOf(list, s) >= 0 }

// lastJSONLine is the last line of out that looks like a JSON object.
func lastJSONLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "{") {
			return l
		}
	}
	return ""
}
