package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// OnboardVersion is bumped when onboarding gains a step worth showing again
// to people who finished an older one.
const OnboardVersion = 1

// State is what the console remembers between runs. It holds no secrets.
type State struct {
	OnboardedAt    string `json:"onboarded_at,omitempty"`
	OnboardVersion int    `json:"onboard_version,omitempty"`
	// OnboardSkipped is true when the person skipped rather than finished.
	OnboardSkipped bool `json:"onboard_skipped,omitempty"`
}

// NeedsOnboarding reports whether the console should open on onboarding.
func (s State) NeedsOnboarding() bool { return s.OnboardVersion < OnboardVersion }

// StatePath is ~/.config/L80/state.json, next to the credentials file.
func StatePath() string {
	p := CredentialsPath()
	if p == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "state.json")
}

// LoadState reads the state file; a missing or unreadable one is the zero State.
func LoadState() State {
	var s State
	if data, err := os.ReadFile(StatePath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

// MarkOnboarded records that onboarding was finished or skipped.
func MarkOnboarded(skipped bool) error {
	s := LoadState()
	s.OnboardedAt = time.Now().UTC().Format(time.RFC3339)
	s.OnboardVersion = OnboardVersion
	s.OnboardSkipped = skipped
	return writePrivate(StatePath(), s)
}

// SaveCredentials writes the publish token to the credentials file Load reads.
func SaveCredentials(token string) error {
	if token == "" {
		return errors.New("empty token")
	}
	return writePrivate(CredentialsPath(), credentials{KeyID: KeyIDOf(token), Token: token})
}

// writePrivate writes v as JSON, readable only by this user, replacing the
// file atomically so a crash never leaves half a credential behind.
func writePrivate(path string, v any) error {
	if path == "" {
		return errors.New("no config directory")
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
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
