package cli

import (
	"flag"
	"fmt"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/output"
)

// runSettings is `L80 settings [list|get <key>|set <key> <value>|unset <key>]`.
func runSettings(e env, args []string) int {
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	fs.Usage = func() { settingsUsage(e) }
	pos, err := parseArgs(fs, args)
	if err != nil {
		return api.ExitUsage
	}
	sub := "list"
	if len(pos) > 0 {
		sub, pos = pos[0], pos[1:]
	}
	unknown := func(k string) int {
		return fail(e, api.Newf("E_USAGE", "Run `L80 settings list` to see the settings.", "unknown setting %q", k))
	}
	switch sub {
	case "list":
		type row struct {
			Key     string `json:"key"`
			Value   string `json:"value"`
			Source  string `json:"source"`
			Default string `json:"default"`
			Env     string `json:"env,omitempty"`
			Help    string `json:"help"`
		}
		var rows []row
		for _, k := range config.Known {
			v, src := config.Get(k.Key)
			rows = append(rows, row{k.Key, v, src, k.Default, k.Env, k.Help})
		}
		if *asJSON {
			return emitJSON(e, map[string]any{"settings": rows, "path": config.SettingsPath()})
		}
		for _, r := range rows {
			output.Successf(e.stdout, "%s = %s  (%s)", r.Key, r.Value, r.Source)
			output.Detailf(e.stdout, "%s", r.Help)
		}
		output.Detailf(e.stdout, "file: %s", config.SettingsPath())
		return api.ExitOK
	case "get":
		if len(pos) != 1 {
			return fail(e, api.Newf("E_USAGE", "Run `L80 settings get <key>`.", "get takes one key"))
		}
		if _, ok := config.LookupSetting(pos[0]); !ok {
			return unknown(pos[0])
		}
		v, src := config.Get(pos[0])
		if *asJSON {
			return emitJSON(e, map[string]any{"key": pos[0], "value": v, "source": src})
		}
		fmt.Fprintln(e.stdout, v)
		return api.ExitOK
	case "set", "unset":
		want := 2
		if sub == "unset" {
			want = 1
		}
		if len(pos) != want {
			return fail(e, api.Newf("E_USAGE", "Run `L80 settings set <key> <value>` or `L80 settings unset <key>`.", "%s takes %d argument(s)", sub, want))
		}
		k, ok := config.LookupSetting(pos[0])
		if !ok {
			return unknown(pos[0])
		}
		val := ""
		if sub == "set" {
			val = pos[1]
		}
		if err := config.Set(k.Key, val); err != nil {
			return fail(e, api.Newf("E_USAGE", k.Help, "%s: %v", k.Key, err))
		}
		v, src := config.Get(k.Key)
		if *asJSON {
			return emitJSON(e, map[string]any{"key": k.Key, "value": v, "source": src})
		}
		output.Successf(e.stdout, "%s = %s", k.Key, v)
		if src != "file" && sub == "set" {
			output.Detailf(e.stdout, "warning: %s is in effect (%s); unset it for the file to apply", v, src)
		}
		return api.ExitOK
	}
	return fail(e, api.Newf("E_USAGE", "Run `L80 settings --help`.", "unknown settings command %q", sub))
}

func settingsUsage(e env) {
	fmt.Fprint(e.stderr, `Usage:
  L80 settings [list] [--json]
  L80 settings get <key> [--json]
  L80 settings set <key> <value>
  L80 settings unset <key>

Settings live in ~/.config/L80/settings.json. An environment variable, where a
setting has one, wins over the file.

  qualitybench.confirm   always (default) | over:<duration> | never
                         Ask before a QualityBench run starts, after showing how long
                         it will take. over:30m asks only for longer runs. --yes on
                         L80 qualitybench skips it once; L80_QUALITYBENCH_CONFIRM overrides.
`)
}
