package pytool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedManifestIsValid(t *testing.T) {
	tools, err := Manifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"betterbench", "vast", "pwdet", "dashboard"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("manifest lacks %s", name)
		}
	}
	bb := tools["betterbench"]
	if !bb.Pinned() || bb.Commit != "f72bb9011a830fd98e82ab89d01e549efa76e9c5" || bb.Env != "L80_BETTERBENCH" {
		t.Errorf("betterbench pin = %+v", bb)
	}
	if tools["pwdet"].Python != "3.12" {
		t.Error("pwdet needs python 3.12")
	}
}

func TestParseManifestRejectsBadEntries(t *testing.T) {
	cases := map[string]string{
		"schema":    `{"schema":2,"tools":{}}`,
		"short sha": `{"schema":1,"tools":{"x":{"repo":"r","package":"p","exe":"e","env":"L80_X","commit":"abc"}}}`,
		"no exe":    `{"schema":1,"tools":{"x":{"repo":"r","package":"p","env":"L80_X"}}}`,
		"env name":  `{"schema":1,"tools":{"x":{"repo":"r","package":"p","exe":"e","env":"X"}}}`,
	}
	for name, js := range cases {
		if _, err := parseManifest([]byte(js)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestUvxArgvAndExtras(t *testing.T) {
	tool := Tool{Package: "pwdet", Exe: "pwdet", Repo: "https://github.com/launch80/pwdet", Commit: strings.Repeat("a", 40),
		Env: "L80_PWDET", Python: "3.12", Extras: []string{"gguf", "pickle"}}
	t.Setenv("L80_PWDET_EXTRAS", "")
	os.Unsetenv("L80_PWDET_EXTRAS")
	got := strings.Join(tool.UvxArgv("/u/uvx"), " ")
	want := "/u/uvx --python 3.12 --from pwdet[gguf,pickle] @ git+https://github.com/launch80/pwdet@" + strings.Repeat("a", 40) + " pwdet"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	t.Setenv("L80_PWDET_EXTRAS", "gguf")
	if !strings.HasPrefix(tool.Requirement(), "pwdet[gguf] @") {
		t.Errorf("extras override: %s", tool.Requirement())
	}
	t.Setenv("L80_PWDET_EXTRAS", "")
	if !strings.HasPrefix(tool.Requirement(), "pwdet @") {
		t.Errorf("empty override must drop extras: %s", tool.Requirement())
	}
}

func swap(t *testing.T, paths map[string]string, version string, verr error) {
	t.Helper()
	oldL, oldP := lookPath, probeVersion
	t.Cleanup(func() { lookPath, probeVersion = oldL, oldP })
	lookPath = func(name string) (string, error) {
		if p, ok := paths[name]; ok {
			return p, nil
		}
		return "", exec.ErrNotFound
	}
	probeVersion = func([]string) (string, error) { return version, verr }
}

func bbTool() Tool {
	return Tool{Name: "betterbench", Package: "betterbench", Exe: "betterbench", Env: "L80_BETTERBENCH",
		Repo: "https://github.com/GGZ14/BetterBench", Commit: strings.Repeat("f", 40), MinVersion: "0.6.1", VersionCmd: []string{"--version"}}
}

func TestResolveOrder(t *testing.T) {
	t.Setenv("L80_BETTERBENCH", "")

	swap(t, map[string]string{"betterbench": "/p/betterbench", "uvx": "/p/uvx"}, "0.6.1", nil)
	r, err := Resolve(bbTool())
	if err != nil || r.Source != "PATH" || r.Argv[0] != "/p/betterbench" || r.Version != "0.6.1" {
		t.Fatalf("current PATH copy should win: %+v %v", r, err)
	}

	swap(t, map[string]string{"betterbench": "/p/betterbench", "uvx": "/p/uvx"}, "0.4.0", nil)
	r, err = Resolve(bbTool())
	if err != nil || !r.Pinned() || r.Argv[0] != "/p/uvx" || len(r.Notes) == 0 {
		t.Fatalf("stale PATH copy should fall through to uvx with a note: %+v %v", r, err)
	}

	swap(t, map[string]string{"betterbench": "/p/betterbench"}, "0.4.0", nil)
	r, err = Resolve(bbTool())
	if err != nil || r.Source != "PATH (older than pinned)" {
		t.Fatalf("stale copy is better than nothing: %+v %v", r, err)
	}

	swap(t, map[string]string{"uvx": "/p/uvx"}, "", nil)
	r, err = Resolve(bbTool())
	if err != nil || strings.Join(r.Argv, " ") != "/p/uvx --from betterbench @ git+https://github.com/GGZ14/BetterBench@"+strings.Repeat("f", 40)+" betterbench" {
		t.Fatalf("uvx bootstrap: %+v %v", r, err)
	}

	unpinned := bbTool()
	unpinned.Commit = ""
	var nr *ErrNoRunner
	if _, err = Resolve(unpinned); !errors.As(err, &nr) || !nr.HasUvx {
		t.Fatalf("unpinned with uvx should be ErrNoRunner{HasUvx}: %v", err)
	}

	swap(t, map[string]string{}, "", nil)
	if _, err = Resolve(bbTool()); !errors.As(err, &nr) || nr.HasUvx || !strings.Contains(nr.Remedy(), "L80_BETTERBENCH") {
		t.Fatalf("nothing available: %v", err)
	}

	t.Setenv("L80_BETTERBENCH", "/x/.venv/bin/python -m betterbench")
	r, err = Resolve(bbTool())
	if err != nil || r.Source != "L80_BETTERBENCH" || strings.Join(r.Argv, " ") != "/x/.venv/bin/python -m betterbench" {
		t.Fatalf("env override: %+v %v", r, err)
	}
}

func TestVersionHelpers(t *testing.T) {
	for in, want := range map[string]string{"BetterBench 0.6.1": "0.6.1", "pwdet, version 0.1": "0.1.0", "v1.2.3-rc1": "1.2.3"} {
		if got, ok := ParseVersion(in); !ok || got != want {
			t.Errorf("ParseVersion(%q) = %q", in, got)
		}
	}
	if _, ok := ParseVersion("no digits"); ok {
		t.Error("want no version")
	}
	if Compare("0.6", "0.6.0") != 0 || Compare("0.10.0", "0.9.9") != 1 || Compare("0.4.0", "0.6.1") != -1 {
		t.Error("Compare is wrong")
	}
}

func TestRedactArgv(t *testing.T) {
	in := []string{"uvx", "run", "--api-key", "sk-live", "--api-key=sk2", "--model", "m", "VAST_API_KEY=abc", "--note", "engine=vLLM", "-token", "t"}
	got := strings.Join(RedactArgv(in), " ")
	want := "uvx run --api-key *** --api-key=*** --model m VAST_API_KEY=*** --note engine=vLLM -token ***"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if in[3] != "sk-live" {
		t.Error("input must not be modified")
	}
}

func script(t *testing.T, body string) Runner {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return Runner{Argv: []string{p}}
}

func TestExecReturnsChildStatusAndEnv(t *testing.T) {
	r := script(t, `echo "$L80_X $1 $PYTHONUNBUFFERED"; exit 3`+"\n")
	r.Env = []string{"L80_X=hello"}
	var out strings.Builder
	code, err := Exec(context.Background(), r, []string{"arg"}, ExecOpts{Stdout: &out})
	// Captured output must stream: Python tools get unbuffered stdout.
	if err != nil || code != 3 || strings.TrimSpace(out.String()) != "hello arg 1" {
		t.Fatalf("code %d err %v out %q", code, err, out.String())
	}
	if _, err := Exec(context.Background(), Runner{Argv: []string{"/nonexistent/x"}}, nil, ExecOpts{}); !errors.Is(err, ErrNotStarted) {
		t.Errorf("want ErrNotStarted, got %v", err)
	}
}

func TestExecCancelSendsInterruptFirst(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "got-int")
	r := script(t, "trap 'echo int > "+marker+"; exit 130' INT\nwhile :; do sleep 0.05; done\n")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	code, _ := Exec(ctx, r, nil, ExecOpts{GraceAfterCancel: 3 * time.Second})
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("child did not see SIGINT (code %d)", code)
	}
}

func TestFormatArgvQuotes(t *testing.T) {
	got := FormatArgv([]string{"uvx", "--from", "betterbench @ git+x@y", "run", "--note", "engine=vLLM 0.9", "--api-key", "k"})
	want := `uvx --from 'betterbench @ git+x@y' run --note 'engine=vLLM 0.9' --api-key '***'`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
