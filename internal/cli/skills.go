package cli

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	l80skills "github.com/launch80/L80-CLI"
	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/output"
)

const defaultSkill = "l80-test-report"

// validTargets is a map, not a bool, so adding cursor/mcp later is one entry --
// the flag shape does not change and no existing invocation breaks.
var validTargets = map[string]bool{"claude-code": true}

func runSkills(e env, args []string) int {
	if len(args) == 0 {
		return fail(e, api.Newf("E_USAGE",
			"Run `L80 skills print` or `L80 skills link --target claude-code`.",
			"skills needs a subcommand"))
	}

	switch args[0] {
	case "print":
		return runSkillsPrint(e, args[1:])
	case "link":
		return runSkillsLink(e, args[1:])
	default:
		return fail(e, api.Newf("E_USAGE",
			"Use `print` or `link`.", "unknown skills subcommand %q", args[0]))
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

func runSkillsLink(e env, args []string) int {
	fs2 := flag.NewFlagSet("skills link", flag.ContinueOnError)
	fs2.SetOutput(e.stderr)
	target := fs2.String("target", "", "agent to install for (claude-code)")
	name := fs2.String("name", defaultSkill, "skill to install")
	from := fs2.String("from", "", "source skills/ directory (implies --dev)")
	dev := fs2.Bool("dev", false, "symlink the source tree so edits are live")
	force := fs2.Bool("force", false, "replace an existing installation")

	if err := fs2.Parse(args); err != nil {
		return api.ExitUsage
	}

	if !validTargets[*target] {
		return fail(e, api.Newf("E_USAGE",
			"Pass --target claude-code.",
			"unknown or missing --target %q", *target))
	}

	dest, err := claudeSkillDir(*name)
	if err != nil {
		return fail(e, api.Newf("E_INTERNAL", "Set CLAUDE_CONFIG_DIR explicitly.", "%v", err))
	}

	if info, statErr := os.Lstat(dest); statErr == nil {
		kind := "directory"
		if info.Mode()&os.ModeSymlink != 0 {
			kind = "symlink"
			if link, e2 := os.Readlink(dest); e2 == nil {
				kind = "symlink -> " + link
			}
		}
		if !*force {
			return fail(e, api.Newf("E_USAGE",
				"Re-run with --force to replace it.",
				"%s already exists (%s)", dest, kind))
		}
		if rmErr := os.RemoveAll(dest); rmErr != nil {
			return fail(e, api.Newf("E_INTERNAL", "Remove it manually and retry.",
				"could not replace %s: %v", dest, rmErr))
		}
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fail(e, api.Newf("E_INTERNAL", "Check directory permissions.", "%v", err))
	}

	if *dev || *from != "" {
		src, resolveErr := devSource(*from, *name)
		if resolveErr != nil {
			return fail(e, api.Newf("E_INPUT_INVALID",
				"Pass --from pointing at the repo's skills/ directory.", "%v", resolveErr))
		}
		if err := os.Symlink(src, dest); err != nil {
			return fail(e, api.Newf("E_INTERNAL", "Check permissions.", "could not symlink: %v", err))
		}
		output.Successf(e.stdout, "linked %s -> %s", dest, src)
	} else {
		if err := materialize(*name, dest); err != nil {
			return fail(e, api.Newf("E_INTERNAL", "Check permissions.", "%v", err))
		}
		output.Successf(e.stdout, "installed %s", dest)
	}

	output.Detailf(e.stdout, "Claude Code picks up new skills on the next session start.")
	return api.ExitOK
}

// claudeSkillDir resolves ~/.claude/skills/<name>, honouring CLAUDE_CONFIG_DIR.
func claudeSkillDir(name string) (string, error) {
	root := os.Getenv("CLAUDE_CONFIG_DIR")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".claude")
	}
	return filepath.Join(root, "skills", name), nil
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

// materialize copies the embedded skill out of the binary, so a downloaded L80
// with no source tree installs exactly the same content.
func materialize(name, dest string) error {
	root := filepath.Join("skills", name)

	return fs.WalkDir(l80skills.FS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out := filepath.Join(dest, rel)

		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, readErr := l80skills.FS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
}
