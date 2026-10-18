package cli

import (
	"flag"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/output"
)

func runTemplates(e env, args []string) int {
	if len(args) == 0 || args[0] != "list" {
		return fail(e, api.Newf("E_USAGE", "Run `L80 templates list`.", "unknown templates subcommand"))
	}

	fs := flag.NewFlagSet("templates list", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	apiBase, asJSON := addCommonFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return api.ExitUsage
	}

	cfg := config.Load(*apiBase)
	templates, apiErr := newClient(cfg).Templates()
	if apiErr != nil {
		return fail(e, apiErr)
	}

	if *asJSON {
		return emitJSON(e, templates)
	}
	for _, t := range templates {
		output.Successf(e.stdout, "%-20s v%-3d %s", t.ID, t.Version, t.Label)
	}
	return api.ExitOK
}
