package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "vix"
	keyringUser    = "anthropic-api-key"
)

// KeySource describes where the API key was found.
type KeySource string

const (
	KeySourceEnv      KeySource = "env"
	KeySourceKeychain KeySource = "keychain"
	KeySourceEnvFile  KeySource = "dotenv"
	KeySourceNone     KeySource = "none"
)

// ResolveAPIKey checks all sources in priority order and returns the key and its source.
// Priority: env var > keychain > .env next to executable > .env in CWD.
func ResolveAPIKey() (key string, source KeySource) {
	// 1. Environment variable
	if key = os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return key, KeySourceEnv
	}

	// 2. OS Keychain
	if key, err := keyring.Get(keyringService, keyringUser); err == nil && key != "" {
		return key, KeySourceKeychain
	}

	// 3. .env next to executable
	if key = loadKeyFromExeEnvFile(); key != "" {
		return key, KeySourceEnvFile
	}

	// 4. .env in CWD
	if key = loadKeyFromCWDEnvFile(); key != "" {
		return key, KeySourceEnvFile
	}

	return "", KeySourceNone
}

// StoreAPIKey writes the API key to the OS keychain.
func StoreAPIKey(key string) error {
	return keyring.Set(keyringService, keyringUser, key)
}

// DeleteAPIKey removes the API key from the OS keychain.
func DeleteAPIKey() error {
	return keyring.Delete(keyringService, keyringUser)
}

// loadKeyFromExeEnvFile tries to read ANTHROPIC_API_KEY from a .env file
// located relative to the executable (../../.env).
func loadKeyFromExeEnvFile() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return parseKeyFromEnvFile(filepath.Join(filepath.Dir(exe), "..", "..", ".env"))
}

// loadKeyFromCWDEnvFile tries to read ANTHROPIC_API_KEY from .env in the current directory.
func loadKeyFromCWDEnvFile() string {
	return parseKeyFromEnvFile(".env")
}

// parseKeyFromEnvFile reads a .env file and extracts the ANTHROPIC_API_KEY value.
func parseKeyFromEnvFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "ANTHROPIC_API_KEY=") {
			return strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
		}
	}
	return ""
}
