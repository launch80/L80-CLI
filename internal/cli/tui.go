package cli

import (
	"errors"
	"flag"
	"os"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/tui"
	"github.com/launch80/L80-CLI/internal/tui/term"
	"github.com/launch80/L80-CLI/internal/vast"
)

// runTUI opens the interactive console. Everything it does is also a
// command; it exists to make the tools discoverable and easy to drive.
func runTUI(e env, args []string) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	apiBase := fs.String("api-base", "", "API base URL (overrides L80_API_BASE)")
	onboarding := fs.Bool("onboarding", false, "open on the guided setup (it runs by itself the first time)")
	if err := fs.Parse(args); err != nil {
		return api.ExitUsage
	}
	if !term.IsTerminal(os.Stdout.Fd()) || os.Getenv("TERM") == "dumb" {
		return fail(e, api.Newf("E_USAGE", "Run it in a terminal, or use the commands directly (`L80 help`).",
			"the interactive console needs a terminal"))
	}
	cfg := config.Load(*apiBase)
	err := tui.Run(tui.Deps{
		Version: Version, Config: cfg, Client: newClient(cfg),
		VastEnv: vastEnv, RunsDir: vast.RunsDir(), DashboardState: dashboardState(),
		Usage: usageText, Onboard: *onboarding || config.LoadState().NeedsOnboarding(),
	})
	switch {
	case errors.Is(err, tui.ErrNoTTY), errors.Is(err, term.ErrUnsupported):
		return fail(e, api.Newf("E_USAGE", "Use the commands directly (`L80 help`).", "%v", err))
	case err != nil:
		return fail(e, api.Newf("E_INTERNAL", "Run the same thing as a command, and report this in the launch80 Discord.", "%v", err))
	}
	return api.ExitOK
}
