package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/output"
	"github.com/launch80/L80-CLI/internal/pwdet"
)

// runVerify is the "Verified by Launch80" workflow. A publisher asks for
// review; anyone with a token can see the queue and reproduce a run; only a
// verifier key (publisher_keys.is_verifier) can stamp it, never its own. A
// verifier may stamp on review alone; a reproduction, when there is one,
// goes with the stamp as evidence.
//
//	L80 verify check <file | url | guid>            run the pre-checks locally (results.json or pwdet report.json)
//	L80 verify request <url | guid> [--note text]   ask for review (the publishing key)
//	L80 verify status <url | guid>                  where your request stands
//	L80 verify mine                                 your requests
//	L80 verify queue [--status requested]           other keys' requests, open to any key
//	L80 verify show <id>                            a request, with the figures to reproduce
//	L80 verify reproduce <id> --endpoint <url>      run it yourself and compare (a pwdet scan needs no endpoint)
//	L80 verify approve <id> [--note text]           stamp it verified (verifier keys)
//	L80 verify reject <id> --reason text            decline it (verifier keys)
func runVerify(e env, args []string) int {
	if len(args) == 0 {
		return fail(e, api.Newf("E_USAGE", "Run `L80 verify check <results.json>`, `L80 verify request <url>`, or `L80 verify queue` to review others' runs.",
			"verify needs a subcommand"))
	}
	sub := args[0]
	fs := flag.NewFlagSet("verify "+sub, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	apiBase, asJSON := addCommonFlags(fs)
	note := fs.String("note", "", "request: how to reproduce the run; approve: what you checked")
	skip := fs.Bool("skip-checks", false, "send the request even if a pre-check fails; the results go with it (request only)")
	status := fs.String("status", "requested", "queue/mine: requested, in_review, verified, rejected, or all")
	endpoint := fs.String("endpoint", "", "reproduce: your OpenAI-compatible endpoint serving the same model")
	model := fs.String("model", "", "reproduce: the model name your server answers to (default: the published one); for a pwdet scan, a local folder with the model's files")
	base := fs.String("base", "", "reproduce, pwdet scans: a local folder with the reference's files")
	apiKey := fs.String("api-key", "", "reproduce: bearer key for your endpoint (or L80_BETTERBENCH_API_KEY)")
	out := fs.String("out", "", "reproduce: where BetterBench writes your results.json")
	reason := fs.String("reason", "", "reject: why; the publisher sees it")
	accept := fs.Bool("accept-differences", false, "approve: stamp even though some figures fell outside tolerance (say why in --note)")
	pos, err := parseArgs(fs, args[1:])
	if err != nil {
		return api.ExitUsage
	}
	cfg := config.Load(*apiBase)
	switch sub {
	case "queue", "mine":
		return verifyList(e, cfg, sub, *status, *asJSON)
	case "show", "reproduce", "approve", "reject":
		if len(pos) != 1 {
			return fail(e, api.Newf("E_USAGE", "Pass the request id from `L80 verify queue`.", "verify %s takes one request id", sub))
		}
		if !cfg.HasToken() {
			return fail(e, api.Newf("E_TOKEN_MISSING", "Set L80_TOKEN or ~/.config/L80/credentials.json; see `L80 auth status`.", "reviewing needs a launch80 key"))
		}
		switch sub {
		case "show":
			return verifyShow(e, cfg, pos[0], *asJSON)
		case "reproduce":
			return verifyReproduce(e, cfg, pos[0], *endpoint, *model, *base, *apiKey, *out, *asJSON)
		default:
			return verifyDecide(e, cfg, sub, pos[0], *note, *reason, *accept, *asJSON)
		}
	case "check", "request", "status":
	default:
		return fail(e, api.Newf("E_USAGE", "Use check, request, status, mine, queue, show, reproduce, approve or reject.", "unknown verify subcommand %q", sub))
	}
	if len(pos) != 1 {
		return fail(e, api.Newf("E_USAGE", "Pass one results.json or pwdet report.json, page URL or guid.", "verify %s takes one argument", sub))
	}

	t, apiErr := app.ResolveTarget(pos[0])
	if apiErr != nil {
		return fail(e, apiErr)
	}

	if sub == "status" {
		if t.GUID == "" {
			return fail(e, api.Newf("E_USAGE", "Pass the page URL or guid (see `L80 history`).", "%s has not been published from this machine", pos[0]))
		}
		v, apiErr := newClient(cfg).VerificationStatus(t.GUID)
		if apiErr != nil {
			return failVerify(e, apiErr)
		}
		if *asJSON {
			return emitJSON(e, v)
		}
		printVerification(e, v)
		return api.ExitOK
	}

	t.RunChecks()
	blocked := betterbench.Blocking(t.Checks)
	if sub == "check" || *asJSON && sub == "check" {
		if *asJSON {
			emitJSON(e, map[string]any{"guid": t.GUID, "url": t.URL, "file": t.File, "verifiable": !blocked, "checks": t.Checks})
		} else {
			printChecks(e, t.Checks)
			if blocked {
				output.Successf(e.stdout, "not verifiable yet: fix the failed checks and publish a new run")
			} else {
				output.Successf(e.stdout, "ready to request verification")
				explainClass(e, t.Class, t.Passes)
				explainScan(e, t)
				switch {
				case t.GUID != "":
					output.Detailf(e.stdout, "L80 verify request %s --note \"how to reproduce it\"", t.URL)
				case t.Scan:
					output.Detailf(e.stdout, "publish it first: L80 pwdet publish %s", t.File)
				default:
					output.Detailf(e.stdout, "publish it first: L80 betterbench --results %s", t.File)
				}
			}
		}
		if blocked {
			return api.ExitValidation
		}
		return api.ExitOK
	}

	// request
	if t.GUID == "" {
		return fail(e, api.Newf("E_USAGE", "Publish it first with `L80 betterbench --results <file>` or `L80 pwdet publish <report.json>`, then request verification of the page.",
			"%s has not been published from this machine", pos[0]))
	}
	if blocked && !*skip {
		printChecks(e, t.Checks)
		return fail(e, api.Newf("E_SCHEMA_INVALID", "Fix the failed checks and publish a new run, or pass --skip-checks to ask anyway (the reviewer sees the failures).",
			"this page would not pass verification"))
	}
	if strings.TrimSpace(*note) == "" {
		output.Detailf(e.stderr, "tip: --note \"…\" tells the reviewer how to reproduce the run")
	}
	v, apiErr := newClient(cfg).RequestVerification(t.GUID, t.Request(*note, Version))
	if apiErr != nil {
		return failVerify(e, apiErr)
	}
	if *asJSON {
		return emitJSON(e, v)
	}
	output.Successf(e.stdout, "verification requested for %s", orNone(t.URL))
	explainClass(e, t.Class, t.Passes)
	explainScan(e, t)
	printVerification(e, v)
	return api.ExitOK
}

func failVerify(e env, apiErr *api.Error) int {
	if api.NotSupported(apiErr) {
		return fail(e, api.Newf("E_NOT_SUPPORTED", "Run `L80 verify check` meanwhile; it shows what a reviewer would check. Watch `L80 update` for support.",
			"this launch80 server does not accept verification requests yet"))
	}
	return fail(e, apiErr)
}

func printChecks(e env, checks []betterbench.Check) {
	marks := map[string]string{"pass": "ok  ", "warn": "warn", "fail": "FAIL"}
	for _, c := range checks {
		output.Successf(e.stdout, "%s  %-12s %s", marks[c.Status], c.Name, c.Detail)
	}
}

func byWhom(who string) string {
	if who == "" {
		return ""
	}
	return " by " + who
}

func printVerification(e env, v *api.Verification) {
	output.Detailf(e.stdout, "status %s", v.Status)
	if v.TrustTier != "" {
		output.Detailf(e.stdout, "tier   %s", v.TrustTier)
	}
	if v.RequestedAt != "" {
		output.Detailf(e.stdout, "requested %s%s", v.RequestedAt, byWhom(v.Requester()))
	}
	if v.ReviewedAt != "" {
		output.Detailf(e.stdout, "reviewed  %s%s", v.ReviewedAt, byWhom(v.Reviewer()))
	}
	if v.DecisionNote != "" {
		output.Detailf(e.stdout, "reviewer: %s", v.DecisionNote)
	}
	for _, c := range v.ServerChecks {
		if c.Status != "pass" {
			output.Detailf(e.stdout, "server check %s: %s %s", c.Name, c.Status, c.Detail)
		}
	}
}

// runReview is the older name for the verifier commands: list is
// `verify queue`, approve and reject are the same.
func runReview(e env, args []string) int {
	if len(args) > 0 && args[0] == "list" {
		args = append([]string{"queue"}, args[1:]...)
	}
	if len(args) == 0 || (args[0] != "queue" && args[0] != "approve" && args[0] != "reject") {
		return fail(e, api.Newf("E_USAGE", "Run `L80 verify queue`, `L80 verify reproduce <id>`, then `L80 verify approve <id>` or `L80 verify reject <id> --reason …`.", "review is now part of `L80 verify`"))
	}
	return runVerify(e, args)
}

func verifyList(e env, cfg config.Config, scope, status string, asJSON bool) int {
	if status == "all" {
		status = ""
	}
	if scope == "mine" && status == "requested" {
		status = ""
	}
	items, apiErr := newClient(cfg).Verifications(scope, status)
	if apiErr != nil {
		return failVerify(e, apiErr)
	}
	if asJSON {
		return emitJSON(e, items)
	}
	if len(items) == 0 {
		if scope == "queue" {
			output.Successf(e.stdout, "nothing waiting for review")
		} else {
			output.Successf(e.stdout, "you have not requested verification yet; see `L80 verify request`")
		}
	}
	for _, it := range items {
		output.Successf(e.stdout, "%s  %-10s %-5s %s", it.ID, it.Status, orDash(it.RunClass), orNone(it.URL))
		if it.Title != "" {
			output.Detailf(e.stdout, "%s", it.Title)
		}
		if r := it.Requester(); scope == "queue" && r != "" {
			output.Detailf(e.stdout, "requested by %s %s", r, it.RequestedAt)
		}
		if r := it.Reviewer(); r != "" {
			output.Detailf(e.stdout, "reviewed by %s %s", r, it.ReviewedAt)
		}
		for _, c := range it.Checks() {
			if c.Status != "pass" {
				output.Detailf(e.stdout, "%s %s: %s", c.Status, c.Name, c.Detail)
			}
		}
		if it.Note != "" {
			output.Detailf(e.stdout, "publisher: %s", it.Note)
		}
		if it.DecisionNote != "" {
			output.Detailf(e.stdout, "verifier: %s", it.DecisionNote)
		}
	}
	if scope == "queue" && len(items) > 0 {
		output.Detailf(e.stdout, "next: L80 verify show <id>, then L80 verify reproduce <id> (a benchmark run also needs --endpoint <url>)")
	}
	return api.ExitOK
}

// request is a verification request and the page it asks to verify: a
// BetterBench run (plan) or a pwdet scan (scan).
type request struct {
	v       *api.Verification
	payload []byte
	plan    app.Plan
	scan    *app.ScanPlan
}

// loadRequest fetches a request and the payload it asks to verify.
func loadRequest(cfg config.Config, id string) (*request, *api.Error) {
	client := newClient(cfg)
	v, apiErr := client.Verification(id)
	if apiErr != nil {
		return nil, apiErr
	}
	payload, apiErr := client.ArtifactPayload(v.ArtifactGUID)
	if apiErr != nil {
		return nil, apiErr
	}
	rq := &request{v: v, payload: payload}
	if pwdet.IsScanPayload(payload) {
		sp, err := app.ScanPlanFrom(payload)
		if err != nil {
			return nil, api.Newf("E_INPUT_INVALID", "The page's report could not be read; ask the publisher to publish it again.", "%v", err)
		}
		rq.scan = &sp
		return rq, nil
	}
	plan, err := app.PlanFrom(payload)
	if err != nil {
		return nil, api.Newf("E_INPUT_INVALID", "Only BetterBench runs and pwdet scans can be verified.", "%v", err)
	}
	rq.plan = plan
	return rq, nil
}

func printRequestHead(e env, v *api.Verification) {
	output.Successf(e.stdout, "%s  %s", v.ID, orNone(v.Title))
	output.Detailf(e.stdout, "page    %s", orNone(v.URL))
	if r := v.Requester(); r != "" {
		output.Detailf(e.stdout, "status  %s, requested by %s %s", v.Status, r, v.RequestedAt)
	} else {
		output.Detailf(e.stdout, "status  %s, requested %s", v.Status, v.RequestedAt)
	}
	if r := v.Reviewer(); r != "" {
		output.Detailf(e.stdout, "reviewed by %s %s", r, v.ReviewedAt)
	}
	if v.Note != "" {
		output.Detailf(e.stdout, "publisher: %s", v.Note)
	}
}

func printOpenChecks(e env, v *api.Verification) {
	for _, c := range v.Checks() {
		if c.Status != "pass" {
			output.Detailf(e.stdout, "%s %s: %s", c.Status, c.Name, c.Detail)
		}
	}
}

func verifyShow(e env, cfg config.Config, id string, asJSON bool) int {
	rq, apiErr := loadRequest(cfg, id)
	if apiErr != nil {
		return failVerify(e, apiErr)
	}
	v := rq.v
	if rq.scan != nil {
		return showScan(e, v, rq.scan, asJSON)
	}
	plan := rq.plan
	if asJSON {
		return emitJSON(e, map[string]any{"request": v, "plan": plan})
	}
	printRequestHead(e, v)
	output.Successf(e.stdout, "to reproduce")
	output.Detailf(e.stdout, "model       %s", plan.Model)
	for _, k := range []string{"engine", "hardware"} {
		if plan.Notes[k] != "" {
			output.Detailf(e.stdout, "%-11s %s", k, plan.Notes[k])
		}
	}
	output.Detailf(e.stdout, "betterbench %s, %d passes per category, %d warmup, phases %s", plan.Version, plan.RunsPerCategory, plan.Warmup, strings.Join(plan.Phases, ", "))
	output.Detailf(e.stdout, "class       %s", plan.Class)
	if plan.Class == betterbench.ClassQuick {
		output.Detailf(e.stdout, "a quick run is stamped only after a reproduction within quick tolerances, on medians")
	}
	output.Successf(e.stdout, "published figures")
	for _, m := range plan.Headline {
		output.Detailf(e.stdout, "%-22s %s %s", m.Label, m.Value, m.Unit)
	}
	printOpenChecks(e, v)
	if v.Mine {
		output.Detailf(e.stdout, "this is your own request: another key has to verify it")
	} else {
		output.Detailf(e.stdout, "next: serve %s on your hardware, then L80 verify reproduce %s --endpoint <url>", plan.Model, v.ID)
	}
	return api.ExitOK
}

func showScan(e env, v *api.Verification, sp *app.ScanPlan, asJSON bool) int {
	if asJSON {
		return emitJSON(e, map[string]any{"request": v, "scan": sp})
	}
	printRequestHead(e, v)
	output.Successf(e.stdout, "to re-scan")
	output.Detailf(e.stdout, "model       %s", orNone(sp.Subject))
	for _, r := range sp.References {
		output.Detailf(e.stdout, "reference   %s", r)
	}
	prof := orNone(sp.Profile)
	if sp.Level != "" && sp.Level != sp.Profile {
		prof += " (" + sp.Level + " level)"
	}
	output.Detailf(e.stdout, "profile     %s, pwdet %s", prof, sp.ToolVersion)
	output.Detailf(e.stdout, "published   %s, digest %s", orNone(sp.Verdict), shortHash(sp.Digest))
	output.Detailf(e.stdout, "a scan is stamped only after a re-run within tolerance: same file hashes, same deterministic findings, same significant probe findings")
	for _, c := range sp.Checks {
		if c.Status != "pass" {
			output.Detailf(e.stdout, "%s %s: %s", c.Status, c.Name, c.Detail)
		}
	}
	printOpenChecks(e, v)
	if v.Mine {
		output.Detailf(e.stdout, "this is your own request: another key has to verify it")
	} else {
		output.Detailf(e.stdout, "next: L80 verify reproduce %s   (pwdet downloads the pinned files%s)", v.ID, map[bool]string{true: " and serves both models on this machine's GPU", false: ""}[sp.Serve])
	}
	return api.ExitOK
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return orNone(h)
}

func verifyReproduce(e env, cfg config.Config, id, endpoint, model, base, apiKey, out string, asJSON bool) int {
	rq, apiErr := loadRequest(cfg, id)
	if apiErr != nil {
		return failVerify(e, apiErr)
	}
	v := rq.v
	var rep *api.Reproduction
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	if rq.scan != nil {
		rep, apiErr = app.ReproduceScan(ctx, v.ID, rq.payload, app.ScanOptions{Model: model, Base: base, Dir: out}, Version, app.WriterSink{W: e.stderr})
	} else {
		if endpoint == "" {
			stop()
			return fail(e, api.Newf("E_USAGE", "Pass --endpoint http://host:port/v1 for a server running the same model on your hardware.", "reproduce needs your endpoint"))
		}
		plan := rq.plan
		if apiKey == "" {
			apiKey = os.Getenv("L80_BETTERBENCH_API_KEY")
		}
		if out == "" {
			out = fmt.Sprintf("reproduce-%s-%s.json", id, time.Now().Format("20060102-1504"))
		}
		output.Detailf(e.stderr, "reproducing %s: %s, %d passes, phases %s", orNone(v.Title), plan.Model, plan.RunsPerCategory, strings.Join(plan.Phases, ", "))
		rep, apiErr = app.Reproduce(ctx, rq.payload, plan.Options(endpoint, model, apiKey, out), Version, app.WriterSink{W: e.stderr})
	}
	stop()
	if apiErr != nil {
		return fail(e, apiErr)
	}
	if err := app.SaveReproduction(v.ID, rep); err != nil {
		output.Detailf(e.stderr, "warning: could not save the reproduction: %v", err)
	}
	if asJSON {
		emitJSON(e, rep)
	} else {
		lines := app.FormatDeltas(rep.Metrics)
		if rep.Scan != nil {
			lines = app.FormatScan(rep.Scan)
		}
		for _, l := range lines {
			output.Detailf(e.stdout, "%s", l)
		}
		switch {
		case rep.WithinTolerance && rep.Scan != nil:
			output.Successf(e.stdout, "your re-scan matches the published report")
		case rep.WithinTolerance:
			output.Successf(e.stdout, "your run matches the published figures")
		case rep.Scan != nil:
			output.Successf(e.stdout, "your re-scan differs from the published report")
		default:
			output.Successf(e.stdout, "some figures differ by more than the tolerance")
		}
		if v.Mine {
			output.Detailf(e.stdout, "this is your own request: another key has to verify it")
		} else {
			output.Detailf(e.stdout, "next (verifier keys): L80 verify approve %s --note \"…\"  or  L80 verify reject %s --reason \"…\"", v.ID, v.ID)
		}
	}
	if !rep.WithinTolerance {
		return api.ExitValidation
	}
	return api.ExitOK
}

func verifyDecide(e env, cfg config.Config, sub, id, note, reason string, accept, asJSON bool) int {
	if sub == "reject" && strings.TrimSpace(reason) == "" {
		return fail(e, api.Newf("E_USAGE", "Say why with --reason \"…\"; the publisher sees it.", "a rejection needs a reason"))
	}
	client := newClient(cfg)
	if me, apiErr := client.Me(); apiErr == nil && !me.IsVerifier {
		return fail(e, api.Newf("E_NOT_VERIFIER", "Anyone can reproduce a run; stamping it needs a verifier key. Ask Launch80 to make your key a verifier.",
			"key %s is not a verifier", orNone(me.KeyID)))
	}
	v, apiErr := client.Verification(id)
	if apiErr != nil {
		return failVerify(e, apiErr)
	}
	if v.Mine {
		return fail(e, api.Newf("E_OWN_REQUEST", "Another verifier has to review your own run.", "request %s was made with this key", id))
	}
	d := api.Decision{Decision: "rejected", Note: reason}
	if sub == "approve" {
		class := runClass(client, v)
		quick := class == betterbench.ClassQuick
		rep, err := app.LoadReproduction(id)
		if class == classScan {
			// A scan is stamped only against a re-run within tolerance.
			if err != nil || rep.Scan == nil {
				return fail(e, api.Newf("E_REPRODUCTION_REQUIRED", fmt.Sprintf("Run `L80 verify reproduce %s` first; a scan is stamped only against a re-run.", id),
					"%s is a pwdet scan and there is no re-scan of it on this machine", id))
			}
			if !rep.WithinTolerance {
				for _, l := range app.FormatScan(rep.Scan) {
					output.Detailf(e.stderr, "%s", l)
				}
				return fail(e, api.Newf("E_OUTSIDE_TOLERANCE", "Reject it, or re-scan; a scan cannot be stamped with --accept-differences.",
					"your re-scan differs from the published report"))
			}
		}
		if err != nil {
			rep = nil
			if quick {
				return fail(e, api.Newf("E_REPRODUCTION_REQUIRED", fmt.Sprintf("Run `L80 verify reproduce %s --endpoint <url>` first; a quick run is stamped only against a reproduction.", id),
					"%s is a quick run and there is no reproduction of it on this machine", id))
			}
			output.Detailf(e.stderr, "stamping on review alone: no reproduction of %s on this machine (L80 verify reproduce %s --endpoint <url> adds one)", id, id)
		}
		if rep != nil && !rep.WithinTolerance && quick {
			for _, l := range app.FormatDeltas(rep.Metrics) {
				output.Detailf(e.stderr, "%s", l)
			}
			return fail(e, api.Newf("E_OUTSIDE_TOLERANCE", "Reject it, or reproduce again; a quick run cannot be stamped with --accept-differences.",
				"your reproduction of this quick run differs from the published figures"))
		}
		if rep != nil && !rep.WithinTolerance && !accept {
			for _, l := range app.FormatDeltas(rep.Metrics) {
				output.Detailf(e.stderr, "%s", l)
			}
			return fail(e, api.Newf("E_OUTSIDE_TOLERANCE", "Reject it, reproduce again, or pass --accept-differences with a --note saying why the difference is acceptable.",
				"your reproduction differs from the published figures"))
		}
		d = api.Decision{Decision: "verified", Note: note, Reproduction: rep}
	}
	out, apiErr := client.Decide(id, d)
	if apiErr != nil {
		return failVerify(e, apiErr)
	}
	if asJSON {
		return emitJSON(e, out)
	}
	output.Successf(e.stdout, "%s: %s", d.Decision, orNone(out.URL))
	return api.ExitOK
}

// explainScan says what a scan's stamp needs.
func explainScan(e env, t *app.Target) {
	if t.Scan && t.Level != "" {
		lvl := strings.ToUpper(t.Level[:1]) + t.Level[1:]
		output.Detailf(e.stdout, "%s scan: a verifier re-runs it with `pwdet reproduce`; the badge reads \"Verified by Launch80 · %s scan reproduced\"", t.Level, lvl)
	}
}

// explainClass says what kind of stamp a run can get.
func explainClass(e env, class string, passes int) {
	if class == betterbench.ClassQuick {
		output.Detailf(e.stdout, "quick run (%d passes): a verifier must reproduce it before stamping; the badge reads \"Verified by Launch80 · quick run (%d passes)\" and compares medians only", passes, passes)
	}
}

// classScan is the class of a scan.pwdet.v1 request.
const classScan = "scan"

// runClass is the request's class: the server's, or derived from the page
// when an older server leaves it out. "" when neither is available.
func runClass(client *api.Client, v *api.Verification) string {
	if v.RunClass != "" {
		return v.RunClass
	}
	payload, apiErr := client.ArtifactPayload(v.ArtifactGUID)
	if apiErr != nil {
		return ""
	}
	if pwdet.IsScanPayload(payload) {
		return classScan
	}
	plan, err := app.PlanFrom(payload)
	if err != nil {
		return ""
	}
	return plan.Class
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
