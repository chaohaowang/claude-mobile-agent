package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Relay   RelayConfig   `toml:"relay"`
	Session SessionConfig `toml:"session"`
}

type RelayConfig struct {
	URL                 string `toml:"url"`
	ReconnectInitialSec int    `toml:"reconnect_initial_sec"`
	ReconnectMaxSec     int    `toml:"reconnect_max_sec"`
	PairID              string `toml:"pair_id"`
	DeviceID            string `toml:"device_id"`
}

type SessionConfig struct {
	TmuxTarget string `toml:"tmux_target"`
	CWD        string `toml:"cwd"`
	Name       string `toml:"name"`
}

// Load reads the TOML config file and validates the full set of required fields
// (both [relay] and [session]). Use LoadRelayOnly when the session fields will
// be supplied at runtime (e.g. the `start` subcommand derives them from a dir).
func Load(path string) (*Config, error) {
	c, err := loadRaw(path)
	if err != nil {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// LoadRelayOnly reads the same TOML file but only requires the [relay] fields.
// Returned Config's Session may be empty; callers must fill it in before
// handing to daemon.New.
func LoadRelayOnly(path string) (*Config, error) {
	c, err := loadRaw(path)
	if err != nil {
		return nil, err
	}
	if err := c.validateRelay(); err != nil {
		return nil, err
	}
	return c, nil
}

func loadRaw(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if _, err := toml.Decode(string(data), &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(&c)
	return &c, nil
}

func applyDefaults(c *Config) {
	if c.Relay.ReconnectInitialSec == 0 {
		c.Relay.ReconnectInitialSec = 5
	}
	if c.Relay.ReconnectMaxSec == 0 {
		c.Relay.ReconnectMaxSec = 60
	}
	if c.Session.Name == "" {
		c.Session.Name = "default"
	}
}

func (c *Config) validateRelay() error {
	if c.Relay.URL == "" {
		return fmt.Errorf("relay.url is required")
	}
	if c.Relay.PairID == "" {
		return fmt.Errorf("relay.pair_id is required")
	}
	if c.Relay.DeviceID == "" {
		return fmt.Errorf("relay.device_id is required")
	}
	return nil
}

func (c *Config) validate() error {
	if err := c.validateRelay(); err != nil {
		return err
	}
	// session.cwd is always required — the daemon needs it to find the jsonl.
	// session.tmux_target is optional: empty means view-only (no inbound).
	if c.Session.CWD == "" {
		return fmt.Errorf("session.cwd is required")
	}
	return nil
}
