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
	Codex           Codex  `toml:"codex"`
	TUI             TUI    `toml:"tui"`
}

// Codex stores backend-related runtime selection.
type Codex struct {
	Backend       string `toml:"backend"`
	ExecutionMode string `toml:"execution_mode"`
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
	cfg.Codex.Backend = normalizeBackendID(cfg.Codex.Backend)
	cfg.Codex.ExecutionMode = normalizeExecutionMode(cfg.Codex.ExecutionMode)
	if cfg.TUI.ScrollSpeed <= 0 {
		cfg.TUI.ScrollSpeed = 3
	}
	return cfg, nil
}

// Save writes orb.toml to path, or DefaultPath when path is empty.
func Save(path string, cfg Config) error {
	resolvedPath, err := resolvePath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		return fmt.Errorf("create config directory for %q: %w", resolvedPath, err)
	}

	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.ReasoningEffort = strings.TrimSpace(cfg.ReasoningEffort)
	cfg.Codex.Backend = normalizeBackendID(cfg.Codex.Backend)
	cfg.Codex.ExecutionMode = normalizeExecutionMode(cfg.Codex.ExecutionMode)
	if cfg.TUI.ScrollSpeed <= 0 {
		cfg.TUI.ScrollSpeed = 3
	}

	file, err := os.Create(resolvedPath)
	if err != nil {
		return fmt.Errorf("create config file %q: %w", resolvedPath, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if err := toml.NewEncoder(file).Encode(cfg); err != nil {
		return fmt.Errorf("encode config %q: %w", resolvedPath, err)
	}
	return nil
}

// SetBackend updates the configured backend and persists orb.toml.
func SetBackend(path string, backend string) error {
	cfg, err := Load(path)
	if err != nil {
		return fmt.Errorf("load config to set backend: %w", err)
	}
	cfg.Codex.Backend = normalizeBackendID(backend)
	if err := Save(path, cfg); err != nil {
		return fmt.Errorf("save backend selection: %w", err)
	}
	return nil
}

// SetExecutionMode updates the configured Codex execution mode and persists orb.toml.
func SetExecutionMode(path string, mode string) error {
	cfg, err := Load(path)
	if err != nil {
		return fmt.Errorf("load config to set execution mode: %w", err)
	}
	cfg.Codex.ExecutionMode = normalizeExecutionMode(mode)
	if err := Save(path, cfg); err != nil {
		return fmt.Errorf("save execution mode: %w", err)
	}
	return nil
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

func normalizeBackendID(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "claude":
		return "claude"
	default:
		return "codex"
	}
}

func normalizeExecutionMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sandboxed":
		return "sandboxed"
	default:
		return "unblocked"
	}
}
