// Package config resolves the CLI's endpoint and credential.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const DefaultBaseURL = "https://www.launch80.com"

const apexBaseURL = "https://launch80.com"

// TokenSource records where the credential came from, so `L80 auth status` and
// `L80 doctor` can explain the resolution without ever printing the secret.
type TokenSource string

const (
	SourceEnv  TokenSource = "L80_TOKEN environment variable"
	SourceFile TokenSource = "credentials file"
	SourceNone TokenSource = "not found"
)

type Config struct {
	BaseURL     string
	Token       string
	KeyID       string
	TokenSource TokenSource
	CredPath    string
}

type credentials struct {
	KeyID string `json:"key_id"`
	Token string `json:"token"`
}

// CredentialsPath is ~/.config/L80/credentials.json, honouring XDG_CONFIG_HOME.
func CredentialsPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "L80", "credentials.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "L80", "credentials.json")
}

// Load resolves configuration in a documented precedence order:
//
//	base URL: --api-base flag > L80_API_BASE > https://www.launch80.com
//	token:    L80_TOKEN > ~/.config/L80/credentials.json > none
//
// A missing token is NOT an error here. Only commands that publish require one,
// so `L80 templates list` and `L80 --version` work with no credential at all.
func Load(apiBaseFlag string) Config {
	c := Config{BaseURL: DefaultBaseURL, TokenSource: SourceNone, CredPath: CredentialsPath()}

	if v := os.Getenv("L80_API_BASE"); v != "" {
		c.BaseURL = v
	}
	if apiBaseFlag != "" {
		c.BaseURL = apiBaseFlag
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	// The apex answers every request with a 307 to www. Go follows it but
	// drops Authorization on the change of host, so an authenticated call
	// would arrive without its token: go to www directly instead.
	if c.BaseURL == apexBaseURL {
		c.BaseURL = DefaultBaseURL
	}

	if v := os.Getenv("L80_TOKEN"); v != "" {
		c.Token = v
		c.TokenSource = SourceEnv
	} else if creds, err := readCredentials(c.CredPath); err == nil {
		c.Token = creds.Token
		c.KeyID = creds.KeyID
		c.TokenSource = SourceFile
	}

	if c.KeyID == "" {
		c.KeyID = KeyIDOf(c.Token)
	}
	return c
}

func readCredentials(path string) (credentials, error) {
	var creds credentials
	if path == "" {
		return creds, errors.New("no credentials path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return creds, err
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return creds, err
	}
	if creds.Token == "" {
		return creds, errors.New("credentials file has no token")
	}
	return creds, nil
}

// KeyIDOf extracts the key id from a token shaped L80_<keyid>_<secret>.
// It never returns the secret.
func KeyIDOf(token string) string {
	parts := strings.Split(token, "_")
	if len(parts) < 3 || parts[0] != "L80" {
		return ""
	}
	return parts[1]
}

// HasToken reports whether a credential was resolved.
func (c Config) HasToken() bool { return c.Token != "" }
