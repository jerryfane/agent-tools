package codex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jerryfane/agent-tools/internal/config"
)

func TestLimitsFetchesAndNormalizesConfiguredProfile(t *testing.T) {
	authHome := t.TempDir()
	writeAuth(t, authHome, "secret-access", "acct_123")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-access" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "acct_123" {
			t.Fatalf("unexpected account header: %q", got)
		}
		_, _ = w.Write([]byte(`{
			"plan_type": "plus",
			"rate_limit": {
				"allowed": true,
				"limit_reached": false,
				"primary_window": {
					"used_percent": 25,
					"reset_at": 1780000000,
					"limit_window_seconds": 18000
				},
				"secondary_window": {
					"used_percent": 60,
					"reset_at": 1780600000,
					"limit_window_seconds": 604800
				}
			}
		}`))
	}))
	defer server.Close()

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["codex"]
	provider.CacheDir = filepath.Join(t.TempDir(), "cache")
	provider.LimitsURL = server.URL
	provider.Profiles = map[string]config.ProfileConfig{
		"work": {Home: authHome, Label: "w"},
	}
	cfg.Usage.Providers["codex"] = provider

	client := NewLimitsClient(cfg)
	client.now = func() time.Time { return time.Unix(1779990000, 0).UTC() }
	snapshots, err := client.Limits(context.Background(), LimitsOptions{ForceRefresh: true})
	if err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.Profile != "work" || snapshot.Label != "w" || snapshot.Plan != "plus" {
		t.Fatalf("unexpected snapshot identity: %+v", snapshot)
	}
	if snapshot.FiveHourRemainingPercent == nil || *snapshot.FiveHourRemainingPercent != 75 {
		t.Fatalf("unexpected five-hour remaining: %+v", snapshot.FiveHourRemainingPercent)
	}
	if snapshot.WeeklyRemainingPercent == nil || *snapshot.WeeklyRemainingPercent != 40 {
		t.Fatalf("unexpected weekly remaining: %+v", snapshot.WeeklyRemainingPercent)
	}
	if snapshot.Source != "api" || snapshot.Error != "" {
		t.Fatalf("unexpected source/error: %+v", snapshot)
	}
	assertPrivateFile(t, filepath.Join(provider.CacheDir, "limits.json"))
}

func TestLimitsUsesFreshCacheWithoutNetwork(t *testing.T) {
	cacheDir := t.TempDir()
	authHome := t.TempDir()
	writeAuth(t, authHome, "secret-access", "acct_123")
	fetchedAt := time.Unix(1779990000, 0).UTC()
	cache := limitsCache{
		GeneratedAt: fetchedAt,
		Source:      "api",
		Profiles: map[string]cachedProfile{
			"work": {
				Name:            "work",
				Home:            authHome,
				Label:           "w",
				AuthFingerprint: authFingerprint(mustReadAuth(t, authHome)),
				Plan:            "plus",
				Source:          "api",
				FetchedAt:       ptr(fetchedAt),
				Primary:         cachedWindow{RemainingPercent: ptr(91.0)},
				Secondary:       cachedWindow{RemainingPercent: ptr(82.0)},
			},
		},
	}
	if err := writePrivateJSON(filepath.Join(cacheDir, "limits.json"), cache); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["codex"]
	provider.CacheDir = cacheDir
	provider.LimitsURL = "http://127.0.0.1:1"
	provider.Profiles = map[string]config.ProfileConfig{
		"work": {Home: authHome, Label: "w"},
	}
	cfg.Usage.Providers["codex"] = provider

	client := NewLimitsClient(cfg)
	client.now = func() time.Time { return fetchedAt.Add(time.Minute) }
	snapshots, err := client.Limits(context.Background(), LimitsOptions{})
	if err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	if got := snapshots[0].Source; got != "cache" {
		t.Fatalf("expected cache source, got %q", got)
	}
	if snapshots[0].CacheAgeSeconds == nil || *snapshots[0].CacheAgeSeconds != 60 {
		t.Fatalf("unexpected cache age: %+v", snapshots[0].CacheAgeSeconds)
	}
}

func TestLimitsIgnoresFreshCacheForChangedAccount(t *testing.T) {
	cacheDir := t.TempDir()
	authHome := t.TempDir()
	writeAuth(t, authHome, "fresh-access", "acct_new")
	oldAuthHome := t.TempDir()
	writeAuth(t, oldAuthHome, "old-access", "acct_old")
	fetchedAt := time.Unix(1779990000, 0).UTC()
	cache := limitsCache{
		GeneratedAt: fetchedAt,
		Source:      "api",
		Profiles: map[string]cachedProfile{
			"work": {
				Name:            "work",
				Home:            authHome,
				Label:           "w",
				AuthFingerprint: authFingerprint(mustReadAuth(t, oldAuthHome)),
				Plan:            "plus",
				Source:          "api",
				FetchedAt:       ptr(fetchedAt),
				Primary:         cachedWindow{RemainingPercent: ptr(1.0)},
				Secondary:       cachedWindow{RemainingPercent: ptr(2.0)},
			},
		},
	}
	if err := writePrivateJSON(filepath.Join(cacheDir, "limits.json"), cache); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"plan_type": "pro",
			"rate_limit": {
				"primary_window": {"used_percent": 10, "reset_at": 1780000000},
				"secondary_window": {"used_percent": 20, "reset_at": 1780600000}
			}
		}`))
	}))
	defer server.Close()

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["codex"]
	provider.CacheDir = cacheDir
	provider.LimitsURL = server.URL
	provider.Profiles = map[string]config.ProfileConfig{
		"work": {Home: authHome, Label: "w"},
	}
	cfg.Usage.Providers["codex"] = provider

	client := NewLimitsClient(cfg)
	client.now = func() time.Time { return fetchedAt.Add(time.Minute) }
	snapshots, err := client.Limits(context.Background(), LimitsOptions{})
	if err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	if snapshots[0].Source != "api" {
		t.Fatalf("expected fresh API source, got %+v", snapshots[0])
	}
	if snapshots[0].FiveHourRemainingPercent == nil || *snapshots[0].FiveHourRemainingPercent != 90 {
		t.Fatalf("expected refreshed remaining percent, got %+v", snapshots[0].FiveHourRemainingPercent)
	}
}

func TestLimitsDoesNotFallbackToCacheForChangedAccountFetchFailure(t *testing.T) {
	cacheDir := t.TempDir()
	authHome := t.TempDir()
	writeAuth(t, authHome, "fresh-access", "acct_new")
	oldAuthHome := t.TempDir()
	writeAuth(t, oldAuthHome, "old-access", "acct_old")
	fetchedAt := time.Unix(1779990000, 0).UTC()
	cache := limitsCache{
		GeneratedAt: fetchedAt,
		Source:      "api",
		Profiles: map[string]cachedProfile{
			"work": {
				Name:            "work",
				Home:            authHome,
				AuthFingerprint: authFingerprint(mustReadAuth(t, oldAuthHome)),
				Plan:            "plus",
				Source:          "api",
				FetchedAt:       ptr(fetchedAt),
				Primary:         cachedWindow{RemainingPercent: ptr(1.0)},
				Secondary:       cachedWindow{RemainingPercent: ptr(2.0)},
			},
		},
	}
	if err := writePrivateJSON(filepath.Join(cacheDir, "limits.json"), cache); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["codex"]
	provider.CacheDir = cacheDir
	provider.LimitsURL = server.URL
	provider.Profiles = map[string]config.ProfileConfig{
		"work": {Home: authHome, Label: "w"},
	}
	cfg.Usage.Providers["codex"] = provider

	client := NewLimitsClient(cfg)
	client.now = func() time.Time { return fetchedAt.Add(time.Minute) }
	snapshots, err := client.Limits(context.Background(), LimitsOptions{})
	if err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	if snapshots[0].Source != "error" {
		t.Fatalf("expected error source, got %+v", snapshots[0])
	}
	if snapshots[0].FiveHourRemainingPercent != nil {
		t.Fatalf("expected no stale quota value, got %+v", snapshots[0].FiveHourRemainingPercent)
	}
}

func TestLimitsPrunesRemovedProfilesFromCache(t *testing.T) {
	cacheDir := t.TempDir()
	authHome := t.TempDir()
	writeAuth(t, authHome, "secret-access", "acct_123")
	auth := mustReadAuth(t, authHome)
	fetchedAt := time.Unix(1779990000, 0).UTC()
	cache := limitsCache{
		GeneratedAt: fetchedAt,
		Source:      "api",
		Profiles: map[string]cachedProfile{
			"work": {
				Name:            "work",
				Home:            authHome,
				AuthFingerprint: authFingerprint(auth),
				Source:          "api",
				FetchedAt:       ptr(fetchedAt),
				Primary:         cachedWindow{RemainingPercent: ptr(91.0)},
				Secondary:       cachedWindow{RemainingPercent: ptr(82.0)},
			},
			"removed": {
				Name:            "removed",
				Home:            "/removed",
				AuthFingerprint: "old",
				Source:          "api",
				FetchedAt:       ptr(fetchedAt),
			},
		},
	}
	if err := writePrivateJSON(filepath.Join(cacheDir, "limits.json"), cache); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["codex"]
	provider.CacheDir = cacheDir
	provider.LimitsURL = "http://127.0.0.1:1"
	provider.Profiles = map[string]config.ProfileConfig{
		"work": {Home: authHome},
	}
	cfg.Usage.Providers["codex"] = provider

	client := NewLimitsClient(cfg)
	client.now = func() time.Time { return fetchedAt.Add(time.Minute) }
	if _, err := client.Limits(context.Background(), LimitsOptions{}); err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	updated, err := readCache(filepath.Join(cacheDir, "limits.json"))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if len(updated.Profiles) != 1 || updated.Profiles["work"].Name == "" {
		t.Fatalf("expected only current profile in cache, got %+v", updated.Profiles)
	}
}

func TestLimitsRetriesAuthRefreshTokenAfterCachedTokenFails(t *testing.T) {
	authHome := t.TempDir()
	writeAuth(t, authHome, "expired-access", "acct_123")
	auth := mustReadAuth(t, authHome)
	cacheDir := t.TempDir()
	tokenCachePath := filepath.Join(cacheDir, "tokens.json")
	tokens := tokenCache{Tokens: map[string]cachedToken{
		tokenCacheKey(profile{name: "work", home: authHome}, auth.Tokens.AccountID): {
			AccessToken:  "expired-access",
			RefreshToken: "stale-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		},
	}}
	if err := writePrivateJSON(tokenCachePath, tokens); err != nil {
		t.Fatalf("write token cache: %v", err)
	}

	var tokenURLValue string
	usageCalls := 0
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usageCalls++
		if usageCalls == 1 {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fresh-access" {
			t.Fatalf("expected refreshed access token, got %q", got)
		}
		_, _ = w.Write([]byte(`{
			"plan_type": "pro",
			"rate_limit": {
				"primary_window": {"used_percent": 30, "reset_at": 1780000000},
				"secondary_window": {"used_percent": 40, "reset_at": 1780600000}
			}
		}`))
	}))
	defer usageServer.Close()
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "stale-refresh") {
			http.Error(w, "stale", http.StatusBadRequest)
			return
		}
		if !strings.Contains(string(body), "secret-refresh") {
			t.Fatalf("expected auth.json refresh token, got body %q", string(body))
		}
		_, _ = w.Write([]byte(`{"access_token":"fresh-access","expires_in":3600}`))
	}))
	tokenURLValue = tokenServer.URL
	defer tokenServer.Close()

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["codex"]
	provider.CacheDir = cacheDir
	provider.LimitsURL = usageServer.URL
	provider.TokenURL = tokenURLValue
	provider.Profiles = map[string]config.ProfileConfig{
		"work": {Home: authHome},
	}
	cfg.Usage.Providers["codex"] = provider

	client := NewLimitsClient(cfg)
	snapshots, err := client.Limits(context.Background(), LimitsOptions{ForceRefresh: true})
	if err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	if snapshots[0].FiveHourRemainingPercent == nil || *snapshots[0].FiveHourRemainingPercent != 70 {
		t.Fatalf("expected refreshed limit snapshot, got %+v", snapshots[0])
	}
}

func writeAuth(t *testing.T, home, accessToken, accountID string) {
	t.Helper()
	auth := map[string]any{
		"tokens": map[string]any{
			"access_token":  accessToken,
			"refresh_token": "secret-refresh",
			"account_id":    accountID,
		},
	}
	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertPrivateFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected %s mode 0600, got %o", path, got)
	}
}

func mustReadAuth(t *testing.T, home string) authFile {
	t.Helper()
	auth, err := readAuth(home)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}
