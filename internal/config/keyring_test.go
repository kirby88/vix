package config

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func init() {
	// Use in-memory mock keyring for all tests.
	keyring.MockInit()
}

func TestResolveAPIKey_EnvVarWins(t *testing.T) {
	// Store a key in the keychain
	if err := StoreAPIKey("keychain-key"); err != nil {
		t.Fatalf("StoreAPIKey: %v", err)
	}
	defer DeleteAPIKey()

	// Set env var — should take priority
	t.Setenv("ANTHROPIC_API_KEY", "env-key")

	key, source := ResolveAPIKey()
	if key != "env-key" {
		t.Errorf("expected env-key, got %q", key)
	}
	if source != KeySourceEnv {
		t.Errorf("expected source %q, got %q", KeySourceEnv, source)
	}
}

func TestResolveAPIKey_KeychainFallback(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	if err := StoreAPIKey("keychain-key"); err != nil {
		t.Fatalf("StoreAPIKey: %v", err)
	}
	defer DeleteAPIKey()

	key, source := ResolveAPIKey()
	if key != "keychain-key" {
		t.Errorf("expected keychain-key, got %q", key)
	}
	if source != KeySourceKeychain {
		t.Errorf("expected source %q, got %q", KeySourceKeychain, source)
	}
}

func TestResolveAPIKey_NoneWhenEmpty(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	// Ensure no keychain entry
	DeleteAPIKey()

	key, source := ResolveAPIKey()
	if key != "" {
		t.Errorf("expected empty key, got %q", key)
	}
	if source != KeySourceNone {
		t.Errorf("expected source %q, got %q", KeySourceNone, source)
	}
}

func TestStoreAndResolveRoundTrip(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	if err := StoreAPIKey("roundtrip-key"); err != nil {
		t.Fatalf("StoreAPIKey: %v", err)
	}
	defer DeleteAPIKey()

	key, source := ResolveAPIKey()
	if key != "roundtrip-key" || source != KeySourceKeychain {
		t.Errorf("round-trip failed: key=%q source=%q", key, source)
	}
}

func TestDeleteAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	StoreAPIKey("delete-me")
	if err := DeleteAPIKey(); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}

	key, source := ResolveAPIKey()
	if key != "" || source != KeySourceNone {
		t.Errorf("expected empty after delete, got key=%q source=%q", key, source)
	}
}
