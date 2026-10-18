package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/history"
	"github.com/launch80/L80-CLI/internal/output"
)

// harnessDumpKeys are top-level fields of a BetterBench results.json. None of
// them exists in any template, so their presence, together with the absence of
// the fields every template requires, means the caller pointed `L80 publish` at
// the harness output instead of at a payload mapped from it. That file is
// usually far over the size cap, and "shorten the sections" is the wrong advice
// for a file that has no sections.
var harnessDumpKeys = []string{"betterbench_version", "env", "sample_gate", "results", "config"}

// templateRequiredKeys are required by every template; a mapped payload has them.
var templateRequiredKeys = []string{"title", "summary"}

func declaresRawTemplate(fields map[string]json.RawMessage) bool {
	var s string
	if raw, ok := fields["$template"]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s == betterbench.RawTemplateID
}

func looksLikeHarnessDump(fields map[string]json.RawMessage) bool {
	for _, k := range templateRequiredKeys {
		if _, ok := fields[k]; ok {
			return false
		}
	}
	for _, k := range harnessDumpKeys {
		if _, ok := fields[k]; ok {
			return true
		}
	}
	return false
}

// compact strips insignificant whitespace so the size limit means the same
// thing however the file was formatted. json.Compact copies value bytes
// verbatim, so numbers and strings come through exactly as written.
func compact(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// resolveTemplate reconciles the --template flag with the file's own $template.
//
// The two can disagree, and silently picking a winner would publish something
// the caller did not ask for, so:
//
//	flag only             -> injected into the payload
//	field only            -> used as-is (the original behaviour)
//	both, and they match  -> fine; the flag is documenting intent
//	both, and they differ -> refuse, naming each source
//	neither               -> refuse, naming both ways to set it
//
// Returns the resolved id and the body to send. The body is the caller's JSON
// with insignificant whitespace removed; values are never rewritten, and the
// object is only re-encoded when an injection was actually required.
func resolveTemplate(data []byte, flagValue, path string) (string, []byte, *api.Error) {
	// json.RawMessage, not `any`: decoding into `any` turns every number into a
	// float64, and re-encoding would then rewrite the caller's values — a large
	// integer comes back in exponent form, and precision past float64 is gone
	// for good. RawMessage hands back each value's original bytes, so injecting
	// a key cannot alter any value the caller wrote.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return "", nil, api.Newf("E_JSON_INVALID",
			"Fix the JSON syntax and retry.",
			"%s is not valid JSON: %v", path, err)
	}

	// bench.betterbench.v1 IS harness output by design; the check below is for
	// the templates that take a mapped payload.
	if flagValue != betterbench.RawTemplateID && !declaresRawTemplate(fields) && looksLikeHarnessDump(fields) {
		return "", nil, api.Newf("E_INPUT_NOT_TEMPLATE",
			"Run `L80 betterbench <results.json>` to map it into a bench.report.v1 payload, then publish "+
				"the file it writes (or add --publish to do both). Never pass results.json itself.",
			"%s looks like raw BetterBench output, not a template payload: it has no title/summary "+
				"but does have harness fields", path)
	}

	fromFile := ""
	if raw, ok := fields["$template"]; ok {
		// A non-string $template stays "" here. The server's schema is what
		// rejects it, and it does so with a proper JSON Pointer.
		_ = json.Unmarshal(raw, &fromFile)
	}

	switch {
	case flagValue == "" && fromFile == "":
		return "", nil, api.Newf("E_TEMPLATE_MISSING",
			"Pass --template <id>, or add a \"$template\" field. Run `L80 templates list` to see valid ids.",
			"%s has no $template field and no --template flag was given", path)

	case flagValue == "":
		body, err := compact(data)
		if err != nil {
			return "", nil, api.Newf("E_INTERNAL", "Retry once.",
				"could not compact %s: %v", path, err)
		}
		return fromFile, body, nil

	case fromFile == "":
		quoted, err := json.Marshal(flagValue)
		if err != nil {
			return "", nil, api.Newf("E_INTERNAL", "Retry once.",
				"could not encode template id: %v", err)
		}
		fields["$template"] = quoted
		merged, err := json.Marshal(fields)
		if err != nil {
			return "", nil, api.Newf("E_INTERNAL", "Retry once.",
				"could not re-encode %s: %v", path, err)
		}
		return flagValue, merged, nil

	case fromFile != flagValue:
		return "", nil, api.Newf("E_TEMPLATE_CONFLICT",
			"Remove one of them, or make them agree, then retry.",
			"--template says %q but %s already declares %q", flagValue, path, fromFile)

	default:
		body, err := compact(data)
		if err != nil {
			return "", nil, api.Newf("E_INTERNAL", "Retry once.",
				"could not compact %s: %v", path, err)
		}
		return flagValue, body, nil
	}
}

func runPublish(e env, args []string) int {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	apiBase, asJSON := addCommonFlags(fs)
	dryRun := fs.Bool("dry-run", false, "validate locally and report, without publishing")
	tmpl := fs.String("template", "", "template id, e.g. bench.report.v1")

	positional, parseErr := parseArgs(fs, args)
	if parseErr != nil {
		return api.ExitUsage
	}
	if len(positional) != 1 {
		return fail(e, api.Newf("E_USAGE",
			"Pass exactly one path, e.g. `L80 publish report.json --template bench.report.v1`.",
			"expected one file argument, got %d", len(positional)))
	}

	path := positional[0]
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return fail(e, api.Newf("E_INPUT_INVALID",
			"Check the path. Ask the user where the report file is if it is not obvious.",
			"could not read %s: %v", path, readErr))
	}

	// Parsing here exists to give a fast, precise error and to reconcile the
	// template id. The server revalidates everything; none of it is a security
	// control.
	template, body, tmplErr := resolveTemplate(data, *tmpl, path)
	if tmplErr != nil {
		return fail(e, tmplErr)
	}

	// A results file published straight through `L80 publish` gets the same
	// privacy treatment `L80 betterbench` applies: the endpoint URL and
	// hostname the harness recorded never leave the machine. The server would
	// refuse them anyway; stripping here means the user never has to.
	if template == betterbench.RawTemplateID {
		stripped, err := betterbench.PrepareRaw(body, betterbench.Prose{})
		if err != nil {
			return fail(e, api.Newf("E_INPUT_INVALID",
				"Pass the results.json written by `betterbench run --out`, or use `L80 betterbench --results <file>`.",
				"%s: %v", path, err))
		}
		body = stripped
	}

	// Checked after resolution: injecting the id makes the body longer, and
	// compaction makes it shorter. Measured on exactly the bytes that are sent.
	limit := api.MaxPayloadBytesFor(template)
	if len(body) > limit {
		return fail(e, api.Newf("E_PAYLOAD_TOO_LARGE",
			"Shorten the sections and retry. A file far over the limit is usually not a template "+
				"payload at all but raw harness output; run `L80 betterbench --results <results.json>` instead.",
			"%s is %d bytes after removing whitespace; the limit for %s is %d", path, len(body), template, limit))
	}

	if *dryRun {
		if *asJSON {
			return emitJSON(e, map[string]any{
				"dry_run": true, "template": template, "byte_size": len(body), "path": path,
			})
		}
		output.Successf(e.stdout, "dry run: %s would publish %d bytes", path, len(body))
		output.Detailf(e.stdout, "template %s (not sent)", template)
		return api.ExitOK
	}

	return publishBody(e, body, template, path, *apiBase, *asJSON)
}

// publishBody sends an already-validated body and reports the result. Shared
// by `publish` and `betterbench` so the two print the same thing.
func publishBody(e env, body []byte, template, path, apiBase string, asJSON bool) int {
	artifact, code := publishArtifact(e, body, template, path, apiBase)
	if artifact == nil {
		return code
	}
	if asJSON {
		return emitJSON(e, artifact)
	}

	// One quotable line the model can hand straight to the user.
	output.Successf(e.stdout, "published: %s", artifact.URL)
	output.Detailf(e.stdout, "template %s v%d · %s · %d bytes",
		artifact.TemplateID, artifact.TemplateVersion, artifact.TrustTier, artifact.ByteSize)
	return api.ExitOK
}

// publishArtifact sends the body and records it locally. On failure it has
// already printed the error and returns a nil artifact and the exit code.
func publishArtifact(e env, body []byte, template, path, apiBase string) (*api.Artifact, int) {
	cfg := config.Load(apiBase)
	res, apiErr := app.Publish(newClient(cfg), app.PublishRequest{
		Body: body, Template: template, Title: titleOfBody(body, template), SourcePath: path, APIBase: cfg.BaseURL,
	})
	if apiErr != nil {
		return nil, fail(e, apiErr)
	}
	// Best effort: a failure to write the local record must not turn a
	// successful publish into an error exit. The URL is still printed.
	if res.LedgerErr != nil {
		output.Detailf(e.stderr, "note: could not record this publish in %s: %v", history.Path(), res.LedgerErr)
	}
	return res.Artifact, api.ExitOK
}

// titleOfBody mirrors the server's titleOf for each template, so the local
// record shows the same title the page carries.
func titleOfBody(body []byte, template string) string {
	var fields struct {
		Title string `json:"title"`
		L80   struct {
			Title string `json:"title"`
		} `json:"l80"`
		Env struct {
			Model string            `json:"model"`
			Notes map[string]string `json:"notes"`
		} `json:"env"`
	}
	if json.Unmarshal(body, &fields) != nil {
		return ""
	}
	if template != betterbench.RawTemplateID {
		return fields.Title
	}
	if fields.L80.Title != "" {
		return fields.L80.Title
	}
	for _, k := range []string{"engine", "server", "image"} {
		if v := fields.Env.Notes[k]; v != "" {
			return fields.Env.Model + " on " + v
		}
	}
	return fields.Env.Model
}

func emitJSON(e env, v any) int {
	enc := json.NewEncoder(e.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(e.stderr, "error: E_INTERNAL")
		return api.ExitInternal
	}
	return api.ExitOK
}
