package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplitOwned(t *testing.T) {
	owned := ownedFlags{"json": false, "title": true, "max-hours": true}
	got, rest, err := splitOwned([]string{"scan", "m", "--json", "--title", "T", "--max-hours=2", "--profile", "quick", "--", "--json", "x"}, owned)
	if err != nil {
		t.Fatal(err)
	}
	if got["json"] != "true" || got["title"] != "T" || got["max-hours"] != "2" {
		t.Errorf("owned = %v", got)
	}
	if strings.Join(rest, " ") != "scan m --profile quick --json x" {
		t.Errorf("rest = %v", rest)
	}
	if _, _, err := splitOwned([]string{"--title"}, owned); err == nil {
		t.Error("missing value must be an error")
	}
}

// fakeTool writes a shell script and points the tool's override at it.
func fakeTool(t *testing.T, envVar, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envVar, p)
	return p
}

func runCmd(t *testing.T, f func(env, []string) int, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := f(env{stdout: &out, stderr: &errOut}, args)
	return code, out.String(), errOut.String()
}

func TestVastPassesThroughWithCallbackEnv(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("BBVASTL80_RUNS_DIR", "")
	t.Setenv("L80_BETTERBENCH", "/opt/my/betterbench")
	fakeTool(t, "L80_VAST", `echo "args:$*"; echo "bin:$L80_BIN"; echo "bb:$L80_BETTERBENCH"; echo "runs:$BBVASTL80_RUNS_DIR"; exit 4`+"\n")
	code, out, _ := runCmd(t, runVast, "destroy", "--run", "r1")
	if code != 4 {
		t.Errorf("child exit status must pass through, got %d", code)
	}
	if !strings.Contains(out, "args:destroy --run r1") || strings.Contains(out, "-y") {
		t.Errorf("args must be verbatim and never gain -y: %s", out)
	}
	runs := filepath.Join(state, "L80", "vast", "runs")
	shim := filepath.Join(state, "L80", "shims", "betterbench")
	if !strings.Contains(out, "runs:"+runs) || !strings.Contains(out, "bb:"+shim) || !strings.Contains(out, "bin:/") {
		t.Errorf("env not injected: %s", out)
	}
	body, err := os.ReadFile(shim)
	if err != nil || !strings.Contains(string(body), "L80_BETTERBENCH='/opt/my/betterbench' exec") || !strings.Contains(string(body), "tools run betterbench") {
		t.Errorf("shim = %s %v", body, err)
	}
}

func TestVastRunNamesBillingInstances(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	runs := filepath.Join(state, "runs")
	t.Setenv("BBVASTL80_RUNS_DIR", runs)
	now := time.Now().Unix()
	fakeTool(t, "L80_VAST", fmt.Sprintf(`mkdir -p "$BBVASTL80_RUNS_DIR/spec-%[1]d"
echo '{"run_id":"spec-%[1]d","created_at":%[1]d,"status":"failed","instance_id":42,"dph":3.0,"started_billing_at":%[1]d}' > "$BBVASTL80_RUNS_DIR/spec-%[1]d/state.json"
echo running; exit 1
`, now))
	code, out, errOut := runCmd(t, runVast, "run", "--spec", "x.yaml", "--json")
	if code != 1 {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(errOut, "instance 42 from run spec-") || !strings.Contains(errOut, "L80 vast destroy --run spec-") {
		t.Errorf("reminder missing: %s", errOut)
	}
	if !strings.Contains(errOut, "running") {
		t.Error("child stdout should go to stderr under --json")
	}
	var got struct {
		ExitStatus int              `json:"exit_status"`
		Billing    []map[string]any `json:"may_be_billing"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.ExitStatus != 1 || len(got.Billing) != 1 {
		t.Errorf("json = %s %v", out, err)
	}
	code, out, _ = runCmd(t, runVast, "status", "--json")
	if code != 0 || !strings.Contains(out, `"may_be_billing": true`) {
		t.Errorf("status --json: %d %s", code, out)
	}
}

func TestVastWatchdogDestroys(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	runs := filepath.Join(state, "runs")
	t.Setenv("BBVASTL80_RUNS_DIR", runs)
	log := filepath.Join(state, "calls")
	now := time.Now().Unix()
	fakeTool(t, "L80_VAST", fmt.Sprintf(`echo "$*" >> %[2]s
if [ "$1" = destroy ]; then sed -i 's/"failed"/"torn_down"/' "$BBVASTL80_RUNS_DIR/r-%[1]d/state.json"; exit 0; fi
mkdir -p "$BBVASTL80_RUNS_DIR/r-%[1]d"
echo '{"run_id":"r-%[1]d","created_at":%[1]d,"status":"failed","instance_id":7}' > "$BBVASTL80_RUNS_DIR/r-%[1]d/state.json"
trap 'exit 130' INT
while :; do sleep 0.05; done
`, now, log))
	code, _, errOut := runCmd(t, runVast, "run", "--spec", "x.yaml", "--max-hours", "0.0001")
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), fmt.Sprintf("destroy --run r-%d -y", now)) {
		t.Errorf("watchdog did not destroy (exit %d): calls=%q\n%s", code, calls, errOut)
	}
	if strings.Contains(errOut, "may still be billing") {
		t.Errorf("a destroyed instance must not be reported as billing: %s", errOut)
	}
}

func TestPwdetScanJSONAndDryRunPublish(t *testing.T) {
	report, _ := filepath.Abs("../pwdet/testdata/report_v1.json")
	fakeTool(t, "L80_PWDET", `echo "progress" >&2; echo "{\"verdict\": \"clean\", \"report\": \"`+report+`\"}"; exit 0`+"\n")
	payload := filepath.Join(t.TempDir(), "p.json")
	code, out, errOut := runCmd(t, runPwdet, "scan", "Wrapzii/x", "--json", "--publish", "--dry-run", "--payload-out", payload)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, `"verdict": "clean"`) || !strings.Contains(out, `"template": "generic.report.v1"`) {
		t.Errorf("json = %s", out)
	}
	b, _ := os.ReadFile(payload)
	if strings.Contains(string(b), "rig01-test") || strings.Contains(string(b), "/home/tester") {
		t.Error("payload leaks host or paths")
	}
	// pwdet's verdict exit status passes through.
	fakeTool(t, "L80_PWDET", `echo "{\"verdict\": \"malicious\", \"report\": \"`+report+`\"}"; exit 2`+"\n")
	if code, _, _ := runCmd(t, runPwdet, "scan", "m", "--json"); code != 2 {
		t.Errorf("tampered verdict must exit 2, got %d", code)
	}
	// Plain verbs are untouched.
	fakeTool(t, "L80_PWDET", `echo "args:$*"`+"\n")
	if _, out, _ := runCmd(t, runPwdet, "checks", "list", "--plain"); strings.TrimSpace(out) != "args:checks list --plain" {
		t.Errorf("passthrough = %q", out)
	}
	if _, out, _ := runCmd(t, runPwdet, "scan", "m", "--profile", "quick"); strings.TrimSpace(out) != "args:scan m --profile quick" {
		t.Errorf("plain scan must not gain --json-only: %q", out)
	}
}

func TestDashboardRefusesSecondSampler(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/snapshot" {
			fmt.Fprint(w, `{"ready":true}`)
		}
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	fakeTool(t, "L80_DASHBOARD", `echo "args:$*|$AI_DASHBOARD_STATE"`+"\n")
	code, _, errOut := runCmd(t, runDashboard, "serve", "--port", port)
	if code != 2 || !strings.Contains(errOut, "already serving") {
		t.Errorf("exit %d: %s", code, errOut)
	}
	code, out, _ := runCmd(t, runDashboard, "serve", "--port", port, "--force")
	if code != 0 || !strings.Contains(out, "args:serve --port "+port+"|") {
		t.Errorf("--force: exit %d %s", code, out)
	}
	if _, out, _ := runCmd(t, runDashboard); !strings.HasPrefix(strings.TrimSpace(out), "args:tui|") || !strings.Contains(out, "dashboard") {
		t.Errorf("default verb: %s", out)
	}
}
