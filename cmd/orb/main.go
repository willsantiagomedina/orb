package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/willsantiagomedina/orb/internal/codex"
	"github.com/willsantiagomedina/orb/internal/config"
	"github.com/willsantiagomedina/orb/internal/store"
	"github.com/willsantiagomedina/orb/internal/tui"
)

var version = "0.1.0-dev"

func main() {
	cfgPath := flag.String("config", "", "path to orb.toml configuration file")
	showVersion := flag.Bool("version", false, "print Orb version")
	flag.Parse()

	if *showVersion {
		_, _ = fmt.Fprintf(os.Stdout, "orb %s\n", version)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		exitWithError("load config", err)
	}
	codex.SetConfigPath(*cfgPath)

	authStatus, err := resolveAuthStatus(*cfgPath)
	if err != nil {
		exitWithError("resolve auth", err)
	}

	taskStore, err := store.OpenDefault(context.Background())
	if err != nil {
		exitWithError("open task store", err)
	}
	defer func() {
		if closeErr := taskStore.Close(); closeErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "orb: close task store: %v\n", closeErr)
		}
	}()

	workingDir, err := os.Getwd()
	if err != nil {
		exitWithError("resolve working directory", err)
	}

	program := tea.NewProgram(
		tui.New(version, authStatus, taskStore, workingDir, *cfgPath, cfg.TUI.ScrollSpeed),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := program.Run(); err != nil {
		exitWithError("start tui", err)
	}
}

func resolveAuthStatus(configPath string) (string, error) {
	credentials, err := codex.ResolveCandidatesWithConfigPath(configPath)
	if errors.Is(err, codex.ErrNoAuth) {
		return "● no auth", nil
	}
	if err != nil {
		return "", err
	}

	for _, credential := range credentials {
		source := strings.TrimSpace(credential.Source)
		if source == "orb-config" || source == "codex-auth-json:OPENAI_API_KEY" || strings.HasPrefix(source, "env:") {
			return "● codex connected", nil
		}
	}
	return "● session auth", nil
}

func exitWithError(operation string, err error) {
	_, _ = fmt.Fprintf(os.Stderr, "orb: %s: %v\n", operation, err)
	os.Exit(1)
}
