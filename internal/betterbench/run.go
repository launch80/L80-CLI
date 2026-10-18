package betterbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/launch80/L80-CLI/internal/pytool"
)

// RawTemplateID is the server template that stores a results.json as written.
const RawTemplateID = "bench.betterbench.v1"

// RawMaxPayloadBytes mirrors the server's limit for RawTemplateID.
const RawMaxPayloadBytes = 4 * 1024 * 1024

// Runner is a resolved way to invoke the betterbench CLI.
type Runner = pytool.Runner

// ErrNoRunner means BetterBench cannot be run on this machine; errors.As it
// to a *pytool.ErrNoRunner for the remedy.
var ErrNoRunner = errors.New("betterbench is not installed and uvx is not available to bootstrap it")

// Tool returns BetterBench's manifest entry: the pinned commit and the
// version this L80 release renders.
func Tool() pytool.Tool {
	t, err := pytool.Lookup("betterbench")
	if err != nil {
		// The manifest is embedded and tested; this cannot happen in a build.
		panic(err)
	}
	return t
}

// ResolveRunner finds how to run BetterBench: L80_BETTERBENCH, then a
// betterbench on PATH at least as new as the pinned version, then
// `uvx --from betterbench @ git+https://github.com/GGZ14/BetterBench@<pin>`.
func ResolveRunner() (Runner, error) {
	r, err := pytool.Resolve(Tool())
	if err != nil {
		return Runner{}, fmt.Errorf("%w: %v", ErrNoRunner, err)
	}
	return r, nil
}

// RunOptions are the parts of `betterbench run` this command exposes by name.
// Anything else goes through Extra verbatim.
type RunOptions struct {
	Endpoint                 string
	Model                    string
	Out                      string
	Quick                    bool
	Passes                   int
	Warmup                   int
	Notes                    []string // KEY=VALUE, as --note takes them
	Phases                   []string // "decode", "prefill", "concurrency": each becomes --<phase>
	NoPrefill, NoConcurrency bool
	Extra                    []string // passed through after the named flags
	// APIKey authenticates requests to the endpoint. It reaches the child as
	// BETTERBENCH_API_KEY, never on the command line, so it cannot show up in
	// an echoed command, a process listing, or a results file.
	APIKey string
}

// Env is the environment the run adds for the child. The update check is
// switched off because L80 owns version alignment: the pinned version is the
// one launch80 renders, and a "newer release" notice would only invite a
// mismatch.
func (o RunOptions) Env() []string {
	env := []string{"BETTERBENCH_NO_UPDATE_CHECK=1"}
	if o.APIKey != "" {
		env = append(env, "BETTERBENCH_API_KEY="+o.APIKey)
	}
	return env
}

// Args builds the argv for `betterbench run`.
func (o RunOptions) Args() []string {
	args := []string{"run", "--endpoint", o.Endpoint, "--model", o.Model, "--out", o.Out}
	if o.Quick {
		args = append(args, "--quick")
	}
	if o.Passes > 0 {
		args = append(args, "--passes", fmt.Sprint(o.Passes))
	}
	if o.Warmup >= 0 && o.warmupSet() {
		args = append(args, "--warmup", fmt.Sprint(o.Warmup))
	}
	for _, n := range o.Notes {
		args = append(args, "--note", n)
	}
	for _, p := range o.Phases {
		args = append(args, "--"+p)
	}
	if o.NoPrefill {
		args = append(args, "--no-prefill")
	}
	if o.NoConcurrency {
		args = append(args, "--no-concurrency")
	}
	return append(args, o.Extra...)
}

// warmupSet distinguishes "--warmup 0" from "not given". Callers set Warmup to
// -1 when the flag was absent.
func (o RunOptions) warmupSet() bool { return o.Warmup >= 0 }

// Execute runs `betterbench run`, streaming its output to out/errOut, and
// returns the results file it wrote. Cancelling ctx interrupts the run.
func Execute(ctx context.Context, r Runner, o RunOptions, out, errOut io.Writer) ([]byte, error) {
	code, err := pytool.Exec(ctx, r, o.Args(), pytool.ExecOpts{Stdout: out, Stderr: errOut, ExtraEnv: o.Env()})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("betterbench exited with status %d", code)
	}
	data, err := os.ReadFile(o.Out)
	if err != nil {
		return nil, fmt.Errorf("betterbench finished but %s was not written: %w", o.Out, err)
	}
	return data, nil
}

// Prose is the publisher-supplied text the raw template accepts under "l80".
type Prose struct {
	Title    string    `json:"title,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	Sections []Section `json:"sections,omitempty"`
}

// strippedEnvKeys never leave the machine: the harness records the LAN URL it
// hit and the hostname it ran on. The server's schema refuses both, so a file
// that skipped this step fails there with a pointer rather than publishing.
var strippedEnvKeys = []string{"endpoint", "host"}

// PrepareRaw turns a results.json into the body to publish under
// RawTemplateID: it removes env.endpoint and env.host, sets $template, and
// attaches prose under "l80" if any was given. Values are copied as raw bytes,
// so no number is re-encoded.
func PrepareRaw(data []byte, prose Prose) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSyntax, err)
	}
	if _, ok := top["$template"]; ok {
		if s, _ := templateOf(top); s != RawTemplateID {
			return nil, ErrAlreadyPayload
		}
	}
	envRaw, ok := top["env"]
	if !ok {
		return nil, fmt.Errorf("%w: no env field", ErrNotBetterBench)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(envRaw, &env); err != nil {
		return nil, fmt.Errorf("%w: env is not an object", ErrNotBetterBench)
	}
	if _, ok := env["model"]; !ok {
		return nil, fmt.Errorf("%w: env.model is missing", ErrNotBetterBench)
	}
	for _, k := range strippedEnvKeys {
		delete(env, k)
	}
	envOut, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	top["env"] = envOut
	top["$template"] = json.RawMessage(`"` + RawTemplateID + `"`)

	if prose.Title != "" || prose.Summary != "" || len(prose.Sections) > 0 {
		if err := checkLen("--title", prose.Title, maxTitle); err != nil {
			return nil, err
		}
		if err := checkLen("--summary", prose.Summary, maxSummary); err != nil {
			return nil, err
		}
		for i, s := range prose.Sections {
			if s.Heading == "" || s.Body == "" {
				return nil, &InputError{fmt.Sprintf("section %d needs both a heading and a body", i+1)}
			}
			if err := checkLen("section heading", s.Heading, maxHeading); err != nil {
				return nil, err
			}
			if err := checkLen("section body", s.Body, maxBody); err != nil {
				return nil, err
			}
		}
		if len(prose.Sections) > maxSections {
			return nil, &InputError{fmt.Sprintf("at most %d sections", maxSections)}
		}
		l80, err := json.Marshal(prose)
		if err != nil {
			return nil, err
		}
		top["l80"] = l80
	}

	out, err := json.Marshal(top)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func templateOf(top map[string]json.RawMessage) (string, bool) {
	raw, ok := top["$template"]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// StripsIdentity reports whether a prepared body still carries the fields
// PrepareRaw removes. Used by tests and by publish's belt-and-braces check.
func StripsIdentity(body []byte) bool {
	var top struct {
		Env map[string]json.RawMessage `json:"env"`
	}
	if err := json.Unmarshal(body, &top); err != nil {
		return false
	}
	for _, k := range strippedEnvKeys {
		if _, ok := top.Env[k]; ok {
			return false
		}
	}
	return true
}
