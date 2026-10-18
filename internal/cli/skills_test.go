package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	l80skills "github.com/launch80/L80-CLI"
)

func runSkillsCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runSkills(env{stdout: &out, stderr: &errOut}, args)
	return code, out.String(), errOut.String()
}

// isolateHome points every harness's skills directory into a temp home.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	return home
}

func TestSkillsLinkTargetsAndAliases(t *testing.T) {
	home := isolateHome(t)
	if code, _, errOut := runSkillsCmd(t, "link", "--target", "codex,gemini"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, p := range []string{".agents/skills/l80/SKILL.md", ".gemini/skills/l80/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(home, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	// A second install refuses without --force.
	if code, _, errOut := runSkillsCmd(t, "link", "--target", "cursor"); code == 0 || !strings.Contains(errOut, "--force") {
		t.Errorf("reinstall: exit %d %s", code, errOut)
	}
	if code, _, _ := runSkillsCmd(t, "link", "--target", "nope"); code == 0 {
		t.Error("unknown target accepted")
	}
}

func TestSkillsLinkAllIntoDir(t *testing.T) {
	isolateHome(t)
	dir := filepath.Join(t.TempDir(), "proj", ".claude", "skills")
	if code, _, errOut := runSkillsCmd(t, "link", "--dir", dir, "--all"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, n := range l80skills.SkillNames {
		if !skillMatches(n, filepath.Join(dir, n)) {
			t.Errorf("%s not installed", n)
		}
	}
}

func TestSkillsLinkDetected(t *testing.T) {
	home := isolateHome(t)
	if code, _, _ := runSkillsCmd(t, "link", "--target", "detected"); code == 0 {
		t.Error("no harness installed, but detected succeeded")
	}
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	if code, _, errOut := runSkillsCmd(t, "link", "--target", "detected"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, p := range []string{".claude/skills/l80", ".agents/skills/l80"} {
		if _, err := os.Stat(filepath.Join(home, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini")); err == nil {
		t.Error("installed into a harness that is not there")
	}

	code, out, _ := runSkillsCmd(t, "targets", "--json")
	var rows []targetRow
	if code != 0 || json.Unmarshal([]byte(out), &rows) != nil {
		t.Fatalf("targets: exit %d %s", code, out)
	}
	got := map[string]targetRow{}
	for _, r := range rows {
		got[r.Target] = r
	}
	if !got["claude-code"].Detected || got["claude-code"].Skills["l80"] != "current" || got["gemini"].Detected {
		t.Errorf("targets = %+v", rows)
	}
}

func TestSkillsAgentsMD(t *testing.T) {
	code, out, _ := runSkillsCmd(t, "agents-md")
	if code != 0 || !strings.Contains(out, "L80 help") || !strings.Contains(out, "consent") {
		t.Errorf("agents-md: %d %s", code, out)
	}
}
