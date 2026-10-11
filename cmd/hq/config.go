package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gronnbeck/hq/internal/token"
)

// config holds the user's defaults, read from config.yaml. Flags and
// environment variables override it.
type config struct {
	Server string `yaml:"server"`
	Agent  string `yaml:"agent"`
}

// configTemplate is written the first time hq runs, so there is a file to
// edit that shows what can be set.
const configTemplate = `# Defaults for the hq CLI. Flags and environment variables override these.

# The hq server to talk to. Override with --server or HQ_SERVER.
server: ` + defaultServer + `

# The identity ` + "`hq report`" + ` uses when --as isn't given. Override with HQ_AGENT.
# agent: my-agent
`

// configDir is $XDG_CONFIG_HOME/hq, or ~/.config/hq, on every platform.
func configDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "hq"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "hq"), nil
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// loadConfig reads config.yaml, creating it from the template if it doesn't
// exist yet. Failing to create it isn't an error: the defaults still apply.
func loadConfig() (config, error) {
	c := config{Server: defaultServer}
	p, err := configPath()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		if os.MkdirAll(filepath.Dir(p), 0o700) == nil {
			os.WriteFile(p, []byte(configTemplate), 0o600)
		}
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", p, err)
	}
	return c, nil
}

// session is the logged-in user's session, kept apart from config.yaml so
// the file people edit holds no secrets. It remembers its server so the token
// is never sent anywhere else.
type session struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func sessionPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "session.json"), nil
}

func loadSession() (session, error) {
	var s session
	p, err := sessionPath()
	if err != nil {
		return s, err
	}
	err = readJSON(p, &s)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	return s, err
}

func saveSession(s session) error {
	p, err := sessionPath()
	if err != nil {
		return err
	}
	return writeJSON(p, s)
}

func deleteSession() error {
	p, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
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
