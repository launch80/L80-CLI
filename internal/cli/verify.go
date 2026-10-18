package cli

import (
	"flag"
	"strings"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/output"
)

// runVerify asks launch80 to verify a published BetterBench page: a reviewer
// checks it and, if it holds up, the page's tier becomes "verified".
//
//	L80 verify check <results.json | url | guid>   run the pre-checks locally
//	L80 verify request <url | guid> [--note text]  ask for review (needs the publishing key)
//	L80 verify status <url | guid>                 where a request stands
func runVerify(e env, args []string) int {
	if len(args) == 0 {
		return fail(e, api.Newf("E_USAGE", "Run `L80 verify check <results.json>`, `L80 verify request <url>` or `L80 verify status <url>`.",
			"verify needs a subcommand"))
	}
	sub := args[0]
	fs := flag.NewFlagSet("verify "+sub, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	apiBase, asJSON := addCommonFlags(fs)
	note := fs.String("note", "", "context for the reviewer: how to reproduce, what changed (request only)")
	skip := fs.Bool("skip-checks", false, "send the request even if a pre-check fails; the results go with it (request only)")
	pos, err := parseArgs(fs, args[1:])
	if err != nil {
		return api.ExitUsage
	}
	if len(pos) != 1 {
		return fail(e, api.Newf("E_USAGE", "Pass one results.json, page URL or guid.", "verify %s takes one argument", sub))
	}
	if sub != "check" && sub != "request" && sub != "status" {
		return fail(e, api.Newf("E_USAGE", "Use check, request or status.", "unknown verify subcommand %q", sub))
	}

	t, apiErr := app.ResolveTarget(pos[0])
	if apiErr != nil {
		return fail(e, apiErr)
	}
	cfg := config.Load(*apiBase)

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
				if t.GUID != "" {
					output.Detailf(e.stdout, "L80 verify request %s --note \"how to reproduce it\"", t.URL)
				} else {
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
		return fail(e, api.Newf("E_USAGE", "Publish it first with `L80 betterbench --results <file>`, then request verification of the page.",
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

func printVerification(e env, v *api.Verification) {
	output.Detailf(e.stdout, "status %s", v.Status)
	if v.TrustTier != "" {
		output.Detailf(e.stdout, "tier   %s", v.TrustTier)
	}
	if v.RequestedAt != "" {
		output.Detailf(e.stdout, "requested %s", v.RequestedAt)
	}
	if v.ReviewedAt != "" {
		output.Detailf(e.stdout, "reviewed  %s", v.ReviewedAt)
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

// runReview is for keys with the reviewer role.
//
//	L80 review list [--status requested]
//	L80 review approve <id> [--note text]
//	L80 review reject <id> --reason text
func runReview(e env, args []string) int {
	if len(args) == 0 {
		return fail(e, api.Newf("E_USAGE", "Run `L80 review list`, `L80 review approve <id>` or `L80 review reject <id> --reason …`.", "review needs a subcommand"))
	}
	sub := args[0]
	fs := flag.NewFlagSet("review "+sub, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	apiBase, asJSON := addCommonFlags(fs)
	status := fs.String("status", "requested", "list requests in this state: requested, in_review, verified, rejected, or all")
	note := fs.String("note", "", "note shown with the decision")
	reason := fs.String("reason", "", "why the run was rejected (required for reject)")
	pos, err := parseArgs(fs, args[1:])
	if err != nil {
		return api.ExitUsage
	}
	client := newClient(config.Load(*apiBase))
	switch sub {
	case "list":
		st := *status
		if st == "all" {
			st = ""
		}
		items, apiErr := client.ReviewQueue(st)
		if apiErr != nil {
			return failVerify(e, apiErr)
		}
		if *asJSON {
			return emitJSON(e, items)
		}
		if len(items) == 0 {
			output.Successf(e.stdout, "no requests %s", *status)
		}
		for _, it := range items {
			output.Successf(e.stdout, "%s  %-10s %s", it.ID, it.Status, orNone(it.URL))
			if it.Title != "" {
				output.Detailf(e.stdout, "%s", it.Title)
			}
			for _, c := range append(append([]api.ClientCheck{}, it.ServerChecks...), it.ClientChecks...) {
				if c.Status != "pass" {
					output.Detailf(e.stdout, "%s %s: %s", c.Status, c.Name, c.Detail)
				}
			}
			if it.Note != "" {
				output.Detailf(e.stdout, "publisher: %s", it.Note)
			}
		}
		return api.ExitOK
	case "approve", "reject":
		if len(pos) != 1 {
			return fail(e, api.Newf("E_USAGE", "Pass the request id from `L80 review list`.", "review %s takes one id", sub))
		}
		decision, text := "verified", *note
		if sub == "reject" {
			decision = "rejected"
			if strings.TrimSpace(*reason) == "" {
				return fail(e, api.Newf("E_USAGE", "Say why with --reason \"…\"; the publisher sees it.", "a rejection needs a reason"))
			}
			text = *reason
		}
		v, apiErr := client.ReviewDecision(pos[0], decision, text)
		if apiErr != nil {
			return failVerify(e, apiErr)
		}
		if *asJSON {
			return emitJSON(e, v)
		}
		output.Successf(e.stdout, "%s: %s", decision, orNone(v.URL))
		return api.ExitOK
	default:
		return fail(e, api.Newf("E_USAGE", "Use list, approve or reject.", "unknown review subcommand %q", sub))
	}
}
