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
}

type SessionConfig struct {
	TmuxTarget string `toml:"tmux_target"`
	CWD        string `toml:"cwd"`
	Name       string `toml:"name"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if _, err := toml.Decode(string(data), &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(&c)
	if err := c.validate(); err != nil {
		return nil, err
	}
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

func (c *Config) validate() error {
	if c.Relay.URL == "" {
		return fmt.Errorf("relay.url is required")
	}
	if c.Session.TmuxTarget == "" {
		return fmt.Errorf("session.tmux_target is required")
	}
	if c.Session.CWD == "" {
		return fmt.Errorf("session.cwd is required")
	}
	return nil
}
