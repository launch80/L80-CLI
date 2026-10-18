package cli

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
)

// runDashboard fronts the AI command dashboard: a terminal UI (and a local
// web page) for GPU and inference telemetry on this machine.
//
//	L80 dashboard                 the terminal UI (= `dashboard tui`)
//	L80 dashboard tui [flags]     attaches to a running server, else samples itself
//	L80 dashboard serve [--port]  the sampler + web page on 127.0.0.1
//
// One sampler per machine: two would double-count energy and gateway calls,
// so `serve` refuses when one already answers on the port (--force skips
// the check).
func runDashboard(e env, args []string) int {
	r, apiErr := resolveTool("dashboard")
	if apiErr != nil {
		return fail(e, apiErr)
	}
	if len(args) == 0 {
		args = []string{"tui"}
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		toolHeader(e, r,
			"L80 dashboard — terminal and web dashboard for local GPU and inference telemetry",
			"",
			"  L80 dashboard                 the terminal UI (attaches to a running server, else samples itself)",
			"  L80 dashboard tui [flags]     flags as for the dashboard's own tui command",
			"  L80 dashboard serve [--port 8787] [--force]   sampler and web page on 127.0.0.1",
			"",
			"  Run one sampler per machine. Read-only: it never restarts or signals anything.",
			"  State is kept in "+dashboardState()+" unless AI_DASHBOARD_STATE is set.")
		res, err := runChild(e, r, []string{"--help"}, nil, nil)
		return exitFromChild(e, "dashboard", res, err)
	}

	verb, rest := args[0], args[1:]
	var mine map[string]string
	if verb == "serve" {
		var err error
		mine, rest, err = splitOwned(rest, ownedFlags{"force": false})
		if err != nil {
			return fail(e, api.Newf("E_USAGE", "Run `L80 dashboard --help`.", "%v", err))
		}
		port := portFrom(rest, "8787")
		if !isTrue(mine["force"]) && samplerAnswers(port) {
			return fail(e, api.Newf("E_USAGE",
				"Run `L80 dashboard` to attach the terminal UI to it, or pass --force to start a second sampler anyway.",
				"a dashboard sampler is already serving on 127.0.0.1:%s", port))
		}
	}
	childEnv := []string{}
	if os.Getenv("AI_DASHBOARD_STATE") == "" {
		dir := dashboardState()
		if err := os.MkdirAll(dir, 0o700); err == nil {
			childEnv = append(childEnv, "AI_DASHBOARD_STATE="+dir)
		}
	}
	res, err := runChild(e, r, append([]string{verb}, rest...), childEnv, nil)
	return exitFromChild(e, "dashboard", res, err)
}

func dashboardState() string { return filepath.Join(stateDir(), "dashboard") }

// portFrom reads --port N / --port=N from the tool's arguments without
// removing it, since the tool needs it too.
func portFrom(args []string, def string) string {
	for i, a := range args {
		switch {
		case a == "--port" && i+1 < len(args):
			return args[i+1]
		case len(a) > 7 && a[:7] == "--port=":
			return a[7:]
		}
	}
	return def
}

var samplerProbe = &http.Client{Timeout: 700 * time.Millisecond}

// samplerAnswers reports whether a dashboard server already serves its
// snapshot on the port.
func samplerAnswers(port string) bool {
	resp, err := samplerProbe.Get(fmt.Sprintf("http://127.0.0.1:%s/api/snapshot", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
