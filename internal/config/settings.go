package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Settings are the person's preferences, in ~/.config/L80/settings.json. The
// file is a flat map of dotted keys so new settings need no migration.
type Settings map[string]string

// SettingKey describes one known setting.
type SettingKey struct {
	Key     string
	Default string
	// Env overrides the file when set.
	Env  string
	Help string
	// Check validates a value; nil accepts any.
	Check func(string) error
}

// KeyQualityConfirm is whether L80 asks before a QualityBench run starts.
const KeyQualityConfirm = "qualitybench.confirm"

// Known lists the settings L80 understands.
var Known = []SettingKey{
	{Key: KeyQualityConfirm, Default: "always", Env: "L80_QUALITYBENCH_CONFIRM",
		Help:  "ask before a QualityBench run starts, showing how long it will take: always | over:<duration> (e.g. over:30m) | never",
		Check: func(v string) error { _, err := ParseConfirm(v); return err }},
}

// LookupSetting returns a known setting by key.
func LookupSetting(key string) (SettingKey, bool) {
	for _, k := range Known {
		if k.Key == key {
			return k, true
		}
	}
	return SettingKey{}, false
}

// SettingsPath is ~/.config/L80/settings.json, next to the credentials file.
func SettingsPath() string {
	p := CredentialsPath()
	if p == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "settings.json")
}

// LoadSettings reads the settings file; a missing or unreadable one is empty.
func LoadSettings() Settings {
	s := Settings{}
	if data, err := os.ReadFile(SettingsPath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

// Get resolves a setting: its environment variable, then the file, then the
// default. source says which ("env L80_…", "file", "default").
func Get(key string) (value, source string) {
	k, known := LookupSetting(key)
	if known && k.Env != "" {
		if v := strings.TrimSpace(os.Getenv(k.Env)); v != "" {
			return v, "env " + k.Env
		}
	}
	if v, ok := LoadSettings()[key]; ok && v != "" {
		return v, "file"
	}
	return k.Default, "default"
}

// Set validates and writes one setting. An empty value removes it.
func Set(key, value string) error {
	k, ok := LookupSetting(key)
	if !ok {
		return fmt.Errorf("unknown setting %q (settings: %s)", key, strings.Join(KnownKeys(), ", "))
	}
	value = strings.TrimSpace(value)
	if value != "" && k.Check != nil {
		if err := k.Check(value); err != nil {
			return err
		}
	}
	s := LoadSettings()
	if value == "" {
		delete(s, key)
	} else {
		s[key] = value
	}
	return writeFile(SettingsPath(), s)
}

// KnownKeys lists the setting keys, sorted.
func KnownKeys() []string {
	out := make([]string, 0, len(Known))
	for _, k := range Known {
		out = append(out, k.Key)
	}
	sort.Strings(out)
	return out
}

// writeFile writes settings readable by others: they hold no secrets.
func writeFile(path string, v any) error {
	if path == "" {
		return fmt.Errorf("no config directory")
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Confirm is the parsed qualitybench.confirm setting.
type Confirm struct {
	// Mode is "always", "never" or "over".
	Mode string
	// Over is the threshold for Mode "over": ask only for longer runs.
	Over time.Duration
}

// ParseConfirm reads always | never | over:<duration>. A bare number of
// minutes ("over:30") is accepted too.
func ParseConfirm(v string) (Confirm, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "always", "yes", "true":
		return Confirm{Mode: "always"}, nil
	case "never", "no", "false", "off":
		return Confirm{Mode: "never"}, nil
	}
	if rest, ok := strings.CutPrefix(v, "over:"); ok {
		d, err := time.ParseDuration(rest)
		if err != nil {
			if n, nerr := strconv.ParseFloat(rest, 64); nerr == nil {
				d, err = time.Duration(n*float64(time.Minute)), nil
			}
		}
		if err == nil && d > 0 {
			return Confirm{Mode: "over", Over: d}, nil
		}
	}
	return Confirm{}, fmt.Errorf("%q: want always, never or over:<duration> such as over:30m", v)
}

// Ask reports whether a run estimated at upper minutes needs a yes.
func (c Confirm) Ask(upperMin float64) bool {
	switch c.Mode {
	case "never":
		return false
	case "over":
		return upperMin > c.Over.Minutes()
	}
	return true
}

// String is the setting's value form.
func (c Confirm) String() string {
	if c.Mode == "over" {
		if c.Over%time.Hour == 0 {
			return fmt.Sprintf("over:%dh", int(c.Over.Hours()))
		}
		return fmt.Sprintf("over:%dm", int(c.Over.Round(time.Minute).Minutes()))
	}
	return c.Mode
}

// QualityConfirm resolves qualitybench.confirm; an invalid value falls back
// to always (the safe choice) and is reported in err.
func QualityConfirm() (Confirm, string, error) {
	v, src := Get(KeyQualityConfirm)
	c, err := ParseConfirm(v)
	if err != nil {
		return Confirm{Mode: "always"}, src, err
	}
	return c, src, nil
}
