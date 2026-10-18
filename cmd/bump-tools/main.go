// Command bump-tools rewrites internal/pytool/manifest.json so each tool is
// pinned to the commit currently checked out in its local working copy.
//
// It is how a change made in BB-VAST-L80, poisoned-weights-detector or
// ai-command-dashboard reaches users: commit and push there, run
// `make bump-tools`, commit the manifest here, tag an L80 release, and
// `L80 update` delivers it.
//
// Local checkout paths come from tools.local.json at the repo root
// ({"vast": "/path/to/BB-VAST-L80", ...}, gitignored), overridden per tool by
// L80_TOOLS_LOCAL_<NAME>. A tool with no local path is left as it is.
//
// Refuses, per tool, a dirty working tree (unless -allow-dirty) and a commit
// that the public remote does not have: a pin users cannot fetch would break
// every bootstrap. Does not commit.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const manifestPath = "internal/pytool/manifest.json"

func main() {
	only := flag.String("only", "", "bump just this tool")
	allowDirty := flag.Bool("allow-dirty", false, "pin HEAD even if the working tree has uncommitted changes")
	skipRemote := flag.Bool("skip-remote-check", false, "do not confirm the commit exists on the remote (offline use only)")
	dry := flag.Bool("n", false, "show what would change without writing")
	flag.Parse()
	if err := run(*only, *allowDirty, *skipRemote, *dry); err != nil {
		fmt.Fprintln(os.Stderr, "bump-tools:", err)
		os.Exit(1)
	}
}

func run(only string, allowDirty, skipRemote, dry bool) error {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("run from the L80-CLI repo root: %w", err)
	}
	var m struct {
		Schema int                                   `json:"schema"`
		Tools  map[string]map[string]json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	locals := map[string]string{}
	if b, err := os.ReadFile("tools.local.json"); err == nil {
		if err := json.Unmarshal(b, &locals); err != nil {
			return fmt.Errorf("tools.local.json: %w", err)
		}
	}

	names := make([]string, 0, len(m.Tools))
	for n := range m.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	changed := 0
	for _, name := range names {
		if only != "" && name != only {
			continue
		}
		dir := locals[name]
		if v := os.Getenv("L80_TOOLS_LOCAL_" + strings.ToUpper(name)); v != "" {
			dir = v
		}
		if dir == "" {
			fmt.Printf("%-12s no local checkout configured; unchanged\n", name)
			continue
		}
		entry := m.Tools[name]
		var repo, oldCommit, oldRef string
		_ = json.Unmarshal(entry["repo"], &repo)
		_ = json.Unmarshal(entry["commit"], &oldCommit)
		_ = json.Unmarshal(entry["ref"], &oldRef)

		commit, err := git(dir, "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("%s: %s is not a git checkout: %w", name, dir, err)
		}
		if !allowDirty {
			if st, _ := git(dir, "status", "--porcelain", "--untracked-files=no"); st != "" {
				return fmt.Errorf("%s: %s has uncommitted changes; commit and push them first (or -allow-dirty)", name, dir)
			}
		}
		ref, err := git(dir, "describe", "--exact-match", "--tags", "HEAD")
		if err != nil {
			ref, _ = git(dir, "rev-parse", "--abbrev-ref", "HEAD")
		}
		if !skipRemote {
			if err := onRemote(repo, ref, commit); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		if commit == oldCommit && ref == oldRef {
			fmt.Printf("%-12s already pinned to %s (%s)\n", name, ref, commit[:12])
			continue
		}
		fmt.Printf("%-12s %s (%s) -> %s (%s)\n", name, orNone(oldRef), short(oldCommit), ref, commit[:12])
		entry["commit"], _ = json.Marshal(commit)
		entry["ref"], _ = json.Marshal(ref)
		changed++
	}
	if changed == 0 || dry {
		return nil
	}
	return writeManifest(m.Schema, m.Tools)
}

// onRemote confirms the remote advertises ref at commit, or failing that can
// serve the commit itself (GitHub allows fetching any reachable SHA).
func onRemote(repo, ref, commit string) error {
	out, err := git("", "ls-remote", repo)
	if err != nil {
		return fmt.Errorf("cannot reach %s (is it public and pushed?): %w", repo, err)
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == commit && (strings.HasSuffix(f[1], "/"+ref) || strings.HasSuffix(f[1], "/"+ref+"^{}")) {
			return nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, commit) {
			return nil
		}
	}
	return fmt.Errorf("%s is not on %s as %s; push it (and the tag) first", commit[:12], repo, ref)
}

// writeManifest keeps a stable, readable layout: tools sorted by name, fields
// in a fixed order, two-space indent.
func writeManifest(schema int, tools map[string]map[string]json.RawMessage) error {
	order := []string{"repo", "ref", "commit", "package", "exe", "env", "python", "extras", "min_version", "version_cmd", "notes"}
	var b bytes.Buffer
	fmt.Fprintf(&b, "{\n  \"schema\": %d,\n  \"tools\": {\n", schema)
	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		fmt.Fprintf(&b, "    %q: {\n", n)
		var lines []string
		seen := map[string]bool{}
		emit := func(k string, v json.RawMessage) {
			var c bytes.Buffer
			_ = json.Compact(&c, v)
			s := strings.ReplaceAll(c.String(), `","`, `", "`)
			lines = append(lines, fmt.Sprintf("      %q: %s", k, s))
			seen[k] = true
		}
		for _, k := range order {
			if v, ok := tools[n][k]; ok {
				emit(k, v)
			}
		}
		var rest []string
		for k := range tools[n] {
			if !seen[k] {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		for _, k := range rest {
			emit(k, tools[n][k])
		}
		b.WriteString(strings.Join(lines, ",\n") + "\n    }")
		if i < len(names)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  }\n}\n")
	var check any
	if err := json.Unmarshal(b.Bytes(), &check); err != nil {
		return fmt.Errorf("internal: produced invalid JSON: %w", err)
	}
	tmp := manifestPath + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, manifestPath); err != nil {
		return err
	}
	fmt.Printf("wrote %s; review the diff, run `make test`, commit, then tag a release\n", filepath.Clean(manifestPath))
	return nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func short(c string) string {
	if len(c) < 12 {
		return "unpinned"
	}
	return c[:12]
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
