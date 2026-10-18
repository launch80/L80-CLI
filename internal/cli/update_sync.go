package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"

	l80skills "github.com/launch80/L80-CLI"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/output"
	"github.com/launch80/L80-CLI/internal/pytool"
	"github.com/launch80/L80-CLI/internal/skillinstall"
)

// What `L80 update` brings current besides the binary: the pinned tools and
// the skills installed from this binary. Whatever it cannot change itself (an
// override variable set in a shell profile, a missing uv) comes back as an
// action: one exact instruction a person or an agent can carry out.

// toolSync is one tool's row in the update report.
type toolSync struct {
	Name    string   `json:"name"`
	Pinned  string   `json:"pinned"`
	Status  string   `json:"status"` // current | upgraded | stale | missing | failed
	Source  string   `json:"source,omitempty"`
	Version string   `json:"version,omitempty"`
	Notes   []string `json:"notes,omitempty"`
	Fix     string   `json:"fix,omitempty"`
}

// skillSync is one bundled skill's row in the update report.
type skillSync struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	Path   string `json:"path"`
	Status string `json:"status"` // current | updated | outdated | linked | not_installed | failed
	Fix    string `json:"fix,omitempty"`
}

type syncOpts struct {
	// apply is false for --check: report, change nothing, download nothing.
	apply bool
	// prefetch builds each pinned tool now rather than on its first use.
	prefetch bool
}

type syncResult struct {
	Tools  []toolSync
	Skills []skillSync
	// Actions are what is left for someone to do, each one instruction.
	Actions []string
	// Pending is set in --check mode when `L80 update` itself would change
	// something (refresh a skill, upgrade a uv-installed tool).
	Pending bool
}

// Seams: update tests swap these so they never touch the machine they run on.
var (
	syncEverything = defaultSync
	uvToolDir      = defaultUvToolDir
	uvToolInstall  = defaultUvToolInstall
	profileFiles   = defaultProfileFiles
)

func defaultSync(e env, opts syncOpts) syncResult {
	var res syncResult
	upgraded := upgradeUvTools(e, opts, &res)

	rows, err := app.ToolsCheckSelective(nil, func(r pytool.Runner) bool {
		if r.Source == r.Tool.Env {
			return true // an override is used unchecked, so this is the only look at its version
		}
		if r.Pinned() && opts.apply && opts.prefetch {
			fmt.Fprintf(e.stderr, "fetching %s %s (uv caches it; only the first fetch is slow)\n", r.Tool.Name, r.Tool.Ref)
			return true
		}
		return false
	})
	if err != nil {
		res.Actions = append(res.Actions, "Reinstall L80 (`curl -fsSL https://www.launch80.com/install.sh | sh`): its tool manifest is unreadable: "+err.Error())
	}
	for _, r := range rows {
		res.Tools = append(res.Tools, toolRow(r, upgraded[r.Name], &res))
	}
	res.Skills = syncSkills(opts, &res)
	return res
}

// upgradeUvTools upgrades a copy of a pinned tool that `uv tool install` put
// on PATH and that is older than the pin. L80 itself would skip such a copy
// for the uvx bootstrap; upgrading it means the user's own `betterbench`
// matches what L80 runs. Copies installed any other way are left alone.
func upgradeUvTools(e env, opts syncOpts, res *syncResult) map[string]string {
	done := map[string]string{}
	uvDir, uvErr := uvToolDir()
	for _, name := range pytool.Names() {
		t, _ := pytool.Lookup(name)
		if !t.Pinned() || t.MinVersion == "" || len(t.VersionCmd) == 0 {
			continue
		}
		p, err := exec.LookPath(t.Exe)
		if err != nil {
			continue
		}
		v, perr := pytool.ProbeVersion(append([]string{p}, t.VersionCmd...))
		if perr == nil && pytool.Compare(v, t.MinVersion) >= 0 {
			continue
		}
		if uvErr != nil || !within(p, uvDir) {
			continue // not ours to upgrade; the row notes L80 uses the pin instead
		}
		if !opts.apply {
			res.Pending = true
			continue
		}
		fmt.Fprintf(e.stderr, "upgrading %s on PATH (%s) to %s\n", t.Exe, orUnknown(v), t.Ref)
		if err := uvToolInstall(t, e); err != nil {
			res.Actions = append(res.Actions, fmt.Sprintf("Upgrade %s yourself: `uv tool install --force %q` failed: %v", t.Exe, t.Requirement(), err))
			continue
		}
		done[name] = fmt.Sprintf("upgraded %s on PATH from %s to %s", t.Exe, orUnknown(v), t.Ref)
	}
	return done
}

// toolRow turns a resolved tool into its report row, adding an action when
// the tool cannot run at the pinned version.
func toolRow(r app.ToolStatus, upgradedNote string, res *syncResult) toolSync {
	row := toolSync{Name: r.Name, Pinned: r.Pinned, Source: r.Source, Version: r.Version, Notes: r.Notes}
	switch {
	case r.Status == "missing":
		row.Status, row.Fix = "missing", r.Remedy
	case r.Source == r.Override && !runnable(r.Argv):
		row.Status = "missing"
		row.Fix = fmt.Sprintf("%s is set to %q, which is not a command on this machine. Remove it so L80 runs the pinned %s: %s Or point it at an existing %s.",
			r.Override, strings.Join(r.Argv, " "), r.Pinned, removeOverride(r.Override), r.Exe)
	case r.Status == "stale" && r.Source == r.Override:
		row.Status, row.Fix = "stale", overrideFix(r)
	case r.Status == "stale":
		row.Status = "stale"
		row.Fix = fmt.Sprintf("Install uv (https://docs.astral.sh/uv/) so L80 can run the pinned %s; `%s` on PATH is %s, older than %s.",
			r.Pinned, r.Exe, orUnknown(r.Version), r.Minimum)
	case probeFailed(r) && r.Source != r.Override:
		row.Status = "failed"
		row.Fix = fmt.Sprintf("Run `L80 tools prefetch %s` and read its output: the pinned build did not start.", r.Name)
	case upgradedNote != "":
		row.Status = "upgraded"
		row.Notes = append(row.Notes, upgradedNote)
	default:
		row.Status = "current"
	}
	if row.Fix != "" {
		res.Actions = append(res.Actions, r.Name+": "+row.Fix)
	}
	return row
}

func probeFailed(r app.ToolStatus) bool {
	for _, n := range r.Notes {
		if strings.HasPrefix(n, "version probe failed") {
			return true
		}
	}
	return false
}

// overrideFix says how to retire an override variable that points at a tool
// older than the pin, naming the profile lines that set it. L80 cannot edit
// a parent shell's environment, and does not edit profiles on its own.
func overrideFix(r app.ToolStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s runs %s %s, older than the %s this L80 needs. Remove the override and L80 runs the pinned version itself",
		r.Override, r.Exe, orUnknown(r.Version), r.Minimum)
	fmt.Fprintf(&b, ": %s To keep an override, point it at %s %s or later.", removeOverride(r.Override), r.Exe, r.Minimum)
	return b.String()
}

// removeOverride is the instruction for retiring an override variable,
// naming the profile lines that set it when there are any.
func removeOverride(name string) string {
	if locs := findAssignments(name); len(locs) > 0 {
		return fmt.Sprintf("delete or comment out the line that sets it (%s), then run `unset %s` or open a new shell.",
			strings.Join(locs, ", "), name)
	}
	return fmt.Sprintf("no shell profile L80 checked sets it, so it comes from this session or whatever launched it: run `unset %s` there, or open a new shell.", name)
}

// runnable reports whether an override's command exists.
func runnable(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	_, err := exec.LookPath(argv[0])
	return err == nil
}

// findAssignments lists profile lines ("~/.bashrc:129") that set name.
func findAssignments(name string) []string {
	re := regexp.MustCompile(`^\s*(export\s+)?` + regexp.QuoteMeta(name) + `=|^\s*set\s+(-\S+\s+)*` + regexp.QuoteMeta(name) + `\s`)
	home, _ := os.UserHomeDir()
	var locs []string
	for _, p := range profileFiles() {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			if re.MatchString(sc.Text()) {
				shown := p
				if home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
					shown = "~" + strings.TrimPrefix(p, home)
				}
				locs = append(locs, fmt.Sprintf("%s:%d", shown, n))
			}
		}
		f.Close()
	}
	return locs
}

func defaultProfileFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range []string{".bashrc", ".bash_profile", ".profile", ".zshrc", ".zprofile", ".zshenv", ".config/fish/config.fish"} {
		out = append(out, filepath.Join(home, f))
	}
	return out
}

// syncSkills refreshes every bundled skill that is installed as a copy, in
// any harness's skills directory, and differs from the one in this binary. A
// --dev symlink is the user's live source tree and is never touched; a skill
// that is not installed stays so. Claude Code reports uninstalled skills too
// (as optional fixes); other harnesses report only what is there.
func syncSkills(opts syncOpts, res *syncResult) []skillSync {
	var out []skillSync
	for _, t := range skillinstall.Targets {
		for _, name := range l80skills.SkillNames {
			dest, err := t.SkillDir(name)
			if err != nil {
				continue
			}
			row := skillSync{Name: name, Target: t.Name, Path: dest}
			switch skillinstall.Status(name, dest) {
			case skillinstall.NotInstalled:
				if t.Name != "claude-code" {
					continue
				}
				row.Status = "not_installed"
				row.Fix = fmt.Sprintf("optional: `L80 skills link --target %s --name %s`", t.Name, name)
			case skillinstall.Linked:
				row.Status = "linked"
			case skillinstall.Current:
				row.Status = "current"
			default:
				if !opts.apply {
					row.Status = "outdated"
					res.Pending = true
					break
				}
				if err := materialize(name, dest); err != nil {
					row.Status = "failed"
					row.Fix = fmt.Sprintf("Run `L80 skills link --target %s --name %s --force`: %v", t.Name, name, err)
					res.Actions = append(res.Actions, "skill "+row.Fix)
				} else {
					row.Status = "updated"
				}
			}
			out = append(out, row)
		}
	}
	return out
}

func skillMatches(name, dest string) bool { return skillinstall.Matches(name, dest) }

func defaultUvToolDir() (string, error) {
	out, err := exec.Command("uv", "tool", "dir").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func defaultUvToolInstall(t pytool.Tool, e env) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	args := []string{"tool", "install", "--force"}
	if t.Python != "" {
		args = append(args, "--python", t.Python)
	}
	cmd := exec.CommandContext(ctx, "uv", append(args, t.Requirement())...)
	cmd.Stdout, cmd.Stderr = e.stderr, e.stderr
	return cmd.Run()
}

// within reports whether path, after following symlinks, lies under dir.
func within(path, dir string) bool {
	if dir == "" {
		return false
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	rel, err := filepath.Rel(dir, real)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func orUnknown(v string) string {
	if v == "" {
		return "of unknown version"
	}
	return v
}

// printSync writes the human form of the tool and skill rows and the actions.
func printSync(e env, res syncResult) {
	for _, t := range res.Tools {
		line := t.Status
		if t.Version != "" {
			line += ", " + t.Version
		}
		if t.Source != "" {
			line += " via " + t.Source
		}
		output.Successf(e.stdout, "%-12s %s", t.Name, line)
		for _, n := range t.Notes {
			output.Detailf(e.stdout, "%s", n)
		}
	}
	for _, s := range res.Skills {
		if s.Status == "not_installed" {
			continue
		}
		output.Successf(e.stdout, "skill %-16s %s (%s)", s.Name, s.Status, s.Path)
	}
	for _, a := range res.Actions {
		output.Successf(e.stdout, "action: %s", a)
	}
}
