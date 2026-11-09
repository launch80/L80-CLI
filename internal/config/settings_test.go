package config

import (
	"testing"
	"time"
)

func TestParseConfirm(t *testing.T) {
	for in, want := range map[string]Confirm{
		"always": {Mode: "always"}, "": {Mode: "always"}, "never": {Mode: "never"}, "NO": {Mode: "never"},
		"over:30m": {Mode: "over", Over: 30 * time.Minute}, "over:2h": {Mode: "over", Over: 2 * time.Hour},
		"over:45": {Mode: "over", Over: 45 * time.Minute},
	} {
		got, err := ParseConfirm(in)
		if err != nil || got != want {
			t.Errorf("%q: %+v %v", in, got, err)
		}
	}
	for _, bad := range []string{"sometimes", "over:", "over:-5m", "over:x"} {
		if _, err := ParseConfirm(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if (Confirm{Mode: "over", Over: 30 * time.Minute}).String() != "over:30m" || (Confirm{Mode: "over", Over: 2 * time.Hour}).String() != "over:2h" {
		t.Error("String")
	}
	c := Confirm{Mode: "over", Over: 30 * time.Minute}
	if c.Ask(20) || !c.Ask(31) || (Confirm{Mode: "never"}).Ask(1e6) || !(Confirm{Mode: "always"}).Ask(0) {
		t.Error("Ask")
	}
}

func TestSettingsPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("L80_QUALITYBENCH_CONFIRM", "")
	if v, src := Get(KeyQualityConfirm); v != "always" || src != "default" {
		t.Errorf("%s %s", v, src)
	}
	if err := Set(KeyQualityConfirm, "never"); err != nil {
		t.Fatal(err)
	}
	if v, src := Get(KeyQualityConfirm); v != "never" || src != "file" {
		t.Errorf("%s %s", v, src)
	}
	t.Setenv("L80_QUALITYBENCH_CONFIRM", "over:10m")
	if c, src, err := QualityConfirm(); err != nil || c.Mode != "over" || src != "env L80_QUALITYBENCH_CONFIRM" {
		t.Errorf("%+v %s %v", c, src, err)
	}
	t.Setenv("L80_QUALITYBENCH_CONFIRM", "bogus")
	if c, _, err := QualityConfirm(); err == nil || c.Mode != "always" {
		t.Error("a bad value must fall back to always")
	}
	if Set("nope", "x") == nil || Set(KeyQualityConfirm, "bogus") == nil {
		t.Error("bad Set accepted")
	}
	if err := Set(KeyQualityConfirm, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadSettings()[KeyQualityConfirm]; ok {
		t.Error("unset kept the key")
	}
}
