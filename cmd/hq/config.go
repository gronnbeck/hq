package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/gronnbeck/hq/internal/token"
)

// config is the logged-in user's state, kept in the user config directory.
type config struct {
	Server  string `json:"server"`
	Session string `json:"session"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hq", "config.json"), nil
}

func loadConfig() (config, error) {
	var c config
	p, err := configPath()
	if err != nil {
		return c, err
	}
	err = readJSON(p, &c)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	return c, err
}

func saveConfig(c config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	return writeJSON(p, c)
}

// cachedAgentToken is a temporary token kept between `hq report` runs so
// agents don't exchange their access token on every message.
type cachedAgentToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// agentTokenPath keys the cache by server, access token and agent identity,
// so any change to them gets a fresh temporary token.
func agentTokenPath(server, accessToken, agent string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	key := token.Hash(server + "\x00" + accessToken + "\x00" + agent)
	return filepath.Join(dir, "hq", "agent-tokens", key+".json"), nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeJSON writes v readable only by the current user, since it holds tokens.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
