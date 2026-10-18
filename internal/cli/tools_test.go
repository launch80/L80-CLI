package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runToolsCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runTools(env{stdout: &out, stderr: &errOut}, args)
	return code, out.String(), errOut.String()
}

func TestToolsListShowsPins(t *testing.T) {
	code, out, _ := runToolsCmd(t, "list", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, row := range got.Tools {
		names[row["name"].(string)] = true
		if row["name"] == "betterbench" && (row["ref"] != "v0.6.1" || row["pinned"] != true) {
			t.Errorf("betterbench row = %v", row)
		}
	}
	for _, n := range []string{"betterbench", "vast", "pwdet", "dashboard"} {
		if !names[n] {
			t.Errorf("list lacks %s", n)
		}
	}
}

func TestToolsCheckAndPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "betterbench"), []byte("#!/bin/sh\necho 'BetterBench 0.6.1'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("L80_BETTERBENCH", "")
	code, out, _ := runToolsCmd(t, "check", "betterbench")
	if code != 0 || !strings.Contains(out, "betterbench  ok via PATH, version 0.6.1") {
		t.Errorf("check: exit %d\n%s", code, out)
	}
	code, out, _ = runToolsCmd(t, "path", "betterbench")
	if code != 0 || strings.TrimSpace(out) != filepath.Join(dir, "betterbench") {
		t.Errorf("path: exit %d %q", code, out)
	}
	// Unpinned and absent: exit 11 with a remedy naming the override.
	t.Setenv("L80_PWDET", "")
	code, out, _ = runToolsCmd(t, "check", "pwdet")
	if code != 11 || !strings.Contains(out, "L80_PWDET") {
		t.Errorf("missing pwdet: exit %d\n%s", code, out)
	}
	if code, _, errOut := runToolsCmd(t, "check", "nope"); code != 2 || !strings.Contains(errOut, "unknown tool") {
		t.Errorf("unknown tool: exit %d %s", code, errOut)
	}
}
