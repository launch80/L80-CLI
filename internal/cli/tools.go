package cli

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"strings"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/output"
	"github.com/launch80/L80-CLI/internal/pytool"
)

// runTools reports and prepares the Python tools this L80 release fronts.
func runTools(e env, args []string) int {
	if len(args) == 0 {
		return fail(e, api.Newf("E_USAGE", "Run `L80 tools list`, `L80 tools check`, `L80 tools prefetch` or `L80 tools path <name>`.",
			"tools needs a subcommand"))
	}
	sub, rest := args[0], args[1:]
	if sub == "run" {
		return toolsRun(e, rest)
	}
	fs := flag.NewFlagSet("tools "+sub, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	probe := fs.Bool("probe", true, "ask each resolved tool for its version (check only)")
	names, err := parseArgs(fs, rest)
	if err != nil {
		return api.ExitUsage
	}
	for _, n := range names {
		if _, lerr := pytool.Lookup(n); lerr != nil {
			return fail(e, api.Newf("E_USAGE", "Run `L80 tools list` for the names.", "%v", lerr))
		}
	}

	switch sub {
	case "list":
		return toolsList(e, *asJSON)
	case "check":
		return toolsCheck(e, names, *probe, *asJSON)
	case "prefetch":
		return toolsPrefetch(e, names)
	case "path":
		if len(names) != 1 {
			return fail(e, api.Newf("E_USAGE", "Run `L80 tools path betterbench`.", "tools path takes exactly one tool name"))
		}
		st, cerr := app.ToolsCheck(names, false)
		if cerr != nil {
			return fail(e, api.Newf("E_INTERNAL", "Retry once.", "%v", cerr))
		}
		if st[0].Status == "missing" {
			return fail(e, api.Newf("E_TOOL_UNAVAILABLE", st[0].Remedy, "%s", strings.Join(st[0].Notes, "; ")))
		}
		if *asJSON {
			return emitJSON(e, st[0].Argv)
		}
		for _, a := range st[0].Argv {
			output.Successf(e.stdout, "%s", a)
		}
		return api.ExitOK
	default:
		return fail(e, api.Newf("E_USAGE", "Use list, check, prefetch or path.", "unknown tools subcommand %q", sub))
	}
}

func toolsList(e env, asJSON bool) int {
	tools, err := pytool.Manifest()
	if err != nil {
		return fail(e, api.Newf("E_INTERNAL", "Reinstall L80.", "%v", err))
	}
	if asJSON {
		rows := make([]map[string]any, 0, len(tools))
		for _, n := range pytool.Names() {
			t := tools[n]
			rows = append(rows, map[string]any{
				"name": n, "repo": t.Repo, "ref": t.Ref, "commit": t.Commit, "package": t.Package, "exe": t.Exe,
				"python": t.Python, "extras": t.EffectiveExtras(), "min_version": t.MinVersion,
				"override_env": t.Env, "pinned": t.Pinned(), "notes": t.Notes,
			})
		}
		return emitJSON(e, map[string]any{"l80_version": Version, "tools": rows})
	}
	output.Successf(e.stdout, "Tools pinned in L80 %s (free to use; no token needed):", Version)
	for _, n := range pytool.Names() {
		t := tools[n]
		pin := "not pinned in this release"
		if t.Pinned() {
			pin = t.Ref + " @ " + t.ShortCommit()
		}
		output.Successf(e.stdout, "  %-12s %-28s %s", n, pin, t.Repo)
		output.Detailf(e.stdout, "  %s", t.Notes)
		output.Detailf(e.stdout, "  runs `%s`; override with %s=/path/to/%s", t.Exe, t.Env, t.Exe)
	}
	return api.ExitOK
}

func toolsCheck(e env, names []string, probe, asJSON bool) int {
	rows, err := app.ToolsCheck(names, probe)
	if err != nil {
		return fail(e, api.Newf("E_INTERNAL", "Retry once.", "%v", err))
	}
	code := api.ExitOK
	for _, r := range rows {
		if r.Status == "missing" {
			code = api.ExitToolUnavailable
		}
	}
	if asJSON {
		emitJSON(e, map[string]any{"uvx": app.HasUvx(), "tools": rows})
		return code
	}
	uvx := "found"
	if !app.HasUvx() {
		uvx = "NOT FOUND (install uv from https://docs.astral.sh/uv/ so L80 can bootstrap pinned tools)"
	}
	output.Successf(e.stdout, "uvx          %s", uvx)
	for _, r := range rows {
		line := r.Status
		if r.Source != "" {
			line += " via " + r.Source
		}
		if r.Version != "" {
			line += ", version " + r.Version
		}
		output.Successf(e.stdout, "%-12s %s", r.Name, line)
		for _, n := range r.Notes {
			output.Detailf(e.stdout, "%s", n)
		}
		if r.Remedy != "" {
			output.Detailf(e.stdout, "remedy: %s", r.Remedy)
		}
	}
	return code
}

// toolsPrefetch warms uv's cache for each pinned tool by running its version
// command through the bootstrap, so the first real run does not stall on a
// download. Tools resolved from PATH or an override need nothing.
func toolsPrefetch(e env, names []string) int {
	if len(names) == 0 {
		names = pytool.Names()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code := api.ExitOK
	for _, n := range names {
		t, _ := pytool.Lookup(n)
		r, err := pytool.Resolve(t)
		if err != nil {
			var nr *pytool.ErrNoRunner
			if errors.As(err, &nr) {
				output.Successf(e.stdout, "%-12s skipped: %v", n, err)
			}
			code = api.ExitToolUnavailable
			continue
		}
		if !r.Pinned() {
			output.Successf(e.stdout, "%-12s nothing to fetch (using %s)", n, r.Source)
			continue
		}
		args := t.VersionCmd
		if len(args) == 0 {
			args = []string{"--help"}
		}
		output.Successf(e.stdout, "%-12s fetching %s", n, t.Requirement())
		status, xerr := pytool.Exec(ctx, r, args, pytool.ExecOpts{Stdout: e.stderr, Stderr: e.stderr})
		if xerr != nil || status != 0 {
			output.Detailf(e.stdout, "failed (status %d) %v", status, xerr)
			code = api.ExitToolUnavailable
		}
	}
	return code
}

// toolsRun execs a tool with its arguments untouched and returns its exit
// status: `L80 tools run pwdet checks list`. It is what the shims call.
func toolsRun(e env, args []string) int {
	if len(args) == 0 {
		return fail(e, api.Newf("E_USAGE", "Run `L80 tools run <name> [args...]`, e.g. `L80 tools run betterbench --version`.", "tools run needs a tool name"))
	}
	r, apiErr := resolveTool(args[0])
	if apiErr != nil {
		return fail(e, apiErr)
	}
	res, err := runChild(e, r, args[1:], nil, nil)
	return exitFromChild(e, args[0], res, err)
}
