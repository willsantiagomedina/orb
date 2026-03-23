package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config captures Orb configuration values parsed from orb.toml.
type Config struct {
	APIKey          string `toml:"api_key"`
	Model           string `toml:"model"`
	ReasoningEffort string `toml:"reasoning_effort"`
	TUI             TUI    `toml:"tui"`
}

// TUI contains terminal UI specific settings.
type TUI struct {
	ScrollSpeed int `toml:"scroll_speed"`
}

// DefaultPath returns the canonical orb.toml path in the XDG config directory.
func DefaultPath() (string, error) {
	xdgHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if xdgHome != "" {
		return filepath.Join(xdgHome, "orb", "orb.toml"), nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}

	return filepath.Join(homeDir, ".config", "orb", "orb.toml"), nil
}

// Load reads orb.toml from path, or from DefaultPath when path is empty.
func Load(path string) (Config, error) {
	resolvedPath, err := resolvePath(path)
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read config %q: %w", resolvedPath, err)
	}

	var cfg Config
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config %q: %w", resolvedPath, err)
	}

	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.ReasoningEffort = strings.TrimSpace(cfg.ReasoningEffort)
	if cfg.TUI.ScrollSpeed <= 0 {
		cfg.TUI.ScrollSpeed = 3
	}
	return cfg, nil
}

func resolvePath(path string) (string, error) {
	cleanPath := strings.TrimSpace(path)
	if cleanPath != "" {
		return filepath.Clean(cleanPath), nil
	}

	defaultPath, err := DefaultPath()
	if err != nil {
		return "", fmt.Errorf("resolve default config path: %w", err)
	}

	return defaultPath, nil
}
