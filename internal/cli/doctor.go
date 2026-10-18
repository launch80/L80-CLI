package cli

import (
	"flag"
	"strings"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/output"
)

func runDoctor(e env, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	apiBase, asJSON := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return api.ExitUsage
	}

	cfg := config.Load(*apiBase)
	health, apiErr := newClient(cfg).Health()

	reachable := apiErr == nil
	templates := []string{}
	if reachable {
		templates = health.Templates
	}

	tools, _ := app.ToolsCheck(nil, false)

	if *asJSON {
		return emitJSON(e, map[string]any{
			"tools":     tools,
			"uvx":       app.HasUvx(),
			"version":   Version,
			"api_base":  cfg.BaseURL,
			"reachable": reachable,
			"templates": templates,
			"has_token": cfg.HasToken(),
			"key_id":    cfg.KeyID,
		})
	}

	output.Successf(e.stdout, "%s", VersionLine())
	output.Successf(e.stdout, "api_base     %s", cfg.BaseURL)
	if reachable {
		output.Successf(e.stdout, "endpoint     reachable")
		output.Successf(e.stdout, "templates    %s", strings.Join(templates, ", "))
	} else {
		output.Successf(e.stdout, "endpoint     UNREACHABLE (%s)", apiErr.Message)
	}
	output.Successf(e.stdout, "token        %s (masked), from %s",
		output.Mask(cfg.Token), cfg.TokenSource)
	for _, t := range tools {
		line := t.Status
		if t.Source != "" {
			line += " via " + t.Source
		}
		output.Successf(e.stdout, "%-12s %s", t.Name, line)
	}
	output.Detailf(e.stdout, "`L80 tools check` probes versions and explains anything missing")

	if !reachable {
		return api.ExitCodeFor(apiErr.Code)
	}
	if !cfg.HasToken() {
		return api.ExitAuth
	}
	return api.ExitOK
}
