// Package host owns the per-Mac configuration: a stable host_id used as
// the relay's pair key, plus the relay WS URL and the public HTTP URL
// the `pair` subcommand bakes into the QR.
package host

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Default URLs match the deployed cmrelay. Override by editing host.toml.
const (
	defaultRelayURL  = "ws://47.79.84.115:8443/ws"
	defaultPublicURL = "http://47.79.84.115:8443"
)

// Config is the on-disk shape of host.toml.
type Config struct {
	HostID    string `toml:"host_id"`
	RelayURL  string `toml:"relay_url"`
	PublicURL string `toml:"public_url"`
}

// LoadOrGenerate reads path; if missing, generates a fresh host_id and
// writes the file with mode 0600. Always fills RelayURL/PublicURL with
// defaults when the loaded file omits them.
func LoadOrGenerate(path string) (Config, error) {
	var cfg Config
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return cfg, fmt.Errorf("stat %s: %w", path, err)
	}

	if cfg.HostID == "" {
		id, err := generateHostID()
		if err != nil {
			return cfg, err
		}
		cfg.HostID = id
	}
	if cfg.RelayURL == "" {
		cfg.RelayURL = defaultRelayURL
	}
	if cfg.PublicURL == "" {
		cfg.PublicURL = defaultPublicURL
	}

	return cfg, save(path, cfg)
}

func generateHostID() (string, error) {
	var buf [6]byte // 12 hex chars
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return "host-" + hex.EncodeToString(buf[:]), nil
}

func save(path string, cfg Config) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return nil
}
