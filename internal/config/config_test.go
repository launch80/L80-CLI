package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBaseURLPrecedence(t *testing.T) {
	t.Setenv("L80_TOKEN", "L80_x_y")

	t.Setenv("L80_API_BASE", "")
	if got := Load("").BaseURL; got != DefaultBaseURL {
		t.Errorf("default = %q, want %q", got, DefaultBaseURL)
	}

	t.Setenv("L80_API_BASE", "http://localhost:3007")
	if got := Load("").BaseURL; got != "http://localhost:3007" {
		t.Errorf("env = %q", got)
	}

	// The flag beats the environment.
	if got := Load("https://staging.example").BaseURL; got != "https://staging.example" {
		t.Errorf("flag = %q", got)
	}
}

func TestBaseURLTrailingSlashTrimmed(t *testing.T) {
	if got := Load("http://localhost:3007/").BaseURL; got != "http://localhost:3007" {
		t.Errorf("got %q, want no trailing slash", got)
	}
}

func TestTokenFromEnvBeatsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeCreds(t, dir, `{"key_id":"filekey","token":"L80_filekey_secretfromfile"}`)

	t.Setenv("L80_TOKEN", "L80_envkey_secretfromenv")
	cfg := Load("")

	if cfg.Token != "L80_envkey_secretfromenv" {
		t.Errorf("token = %q, want the env value", cfg.Token)
	}
	if cfg.KeyID != "envkey" {
		t.Errorf("key id = %q, want envkey", cfg.KeyID)
	}
	if cfg.TokenSource != SourceEnv {
		t.Errorf("source = %q", cfg.TokenSource)
	}
}

func TestTokenFallsBackToCredentialsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("L80_TOKEN", "")
	writeCreds(t, dir, `{"key_id":"filekey","token":"L80_filekey_secretfromfile"}`)

	cfg := Load("")
	if cfg.KeyID != "filekey" || cfg.TokenSource != SourceFile {
		t.Errorf("got key=%q source=%q", cfg.KeyID, cfg.TokenSource)
	}
	if !cfg.HasToken() {
		t.Error("HasToken() = false, want true")
	}
}

func TestMissingTokenIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("L80_TOKEN", "")

	cfg := Load("")
	if cfg.HasToken() {
		t.Error("HasToken() = true with no credential")
	}
	if cfg.TokenSource != SourceNone {
		t.Errorf("source = %q, want %q", cfg.TokenSource, SourceNone)
	}
	if cfg.BaseURL == "" {
		t.Error("base URL should still resolve without a token")
	}
}

func TestKeyIDOfNeverLeaksTheSecret(t *testing.T) {
	if got := KeyIDOf("L80_example-fake-for-test-only_supersecretvalue"); got != "example-fake-for-test-only" {
		t.Errorf("got %q, want example-fake-for-test-only", got)
	}
	for _, bad := range []string{"", "garbage", "bearer_x_y", "L80_only"} {
		if got := KeyIDOf(bad); got != "" {
			t.Errorf("KeyIDOf(%q) = %q, want empty", bad, got)
		}
	}
}

func writeCreds(t *testing.T, dir, contents string) {
	t.Helper()
	path := filepath.Join(dir, "L80")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "credentials.json"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOnboardingStateAndSavedToken(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("L80_TOKEN", "")
	if !LoadState().NeedsOnboarding() {
		t.Fatal("a fresh machine needs onboarding")
	}
	if err := MarkOnboarded(true); err != nil {
		t.Fatal(err)
	}
	if s := LoadState(); s.NeedsOnboarding() || !s.OnboardSkipped {
		t.Fatalf("state = %+v", s)
	}

	if err := SaveCredentials("L80_k9_secret"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(CredentialsPath())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials file: %v %v", info, err)
	}
	if c := Load(""); c.Token != "L80_k9_secret" || c.KeyID != "k9" || c.TokenSource != SourceFile {
		t.Errorf("loaded = %+v", c)
	}
}

func TestApexBaseURLGoesToWWW(t *testing.T) {
	t.Setenv("L80_API_BASE", "https://launch80.com/")
	if got := Load("").BaseURL; got != "https://www.launch80.com" {
		t.Errorf("apex = %q", got)
	}
	if got := Load("http://localhost:3007").BaseURL; got != "http://localhost:3007" {
		t.Errorf("other hosts are left alone: %q", got)
	}
}
