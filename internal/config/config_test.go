package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad_DefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte(`
[relay]
url = "ws://localhost:9999/ws"

[session]
tmux_target = "claude-mobile:0"
cwd = "/tmp"
`), 0644)
	assert.NoError(t, err)

	cfg, err := Load(path)
	assert.NoError(t, err)
	assert.Equal(t, "ws://localhost:9999/ws", cfg.Relay.URL)
	assert.Equal(t, "claude-mobile:0", cfg.Session.TmuxTarget)
	assert.Equal(t, "/tmp", cfg.Session.CWD)
	assert.Equal(t, 5, cfg.Relay.ReconnectInitialSec)
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path.toml")
	assert.Error(t, err)
}

func TestLoad_MissingRequiredField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`[relay]`), 0644)
	_, err := Load(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "relay.url")
}
