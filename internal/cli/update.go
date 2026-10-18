package cli

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/output"
)

// releaseSource is where `L80 update` looks for releases. It mirrors what
// launch80.com/install.sh does, so the two never disagree about which binary
// is current. Tests point both URLs at an httptest server.
type releaseSource struct {
	// apiBase answers GET <apiBase>/releases/latest with GitHub's release JSON.
	apiBase string
	// downloadBase serves <downloadBase>/releases/download/<tag>/<asset>.
	downloadBase string
}

var updateSource = releaseSource{
	apiBase:      "https://api.github.com/repos/launch80/L80-CLI",
	downloadBase: "https://github.com/launch80/L80-CLI",
}

// currentExecutable is swapped in tests so the update replaces a scratch file
// rather than the test binary.
var currentExecutable = func() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

var updateHTTP = &http.Client{Timeout: 90 * time.Second}

const releaseBinaryName = "L80"

// assetName is the tarball the release workflow publishes for this platform.
// It must stay in step with .github/workflows/release.yml and install.sh.
func assetName() string {
	return fmt.Sprintf("%s_%s_%s.tar.gz", releaseBinaryName, runtime.GOOS, runtime.GOARCH)
}

// parseSemver reads "v1.2.3" or "1.2.3" into its three numbers. Anything else
// (a "dev" build, an empty string) is reported as unparseable, and the caller
// treats it as older than any real release so a dev build can still update.
func parseSemver(v string) (nums [3]int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(v, "-", 2)[0]
	fields := strings.Split(parts, ".")
	if len(fields) != 3 {
		return nums, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return nums, false
		}
		nums[i] = n
	}
	return nums, true
}

// newerThan reports whether candidate is a strictly newer release than current.
func newerThan(candidate, current string) bool {
	c, okC := parseSemver(candidate)
	cur, okCur := parseSemver(current)
	if !okC {
		return false
	}
	if !okCur {
		return true
	}
	for i := 0; i < 3; i++ {
		if c[i] != cur[i] {
			return c[i] > cur[i]
		}
	}
	return false
}

func (s releaseSource) latestTag() (string, *api.Error) {
	req, err := http.NewRequest(http.MethodGet, s.apiBase+"/releases/latest", nil)
	if err != nil {
		return "", api.Newf("E_INTERNAL", "Retry once.", "could not build request: %v", err)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("L80/%s (%s/%s)", Version, runtime.GOOS, runtime.GOARCH))
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := updateHTTP.Do(req)
	if err != nil {
		return "", api.Newf("E_NETWORK",
			"Check the network and retry. If GitHub is unreachable, reinstall with `curl -fsSL https://www.launch80.com/install.sh | sh`.",
			"could not reach the release index: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", api.Newf("E_NETWORK",
			"Retry in a few minutes. If it persists, reinstall with `curl -fsSL https://www.launch80.com/install.sh | sh`.",
			"release index returned HTTP %d", resp.StatusCode)
	}

	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil || rel.TagName == "" {
		return "", api.Newf("E_NETWORK", "Retry once.", "release index returned an unreadable response")
	}
	return rel.TagName, nil
}

func (s releaseSource) fetch(tag, name string) ([]byte, *api.Error) {
	url := fmt.Sprintf("%s/releases/download/%s/%s", s.downloadBase, tag, name)
	resp, err := updateHTTP.Get(url)
	if err != nil {
		return nil, api.Newf("E_NETWORK", "Check the network and retry.", "could not download %s: %v", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, api.Newf("E_UPDATE_FAILED",
			"This platform may not have a prebuilt binary for that release. Build from source with `make install`.",
			"%s is not available for %s: HTTP %d", name, tag, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, api.Newf("E_NETWORK", "Retry once.", "download of %s was interrupted: %v", name, err)
	}
	return body, nil
}

// expectedChecksum finds the sha256 listed for asset in a checksums.txt of
// "<hex>  <name>" lines, the format `sha256sum` writes and the workflow ships.
func expectedChecksum(checksums []byte, asset string) (string, bool) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// extractBinary returns the single file the release tarball is required to
// contain, refusing anything that is not a plain file named L80.
func extractBinary(tgz []byte) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(tgz)))
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("archive does not contain " + releaseBinaryName)
		}
		if err != nil {
			return nil, fmt.Errorf("unreadable archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != releaseBinaryName {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, 64<<20))
	}
}

// replaceExecutable writes the new binary beside the current one and renames
// it into place, so a crash mid-write can never leave a half-written L80.
func replaceExecutable(target string, bin []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, "."+releaseBinaryName+".update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		cleanup()
		return err
	}
	return nil
}

// binaryState is the binary half of the update report. The JSON keys predate
// the tool and skill sync and are kept as they were.
type binaryState struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Installed       bool   `json:"installed"`
	Path            string `json:"path,omitempty"`
}

// reexecUpdate runs the binary that was just installed, so the tools and
// skills it ships (not this process's) are what get synced. Swapped in tests.
var reexecUpdate = func(path string, args []string, e env) (int, error) {
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, e.stdout, e.stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 0, err
	}
	return 0, nil
}

// runUpdate brings everything L80 manages up to date in one command: the
// binary, the pinned tools (fetched now, and a uv-installed copy on PATH
// upgraded), and the skills installed from it. It exits 0 when nothing is
// left to do and ExitActionNeeded when something needs a person, each item
// spelled out in `actions`.
func runUpdate(e env, args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	checkOnly := fs.Bool("check", false, "report what is out of date without changing anything")
	noPrefetch := fs.Bool("no-prefetch", false, "do not download pinned tools now; each downloads on first use")
	resumedFrom := fs.String("resumed-from", "", "set by the binary that just installed this one (internal)")
	if err := fs.Parse(args); err != nil {
		return api.ExitUsage
	}
	opts := syncOpts{apply: !*checkOnly, prefetch: !*noPrefetch}

	if *resumedFrom != "" {
		target, _ := currentExecutable()
		return finishUpdate(e, binaryState{
			Current: *resumedFrom, Latest: Version, UpdateAvailable: true, Installed: true, Path: target,
		}, opts, *asJSON)
	}

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return fail(e, api.Newf("E_UPDATE_FAILED",
			"Build from source with `make install`.",
			"prebuilt binaries are not published for %s/%s", runtime.GOOS, runtime.GOARCH))
	}

	latest, apiErr := updateSource.latestTag()
	if apiErr != nil {
		return fail(e, apiErr)
	}
	bin := binaryState{Current: Version, Latest: strings.TrimPrefix(latest, "v"), UpdateAvailable: newerThan(latest, Version)}
	if *checkOnly || !bin.UpdateAvailable {
		return finishUpdate(e, bin, opts, *asJSON)
	}

	target, err := currentExecutable()
	if err != nil {
		return fail(e, api.Newf("E_UPDATE_FAILED",
			"Reinstall with `curl -fsSL https://www.launch80.com/install.sh | sh`.",
			"could not locate the running binary: %v", err))
	}

	asset := assetName()
	tgz, apiErr := updateSource.fetch(latest, asset)
	if apiErr != nil {
		return fail(e, apiErr)
	}
	sums, apiErr := updateSource.fetch(latest, "checksums.txt")
	if apiErr != nil {
		return fail(e, apiErr)
	}
	want, ok := expectedChecksum(sums, asset)
	if !ok {
		return fail(e, api.Newf("E_UPDATE_FAILED",
			"Retry later; the release may still be publishing.",
			"checksums.txt for %s does not list %s", latest, asset))
	}
	sum := sha256.Sum256(tgz)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fail(e, api.Newf("E_UPDATE_FAILED",
			"Refusing to install. Retry; if it happens again, report it in the launch80 Discord.",
			"checksum mismatch for %s", asset))
	}

	binData, err := extractBinary(tgz)
	if err != nil {
		return fail(e, api.Newf("E_UPDATE_FAILED", "Retry once.", "bad release archive: %v", err))
	}
	if err := replaceExecutable(target, binData); err != nil {
		return fail(e, api.Newf("E_UPDATE_FAILED",
			fmt.Sprintf("Cannot write to %s. Rerun with permission to write there, or reinstall with "+
				"`curl -fsSL https://www.launch80.com/install.sh | sh -s -- --prefix <writable dir>`.", filepath.Dir(target)),
			"could not replace %s: %v", target, err))
	}
	bin.Installed, bin.Path = true, target

	child := []string{"update", "--resumed-from", Version}
	if *asJSON {
		child = append(child, "--json")
	}
	if *noPrefetch {
		child = append(child, "--no-prefetch")
	}
	code, err := reexecUpdate(target, child, e)
	if err == nil {
		return code
	}
	// The new binary is in place but would not start here; report the
	// binary and leave the sync to the next run rather than doing it with
	// this release's stale manifest.
	return emitUpdate(e, bin, syncResult{Actions: []string{
		fmt.Sprintf("Run `L80 update` again to bring tools and skills current; the new binary did not start in this process: %v", err),
	}}, *asJSON, false)
}

// finishUpdate syncs tools and skills and reports the whole state.
func finishUpdate(e env, bin binaryState, opts syncOpts, asJSON bool) int {
	return emitUpdate(e, bin, syncEverything(e, opts), asJSON, !opts.apply)
}

func emitUpdate(e env, bin binaryState, res syncResult, asJSON, checkOnly bool) int {
	actions := res.Actions
	if checkOnly && (bin.UpdateAvailable || res.Pending) {
		actions = append([]string{"Run `L80 update`: it installs what --check found and reports anything left."}, actions...)
	}
	if actions == nil {
		actions = []string{}
	}
	upToDate := len(actions) == 0
	code := api.ExitOK
	if !upToDate && !checkOnly {
		code = api.ExitActionNeeded
	}

	if asJSON {
		emitJSON(e, map[string]any{
			"current": bin.Current, "latest": bin.Latest,
			"update_available": bin.UpdateAvailable, "installed": bin.Installed, "path": bin.Path,
			"tools": res.Tools, "skills": res.Skills, "actions": actions, "up_to_date": upToDate,
		})
		return code
	}

	switch {
	case bin.Installed:
		output.Successf(e.stdout, "updated L80 %s -> %s", bin.Current, bin.Latest)
		output.Detailf(e.stdout, "installed to %s", bin.Path)
	case bin.UpdateAvailable:
		output.Successf(e.stdout, "update available: %s -> %s", bin.Current, bin.Latest)
	default:
		output.Successf(e.stdout, "L80 %s is up to date", bin.Current)
	}
	printSync(e, syncResult{Tools: res.Tools, Skills: res.Skills, Actions: actions})
	if upToDate {
		output.Successf(e.stdout, "everything is current")
	} else if !checkOnly {
		output.Detailf(e.stdout, "%d action(s) left that L80 cannot do itself", len(actions))
	}
	return code
}
