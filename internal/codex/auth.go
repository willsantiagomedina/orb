package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/willdev/orb/internal/config"
)

// ErrNoAuth is returned when no Codex credential can be resolved.
var ErrNoAuth = errors.New("no codex authentication found")

var (
	resolveMu          sync.RWMutex
	cachedKey          string
	cachedFrom         string
	configuredAuthPath string
)

// Credential identifies an auth key and where it came from.
type Credential struct {
	Key    string
	Source string
}

type codexAuthFile struct {
	AuthMode     string          `json:"auth_mode"`
	OpenAIAPIKey string          `json:"OPENAI_API_KEY"`
	Tokens       codexAuthTokens `json:"tokens"`
}

type codexAuthTokens struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
}

// Resolve returns a Codex credential using the default orb.toml location.
func Resolve() (string, error) {
	return ResolveWithConfigPath(configPathOrDefault(""))
}

// ResolveCandidates returns candidate credentials in priority order.
func ResolveCandidates() ([]Credential, error) {
	return ResolveCandidatesWithConfigPath(configPathOrDefault(""))
}

// SetConfigPath configures the auth/model config path used by Stream and Resolve.
func SetConfigPath(path string) {
	resolveMu.Lock()
	defer resolveMu.Unlock()
	configuredAuthPath = strings.TrimSpace(path)
}

// ResolveWithConfigPath returns a Codex credential with priority order:
// 1. ~/.codex/auth.json CLI session token
// 2. ~/.codex/auth.json OPENAI_API_KEY
// 3. orb.toml api_key
// 4. CODEX_API_KEY environment variable
// 5. OPENAI_API_KEY environment variable
//
// The first successful credential is cached in memory for future calls.
func ResolveWithConfigPath(configPath string) (string, error) {
	candidates, err := ResolveCandidatesWithConfigPath(configPathOrDefault(configPath))
	if err != nil {
		return "", err
	}

	primary := candidates[0]
	cacheCredential(primary.Key, primary.Source, false)
	return primary.Key, nil
}

// ResolveCandidatesWithConfigPath returns candidate credentials in priority order:
// 1. ~/.codex/auth.json CLI session token
// 2. ~/.codex/auth.json OPENAI_API_KEY
// 3. orb.toml api_key
// 4. CODEX_API_KEY environment variable
// 5. OPENAI_API_KEY environment variable
//
// Cached credentials are returned first when available.
func ResolveCandidatesWithConfigPath(configPath string) ([]Credential, error) {
	candidates := make([]Credential, 0, 4)
	resolvedConfigPath := configPathOrDefault(configPath)

	cachedResolvedKey, cachedResolvedFrom := readCachedCredential()
	appendCandidate(&candidates, cachedResolvedKey, cachedResolvedFrom)

	key, source, err := resolveFromCodexSession()
	if err != nil {
		return nil, err
	}
	appendCandidate(&candidates, key, source)

	key, source, err = resolveFromCodexStoredAPIKey()
	if err != nil {
		return nil, err
	}
	appendCandidate(&candidates, key, source)

	key, source, err = resolveFromOrbConfig(resolvedConfigPath)
	if err != nil {
		return nil, err
	}
	appendCandidate(&candidates, key, source)

	envCandidates := resolveFromEnv()
	for _, candidate := range envCandidates {
		appendCandidate(&candidates, candidate.Key, candidate.Source)
	}

	if len(candidates) == 0 {
		return nil, ErrNoAuth
	}
	return candidates, nil
}

// SetCachedCredential overrides the in-memory cached credential.
func SetCachedCredential(key string, source string) {
	cacheCredential(key, source, true)
}

// CachedSource returns where the cached credential was resolved from.
// It returns an empty string when no credential is cached.
func CachedSource() string {
	resolveMu.RLock()
	defer resolveMu.RUnlock()
	return cachedFrom
}

func readCachedCredential() (string, string) {
	resolveMu.RLock()
	defer resolveMu.RUnlock()
	return cachedKey, cachedFrom
}

func configPathOrDefault(path string) string {
	cleanPath := strings.TrimSpace(path)
	if cleanPath != "" {
		return cleanPath
	}
	resolveMu.RLock()
	defer resolveMu.RUnlock()
	return configuredAuthPath
}

func cacheCredential(key string, source string, overwrite bool) {
	key = strings.TrimSpace(key)
	source = strings.TrimSpace(source)
	if key == "" {
		return
	}
	resolveMu.Lock()
	defer resolveMu.Unlock()
	if cachedKey != "" && !overwrite {
		return
	}
	cachedKey = key
	cachedFrom = source
}

func appendCandidate(candidates *[]Credential, key string, source string) {
	key = strings.TrimSpace(key)
	source = strings.TrimSpace(source)
	if key == "" {
		return
	}

	for _, candidate := range *candidates {
		if candidate.Key == key {
			return
		}
	}

	*candidates = append(*candidates, Credential{
		Key:    key,
		Source: source,
	})
}

func resolveFromCodexSession() (string, string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve user home for codex session: %w", err)
	}

	authPath := filepath.Join(homeDir, ".codex", "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("read codex session auth file %q: %w", authPath, err)
	}

	var auth codexAuthFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return "", "", fmt.Errorf("decode codex session auth file %q: %w", authPath, err)
	}

	accessToken := strings.TrimSpace(auth.Tokens.AccessToken)
	if accessToken != "" {
		return accessToken, "codex-cli-session", nil
	}

	idToken := strings.TrimSpace(auth.Tokens.IDToken)
	if idToken != "" {
		return idToken, "codex-cli-session-id-token", nil
	}

	return "", "", nil
}

func resolveFromCodexStoredAPIKey() (string, string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve user home for codex api key: %w", err)
	}

	authPath := filepath.Join(homeDir, ".codex", "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("read codex auth file %q for api key: %w", authPath, err)
	}

	var auth codexAuthFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return "", "", fmt.Errorf("decode codex auth file %q for api key: %w", authPath, err)
	}

	apiKey := strings.TrimSpace(auth.OpenAIAPIKey)
	if apiKey == "" {
		return "", "", nil
	}
	return apiKey, "codex-auth-json:OPENAI_API_KEY", nil
}

func resolveFromOrbConfig(configPath string) (string, string, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", "", fmt.Errorf("load orb config for auth resolution: %w", err)
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return "", "", nil
	}
	return apiKey, "orb-config", nil
}

func resolveFromEnv() []Credential {
	candidates := make([]Credential, 0, 2)

	codexKey := strings.TrimSpace(os.Getenv("CODEX_API_KEY"))
	if codexKey != "" {
		candidates = append(candidates, Credential{
			Key:    codexKey,
			Source: "env:CODEX_API_KEY",
		})
	}

	openAIKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if openAIKey != "" {
		candidates = append(candidates, Credential{
			Key:    openAIKey,
			Source: "env:OPENAI_API_KEY",
		})
	}

	return candidates
}
