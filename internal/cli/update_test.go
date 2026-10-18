package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launch80/L80-CLI/internal/api"
)

func TestNewerThan(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"v0.1.1", "0.1.0", true},
		{"v0.1.0", "0.1.0", false},
		{"v0.1.0", "0.1.1", false},
		{"v1.0.0", "0.9.9", true},
		{"v0.10.0", "0.9.0", true},
		{"v0.2.0", "dev", true},     // a dev build should always be offered the release
		{"garbage", "0.1.0", false}, // an unreadable tag must never trigger a download
	}
	for _, c := range cases {
		if got := newerThan(c.candidate, c.current); got != c.want {
			t.Errorf("newerThan(%q, %q) = %v, want %v", c.candidate, c.current, got, c.want)
		}
	}
}

func TestExpectedChecksumParsesSha256sumFormat(t *testing.T) {
	sums := []byte("aaaa  L80_darwin_arm64.tar.gz\nbbbb  L80_linux_amd64.tar.gz\n")
	got, ok := expectedChecksum(sums, "L80_linux_amd64.tar.gz")
	if !ok || got != "bbbb" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	if _, ok := expectedChecksum(sums, "L80_windows_amd64.tar.gz"); ok {
		t.Fatal("unlisted asset must not match")
	}
}

// tarball builds a release-shaped archive containing one file named L80.
func tarball(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// fakeRelease stands in for GitHub: a latest-release index plus asset downloads.
// Returns the server and the "installed" binary path the update should replace.
func fakeRelease(t *testing.T, tag string, tgz []byte, checksums string) (target string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("GitHub requires a User-Agent; none was sent")
		}
		fmt.Fprintf(w, `{"tag_name":%q}`, tag)
	})
	mux.HandleFunc("/releases/download/"+tag+"/"+assetName(), func(w http.ResponseWriter, r *http.Request) {
		w.Write(tgz)
	})
	mux.HandleFunc("/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, checksums)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	oldSrc, oldExe := updateSource, currentExecutable
	updateSource = releaseSource{apiBase: srv.URL, downloadBase: srv.URL}
	target = filepath.Join(t.TempDir(), "L80")
	if err := os.WriteFile(target, []byte("OLD BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	currentExecutable = func() (string, error) { return target, nil }
	t.Cleanup(func() { updateSource, currentExecutable = oldSrc, oldExe })
	stubSync(t, syncResult{})
	return target
}

// stubSync replaces the tool and skill sync, and the re-exec of the new
// binary, so update tests never touch the machine they run on. The fake
// child answers as a real one would: by running the resumed update here.
func stubSync(t *testing.T, res syncResult) (reexecArgs *[]string) {
	t.Helper()
	oldSync, oldReexec := syncEverything, reexecUpdate
	syncEverything = func(env, syncOpts) syncResult { return res }
	var got []string
	reexecUpdate = func(path string, args []string, e env) (int, error) {
		got = append([]string{path}, args...)
		return runUpdate(e, args[1:]), nil
	}
	t.Cleanup(func() { syncEverything, reexecUpdate = oldSync, oldReexec })
	return &got
}

func sumOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func run(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, er bytes.Buffer
	code = runUpdate(env{stdout: &o, stderr: &er}, args)
	return code, o.String(), er.String()
}

func TestUpdateReplacesBinaryInPlace(t *testing.T) {
	newBin := []byte("NEW BINARY")
	tgz := tarball(t, "L80", newBin)
	target := fakeRelease(t, "v99.0.0", tgz, sumOf(tgz)+"  "+assetName()+"\n")

	code, out, errOut := run(t)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(out, "updated L80 "+Version+" -> ") || !strings.Contains(out, "everything is current") {
		t.Errorf("unexpected output: %s", out)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(newBin) {
		t.Fatalf("binary not replaced: %q", got)
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("replaced binary is not executable")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".L80.update-*")); len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestUpdateRefusesChecksumMismatchAndLeavesBinaryIntact(t *testing.T) {
	tgz := tarball(t, "L80", []byte("NEW BINARY"))
	target := fakeRelease(t, "v99.0.0", tgz, strings.Repeat("0", 64)+"  "+assetName()+"\n")

	code, _, errOut := run(t)
	if code == 0 || !strings.Contains(errOut, "E_UPDATE_FAILED") || !strings.Contains(errOut, "checksum mismatch") {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "OLD BINARY" {
		t.Fatalf("binary was replaced despite bad checksum: %q", got)
	}
}

func TestUpdateCheckOnlyReportsWithoutInstalling(t *testing.T) {
	tgz := tarball(t, "L80", []byte("NEW BINARY"))
	target := fakeRelease(t, "v99.0.0", tgz, sumOf(tgz)+"  "+assetName()+"\n")

	code, out, _ := run(t, "--check")
	if code != 0 || !strings.Contains(out, "update available: "+Version+" -> 99.0.0") {
		t.Fatalf("exit %d, out: %s", code, out)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "OLD BINARY" {
		t.Fatal("--check must not install")
	}

	code, out, _ = run(t, "--check", "--json")
	if code != 0 || !strings.Contains(out, `"update_available": true`) || !strings.Contains(out, `"installed": false`) {
		t.Fatalf("json: exit %d, out: %s", code, out)
	}
}

func TestUpdateWhenAlreadyCurrentDoesNothing(t *testing.T) {
	tgz := tarball(t, "L80", []byte("NEW BINARY"))
	target := fakeRelease(t, "v"+Version, tgz, sumOf(tgz)+"  "+assetName()+"\n")

	code, out, _ := run(t)
	if code != 0 || !strings.Contains(out, "is up to date") {
		t.Fatalf("exit %d, out: %s", code, out)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "OLD BINARY" {
		t.Fatal("binary must be untouched when already current")
	}
}

func TestUpdateRejectsArchiveWithoutTheBinary(t *testing.T) {
	tgz := tarball(t, "README.md", []byte("not a binary"))
	target := fakeRelease(t, "v99.0.0", tgz, sumOf(tgz)+"  "+assetName()+"\n")

	code, _, errOut := run(t)
	if code == 0 || !strings.Contains(errOut, "does not contain L80") {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "OLD BINARY" {
		t.Fatal("binary must be untouched")
	}
}

func TestUpdateHandsTheSyncToTheNewBinary(t *testing.T) {
	tgz := tarball(t, "L80", []byte("NEW BINARY"))
	target := fakeRelease(t, "v99.0.0", tgz, sumOf(tgz)+"  "+assetName()+"\n")
	args := stubSync(t, syncResult{})

	code, out, _ := run(t, "--json", "--no-prefetch")
	if code != 0 {
		t.Fatalf("exit %d, out: %s", code, out)
	}
	want := []string{target, "update", "--resumed-from", Version, "--json", "--no-prefetch"}
	if strings.Join(*args, " ") != strings.Join(want, " ") {
		t.Fatalf("re-exec argv = %q, want %q", *args, want)
	}
	for _, s := range []string{`"installed": true`, `"current": "` + Version + `"`, `"up_to_date": true`, `"actions": []`} {
		if !strings.Contains(out, s) {
			t.Errorf("json lacks %s: %s", s, out)
		}
	}
}

func TestUpdateFallsBackWhenTheNewBinaryWillNotStart(t *testing.T) {
	tgz := tarball(t, "L80", []byte("NEW BINARY"))
	fakeRelease(t, "v99.0.0", tgz, sumOf(tgz)+"  "+assetName()+"\n")
	reexecUpdate = func(string, []string, env) (int, error) { return 0, errors.New("exec format error") }

	code, out, _ := run(t)
	if code != api.ExitActionNeeded || !strings.Contains(out, "updated L80 "+Version+" -> 99.0.0") ||
		!strings.Contains(out, "Run `L80 update` again") {
		t.Fatalf("exit %d, out: %s", code, out)
	}
}

func TestUpdateReportsActionsAndExitsActionNeeded(t *testing.T) {
	tgz := tarball(t, "L80", nil)
	fakeRelease(t, "v"+Version, tgz, "")
	stubSync(t, syncResult{Actions: []string{"betterbench: unset L80_BETTERBENCH"}})

	code, out, _ := run(t, "--json")
	if code != api.ExitActionNeeded || !strings.Contains(out, `"up_to_date": false`) || !strings.Contains(out, "unset L80_BETTERBENCH") {
		t.Fatalf("exit %d, out: %s", code, out)
	}
	// --check reports the same thing but is not itself a failure.
	if code, _, _ := run(t, "--check"); code != 0 {
		t.Fatalf("--check exit %d", code)
	}
}

func TestUpdateCheckSaysToRunUpdateWhenItWouldChangeSomething(t *testing.T) {
	tgz := tarball(t, "L80", nil)
	fakeRelease(t, "v"+Version, tgz, "")
	stubSync(t, syncResult{Pending: true})

	code, out, _ := run(t, "--check", "--json")
	if code != 0 || !strings.Contains(out, "Run `L80 update`") || !strings.Contains(out, `"up_to_date": false`) {
		t.Fatalf("exit %d, out: %s", code, out)
	}
}

// fakeBetterbench writes a script that reports version v, and points the
// override variable at it.
func fakeBetterbench(t *testing.T, v string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "betterbench")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho BetterBench "+v+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("L80_BETTERBENCH", p)
}

func isolateSync(t *testing.T, profile string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	rc := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(rc, []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	oldP, oldD := profileFiles, uvToolDir
	profileFiles = func() []string { return []string{rc} }
	uvToolDir = func() (string, error) { return "", errors.New("no uv in tests") }
	t.Cleanup(func() { profileFiles, uvToolDir = oldP, oldD })
	for _, n := range []string{"L80_VAST", "L80_PWDET", "L80_DASHBOARD"} {
		t.Setenv(n, "true") // resolve the other tools without probing or downloading
	}
}

func toolByName(res syncResult, name string) toolSync {
	for _, r := range res.Tools {
		if r.Name == name {
			return r
		}
	}
	return toolSync{}
}

func TestSyncNamesTheProfileLineOfAStaleOverride(t *testing.T) {
	isolateSync(t, "# export L80_BETTERBENCH=/old\nexport PATH=$PATH\nexport L80_BETTERBENCH=\"$HOME/bb\"\n")
	fakeBetterbench(t, "0.4.0")

	res := defaultSync(env{stdout: io.Discard, stderr: io.Discard}, syncOpts{apply: true})
	bb := toolByName(res, "betterbench")
	if bb.Status != "stale" || bb.Version != "0.4.0" {
		t.Fatalf("row = %+v", bb)
	}
	if len(res.Actions) != 1 || !strings.Contains(res.Actions[0], ".bashrc:3") || strings.Contains(res.Actions[0], ".bashrc:1") {
		t.Fatalf("actions = %q", res.Actions)
	}
}

func TestSyncAcceptsACurrentOverride(t *testing.T) {
	isolateSync(t, "")
	fakeBetterbench(t, "0.6.1")

	res := defaultSync(env{stdout: io.Discard, stderr: io.Discard}, syncOpts{apply: true})
	if bb := toolByName(res, "betterbench"); bb.Status != "current" || len(res.Actions) != 0 {
		t.Fatalf("row = %+v, actions = %q", bb, res.Actions)
	}
}

func TestSyncRefreshesInstalledSkillsOnly(t *testing.T) {
	isolateSync(t, "")
	fakeBetterbench(t, "0.6.1")
	root := os.Getenv("CLAUDE_CONFIG_DIR")

	copyDir := filepath.Join(root, "skills", "l80-test-report")
	if err := materialize("l80-test-report", copyDir); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(copyDir, "SKILL.md")
	os.WriteFile(stale, []byte("old skill"), 0o644)
	os.WriteFile(filepath.Join(copyDir, "NOTES.md"), []byte("mine"), 0o644)
	src := t.TempDir()
	os.Symlink(src, filepath.Join(root, "skills", "l80-vast"))

	quiet := env{stdout: io.Discard, stderr: io.Discard}
	check := defaultSync(quiet, syncOpts{})
	if !check.Pending || check.Skills[0].Status != "outdated" {
		t.Fatalf("check: %+v", check)
	}
	if b, _ := os.ReadFile(stale); string(b) != "old skill" {
		t.Fatal("--check must not change a skill")
	}

	res := defaultSync(quiet, syncOpts{apply: true})
	got := map[string]string{}
	for _, s := range res.Skills {
		got[s.Name] = s.Status
	}
	if got["l80-test-report"] != "updated" || got["l80-vast"] != "linked" || got["l80-pwdet"] != "not_installed" {
		t.Fatalf("statuses = %v", got)
	}
	if !skillMatches("l80-test-report", copyDir) {
		t.Fatal("skill not refreshed")
	}
	if b, _ := os.ReadFile(filepath.Join(copyDir, "NOTES.md")); string(b) != "mine" {
		t.Fatal("a file the user added was removed")
	}
	if len(res.Actions) != 0 {
		t.Fatalf("an uninstalled skill is not an action: %q", res.Actions)
	}
}

func TestSyncReportsAnOverrideThatPointsAtNothing(t *testing.T) {
	isolateSync(t, "export L80_BETTERBENCH=/gone/betterbench\n")
	t.Setenv("L80_BETTERBENCH", filepath.Join(t.TempDir(), "gone", "betterbench"))

	res := defaultSync(env{stdout: io.Discard, stderr: io.Discard}, syncOpts{apply: true})
	if bb := toolByName(res, "betterbench"); bb.Status != "missing" {
		t.Fatalf("row = %+v", bb)
	}
	if len(res.Actions) != 1 || !strings.Contains(res.Actions[0], "not a command") || !strings.Contains(res.Actions[0], ".bashrc:1") {
		t.Fatalf("actions = %q", res.Actions)
	}
}
