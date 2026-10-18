// Package cli implements the L80 command surface.
//
// Dispatch is a switch plus a flag.FlagSet per subcommand rather than a CLI
// framework: this binary holds a credential, so every dependency it does not
// have is a supply-chain surface it cannot inherit.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/tui/term"
)

type env struct {
	stdout io.Writer
	stderr io.Writer
}

// Run dispatches argv and returns a process exit code.
func Run(args []string) int {
	e := env{stdout: os.Stdout, stderr: os.Stderr}

	if len(args) < 2 {
		usage(e.stdout)
		if f, ok := e.stdout.(*os.File); ok && term.IsTerminal(f.Fd()) {
			fmt.Fprintln(e.stdout, "Run `L80 tui` for the interactive console.")
		}
		return api.ExitUsage
	}

	switch args[1] {
	case "publish":
		return runPublish(e, args[2:])
	case "betterbench":
		return runBetterbench(e, args[2:])
	case "templates":
		return runTemplates(e, args[2:])
	case "skills":
		return runSkills(e, args[2:])
	case "auth":
		return runAuth(e, args[2:])
	case "doctor":
		return runDoctor(e, args[2:])
	case "update":
		return runUpdate(e, args[2:])
	case "history":
		return runHistory(e, args[2:])
	case "tools":
		return runTools(e, args[2:])
	case "vast":
		return runVast(e, args[2:])
	case "pwdet":
		return runPwdet(e, args[2:])
	case "dashboard":
		return runDashboard(e, args[2:])
	case "verify":
		return runVerify(e, args[2:])
	case "review":
		return runReview(e, args[2:])
	case "tui", "console":
		return runTUI(e, args[2:])
	case "version", "--version", "-v":
		fmt.Fprintln(e.stdout, VersionLine())
		return api.ExitOK
	case "help", "--help", "-h":
		usage(e.stdout)
		return api.ExitOK
	default:
		fmt.Fprintf(e.stderr, "error: E_USAGE\n  unknown command %q\n", args[1])
		fmt.Fprintln(e.stderr, "remedy: Run `L80 help` to see available commands.")
		return api.ExitUsage
	}
}

// fail prints a structured error and returns its exit code.
func fail(e env, err *api.Error) int {
	err.Print(e.stderr)
	return api.ExitCodeFor(err.Code)
}

// newClient builds an API client from resolved config.
func newClient(cfg config.Config) *api.Client {
	return api.NewClient(cfg.BaseURL, cfg.Token, Version)
}

// addCommonFlags registers flags every network-facing subcommand accepts.
func addCommonFlags(fs *flag.FlagSet) (apiBase *string, asJSON *bool) {
	apiBase = fs.String("api-base", "", "API base URL (overrides L80_API_BASE)")
	asJSON = fs.Bool("json", false, "emit machine-readable JSON")
	return
}

func usage(w io.Writer) {
	fmt.Fprint(w, `L80 — benchmark, inspect and publish to launch80

Usage:
  Benchmark and publish (publishing needs a token):
  L80 betterbench --endpoint <url> --model <m>   Run BetterBench and publish the results
                  [--quick] [--note k=v] ...     file as written (see --help)
  L80 betterbench --results <results.json>       Publish an existing results.json
  L80 publish <file.json> [--template <id>]      Publish a payload, print the URL
                          [--json] [--dry-run]
  L80 history [--limit N] [--json] [--local]     List the share URLs this key has published
  L80 templates list [--json]                    List available templates
  L80 verify check <results.json|url>            Run the checks a "verified by Launch80" review applies
  L80 verify request <url> [--note text]         Ask launch80 to verify a published BetterBench page
  L80 verify status <url>                        Where a verification request stands

  Tools (free, no token):
  L80 vast <command> [args]                      Rent a Vast.ai GPU, serve, benchmark, publish (bbvastl80)
  L80 vast run --spec run.yaml [--max-hours N]   The whole pipeline, with an optional cost watchdog
  L80 pwdet scan <model> [--json] [--publish]    Check weights for tampering (Poisoned Weights Detector)
  L80 dashboard [tui|serve]                      GPU and inference telemetry for this machine
  L80 tools list [--json]                        Show the tools and the versions this release pins
  L80 tools check [name...] [--json]             Show how each tool resolves on this machine
  L80 tools prefetch [name...]                   Download and build pinned tools ahead of time

  Interactive:
  L80 tui                                        A full-screen console for everything above

  Setup and maintenance:
  L80 skills print [name]                        Print a bundled SKILL.md
  L80 skills link --target claude-code [--dev]   Install a skill for an agent
  L80 auth status                                Show the resolved endpoint and key
  L80 doctor                                     Check connectivity, config and tools
  L80 update [--check] [--json]                  Install the latest release in place
  L80 version                                    Print the version

Environment:
  L80_API_BASE             API base URL (default https://launch80.com)
  L80_TOKEN                Publish token; otherwise ~/.config/L80/credentials.json
  L80_BETTERBENCH          Command to run instead of the pinned BetterBench
  L80_BETTERBENCH_API_KEY  Bearer key for the endpoint under test (never recorded)
  L80_VAST, L80_PWDET, L80_DASHBOARD   Commands to run instead of the pinned tools
  VAST_API_KEY             Vast.ai key for L80 vast (or ~/.config/bbvastl80/credentials)

`)
}

// parseArgs parses flags that may appear before OR after positional arguments.
//
// Go's flag package stops at the first non-flag argument, so `L80 publish
// report.json --dry-run` would otherwise silently treat --dry-run as a second
// file. A model writing the command naturally puts the file first, so this
// permutation loop is what makes the documented usage actually work.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args

	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
}
