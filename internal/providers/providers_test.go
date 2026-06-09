package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jerryfane/agent-tools/internal/config"
)

func TestEnabledReturnsConfiguredProviders(t *testing.T) {
	cfg := config.Defaults()
	got := Enabled(cfg)
	if len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Fatalf("unexpected enabled providers: %v", got)
	}

	provider := cfg.Usage.Providers["claude"]
	provider.Enabled = false
	cfg.Usage.Providers["claude"] = provider
	if got := Enabled(cfg); len(got) != 1 || got[0] != "codex" {
		t.Fatalf("expected only codex when claude disabled, got %v", got)
	}
}

func TestLimitsForUnknownProvider(t *testing.T) {
	_, err := LimitsFor(context.Background(), config.Defaults(), "gemini", false)
	if err == nil || !strings.Contains(err.Error(), "not implemented yet") {
		t.Fatalf("expected not-implemented error, got %v", err)
	}
}

func TestLimitsForDisabledProvider(t *testing.T) {
	cfg := config.Defaults()
	provider := cfg.Usage.Providers["claude"]
	provider.Enabled = false
	cfg.Usage.Providers["claude"] = provider
	_, err := LimitsFor(context.Background(), cfg, "claude", false)
	if err == nil || !strings.Contains(err.Error(), "claude provider is disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
}

func TestLimitsForDispatchesToClaude(t *testing.T) {
	home := t.TempDir()
	creds := map[string]any{"claudeAiOauth": map[string]any{
		"accessToken": "tok", "refreshToken": "ref", "expiresAt": time.Now().Add(time.Hour).UnixMilli(),
		"subscriptionType": "max", "rateLimitTier": "default_claude_max_5x",
	}}
	data, _ := json.Marshal(creds)
	if err := os.WriteFile(filepath.Join(home, ".credentials.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":4,"resets_at":null},"seven_day":{"utilization":0,"resets_at":null}}`))
	}))
	defer server.Close()

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["claude"]
	provider.CacheDir = filepath.Join(t.TempDir(), "cache")
	provider.LimitsURL = server.URL
	provider.Profiles = map[string]config.ProfileConfig{"default": {Home: home, Label: "claude"}}
	cfg.Usage.Providers["claude"] = provider

	snapshots, err := LimitsFor(context.Background(), cfg, "claude", true)
	if err != nil {
		t.Fatalf("LimitsFor returned error: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Provider != "claude" {
		t.Fatalf("unexpected snapshots: %+v", snapshots)
	}
	if snapshots[0].FiveHourRemainingPercent == nil || *snapshots[0].FiveHourRemainingPercent != 96 {
		t.Fatalf("unexpected remaining: %+v", snapshots[0].FiveHourRemainingPercent)
	}
}
