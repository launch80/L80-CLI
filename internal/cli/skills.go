package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	l80skills "github.com/launch80/L80-CLI"
	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/output"
	"github.com/launch80/L80-CLI/internal/skillinstall"
)

// defaultSkill is the umbrella skill: it teaches an agent the whole command
// surface and points at the detailed skills for publishing, renting and scanning.
const defaultSkill = "l80"

func runSkills(e env, args []string) int {
	if len(args) == 0 {
		return fail(e, api.Newf("E_USAGE",
			"Run `L80 skills targets`, then `L80 skills link --target detected --all`.",
			"skills needs a subcommand"))
	}

	switch args[0] {
	case "print":
		return runSkillsPrint(e, args[1:])
	case "link", "install":
		return runSkillsLink(e, args[1:])
	case "targets":
		return runSkillsTargets(e, args[1:])
	case "agents-md":
		fmt.Fprint(e.stdout, agentsMD)
		return api.ExitOK
	default:
		return fail(e, api.Newf("E_USAGE",
			"Use `print`, `link`, `targets` or `agents-md`.", "unknown skills subcommand %q", args[0]))
	}
}

func runSkillsPrint(e env, args []string) int {
	name := defaultSkill
	if len(args) > 0 {
		name = args[0]
	}

	data, err := l80skills.FS.ReadFile(filepath.Join("skills", name, "SKILL.md"))
	if err != nil {
		return fail(e, api.Newf("E_INPUT_INVALID",
			fmt.Sprintf("Available skills: %v", l80skills.SkillNames),
			"no bundled skill named %q", name))
	}

	e.stdout.Write(data)
	return api.ExitOK
}

// installDest is one place a skill is going.
type installDest struct {
	target string // a target name, or "dir"
	dir    string // the skills directory
}

func runSkillsLink(e env, args []string) int {
	fs2 := flag.NewFlagSet("skills link", flag.ContinueOnError)
	fs2.SetOutput(e.stderr)
	target := fs2.String("target", "", "where to install: "+strings.Join(skillinstall.Names(), ", ")+" (comma-separated)")
	dir := fs2.String("dir", "", "install into this skills directory instead (e.g. .claude/skills in a repo)")
	name := fs2.String("name", defaultSkill, "skill to install")
	all := fs2.Bool("all", false, "install every bundled skill")
	from := fs2.String("from", "", "source skills/ directory (implies --dev)")
	dev := fs2.Bool("dev", false, "symlink the source tree so edits are live")
	force := fs2.Bool("force", false, "replace an existing installation")

	if err := fs2.Parse(args); err != nil {
		return api.ExitUsage
	}

	var dests []installDest
	if *dir != "" {
		abs, err := filepath.Abs(*dir)
		if err != nil {
			return fail(e, api.Newf("E_INPUT_INVALID", "Pass a directory path.", "%v", err))
		}
		dests = append(dests, installDest{target: "dir", dir: abs})
	}
	for _, tn := range strings.Split(*target, ",") {
		tn = strings.TrimSpace(tn)
		switch {
		case tn == "":
		case tn == "detected":
			found := skillinstall.Detected()
			if len(found) == 0 {
				return fail(e, api.Newf("E_USAGE",
					"Name one with --target (e.g. --target agents), or use --dir.",
					"no agent harness found in your home directory"))
			}
			for _, t := range found {
				d, err := t.Dir()
				if err != nil {
					return fail(e, api.Newf("E_INTERNAL", "Pass --dir explicitly.", "%v", err))
				}
				dests = append(dests, installDest{target: t.Name, dir: d})
			}
		default:
			t, ok := skillinstall.Lookup(tn)
			if !ok {
				return fail(e, api.Newf("E_USAGE",
					"Pass --target "+strings.Join(skillinstall.Names(), "|")+", or --dir PATH.",
					"unknown --target %q", tn))
			}
			d, err := t.Dir()
			if err != nil {
				return fail(e, api.Newf("E_INTERNAL", "Set CLAUDE_CONFIG_DIR or pass --dir explicitly.", "%v", err))
			}
			dests = append(dests, installDest{target: t.Name, dir: d})
		}
	}
	if len(dests) == 0 {
		return fail(e, api.Newf("E_USAGE",
			"Pass --target "+strings.Join(skillinstall.Names(), "|")+", or --dir PATH. `L80 skills targets` lists them.",
			"missing --target"))
	}

	names := []string{*name}
	if *all {
		names = l80skills.SkillNames
	} else if !skillinstall.Bundled(*name) {
		return fail(e, api.Newf("E_INPUT_INVALID",
			fmt.Sprintf("Available skills: %v", l80skills.SkillNames), "no bundled skill named %q", *name))
	}

	for _, d := range dests {
		for _, n := range names {
			dest := filepath.Join(d.dir, n)
			o := skillinstall.Opts{Force: *force}
			if *dev || *from != "" {
				src, err := devSource(*from, n)
				if err != nil {
					return fail(e, api.Newf("E_INPUT_INVALID",
						"Pass --from pointing at the repo's skills/ directory.", "%v", err))
				}
				o.DevSource = src
			}
			if err := skillinstall.Install(n, dest, o); err != nil {
				if errors.Is(err, skillinstall.ErrExists) {
					return fail(e, api.Newf("E_USAGE", "Re-run with --force to replace it.", "%v", err))
				}
				return fail(e, api.Newf("E_INTERNAL", "Check permissions.", "%v", err))
			}
			if o.DevSource != "" {
				output.Successf(e.stdout, "linked %s -> %s", dest, o.DevSource)
			} else {
				output.Successf(e.stdout, "installed %s", dest)
			}
		}
	}

	output.Detailf(e.stdout, "Agents pick up new skills on their next session start.")
	return api.ExitOK
}

// targetRow is one harness in `L80 skills targets`.
type targetRow struct {
	Target   string            `json:"target"`
	Harness  string            `json:"harness"`
	Detected bool              `json:"detected"`
	Dir      string            `json:"dir"`
	Skills   map[string]string `json:"skills"` // name -> current | outdated | linked | not_installed
}

func runSkillsTargets(e env, args []string) int {
	fs2 := flag.NewFlagSet("skills targets", flag.ContinueOnError)
	fs2.SetOutput(e.stderr)
	asJSON := fs2.Bool("json", false, "emit machine-readable JSON")
	if err := fs2.Parse(args); err != nil {
		return api.ExitUsage
	}

	var rows []targetRow
	for _, t := range skillinstall.Targets {
		d, err := t.Dir()
		if err != nil {
			continue
		}
		row := targetRow{Target: t.Name, Harness: t.Harness, Detected: t.Detected(), Dir: d, Skills: map[string]string{}}
		for _, n := range l80skills.SkillNames {
			row.Skills[n] = string(skillinstall.Status(n, filepath.Join(d, n)))
		}
		rows = append(rows, row)
	}

	if *asJSON {
		enc := json.NewEncoder(e.stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rows)
		return api.ExitOK
	}
	for _, r := range rows {
		mark := "not found"
		if r.Detected {
			mark = "detected"
		}
		output.Successf(e.stdout, "%-12s %-9s %s", r.Target, mark, r.Dir)
		output.Detailf(e.stdout, "%s", r.Harness)
		for _, n := range l80skills.SkillNames {
			if s := r.Skills[n]; s != string(skillinstall.NotInstalled) {
				output.Detailf(e.stdout, "%s: %s", n, s)
			}
		}
	}
	output.Detailf(e.stdout, "Install everywhere detected: `L80 skills link --target detected --all`.")
	output.Detailf(e.stdout, "No skills support? Paste `L80 skills agents-md` into AGENTS.md or CLAUDE.md.")
	return api.ExitOK
}

// claudeSkillDir resolves ~/.claude/skills/<name>, honouring CLAUDE_CONFIG_DIR.
func claudeSkillDir(name string) (string, error) {
	t, _ := skillinstall.Lookup("claude-code")
	return t.SkillDir(name)
}

func devSource(from, name string) (string, error) {
	if from != "" {
		return filepath.Abs(filepath.Join(from, name))
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(wd, "skills", name)
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("no skills/%s under %s", name, wd)
	}
	return candidate, nil
}

func materialize(name, dest string) error { return skillinstall.Materialize(name, dest) }

// agentsMD is for harnesses that read a project instruction file but not
// skills: enough for an agent to discover the rest from the CLI itself.
const agentsMD = `## L80 (launch80 CLI)

This machine has ` + "`L80`" + `, the launch80 CLI: benchmark LLM endpoints (BetterBench),
rent a Vast.ai GPU to benchmark on, scan model weights for tampering, watch GPU
telemetry, and publish results as share pages on launch80.com.

- Run ` + "`L80 help`" + ` for the command list; ` + "`L80 skills print l80`" + ` for the full agent guide.
- Prefer ` + "`--json`" + ` where a command takes it; errors print ` + "`error: E_CODE`" + ` and a ` + "`remedy:`" + ` line.
- Tools are free. Publishing needs a token the user configures; never ask them to paste one into chat.
- ` + "`L80 vast rent|serve|run`" + ` bills the user's Vast.ai account: get explicit consent first,
  and always tear down (` + "`L80 vast destroy`" + `) when done.
- Published pages are public. Confirm before publishing.
`
