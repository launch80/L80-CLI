package cli

import (
	"flag"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/output"
)

func runAuth(e env, args []string) int {
	if len(args) == 0 || args[0] != "status" {
		return fail(e, api.Newf("E_USAGE", "Run `L80 auth status`.", "unknown auth subcommand"))
	}

	fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	apiBase, asJSON := addCommonFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return api.ExitUsage
	}

	cfg := config.Load(*apiBase)

	// Note what is reported: the endpoint, the key id, where the credential came
	// from, and a masked fragment. Never the token itself -- this output may be
	// read back by a model or pasted into a bug report.
	if *asJSON {
		return emitJSON(e, map[string]any{
			"api_base":     cfg.BaseURL,
			"key_id":       cfg.KeyID,
			"token_source": string(cfg.TokenSource),
			"token_masked": output.Mask(cfg.Token),
			"has_token":    cfg.HasToken(),
		})
	}

	output.Successf(e.stdout, "api_base     %s", cfg.BaseURL)
	output.Successf(e.stdout, "key_id       %s", orNone(cfg.KeyID))
	output.Successf(e.stdout, "token        %s (masked)", output.Mask(cfg.Token))
	output.Successf(e.stdout, "source       %s", cfg.TokenSource)
	output.Successf(e.stdout, "credentials  %s", cfg.CredPath)

	if !cfg.HasToken() {
		output.Successf(e.stdout, "")
		output.Successf(e.stdout, "No token configured. Set L80_TOKEN or write %s", cfg.CredPath)
		return api.ExitAuth
	}
	return api.ExitOK
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
