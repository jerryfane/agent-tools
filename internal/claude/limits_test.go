package claude

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jerryfane/agent-tools/internal/config"
)

func TestLimitsFetchesAndNormalizesConfiguredProfile(t *testing.T) {
	authHome := t.TempDir()
	writeCreds(t, authHome, "secret-access", "secret-refresh", futureMillis())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-access" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != oauthBeta {
			t.Fatalf("unexpected anthropic-beta header: %q", got)
		}
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 4.0, "resets_at": "2026-06-10T03:00:00.970493+00:00"},
			"seven_day": {"utilization": 0.0, "resets_at": "2026-06-12T01:00:00+00:00"},
			"seven_day_opus": null,
			"extra_usage": {"is_enabled": false}
		}`))
	}))
	defer server.Close()

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["claude"]
	provider.CacheDir = filepath.Join(t.TempDir(), "cache")
	provider.LimitsURL = server.URL
	provider.Profiles = map[string]config.ProfileConfig{
		"work": {Home: authHome, Label: "w"},
	}
	cfg.Usage.Providers["claude"] = provider

	client := NewLimitsClient(cfg)
	client.now = func() time.Time { return time.UnixMilli(fixedNowMillis).UTC() }
	snapshots, err := client.Limits(context.Background(), LimitsOptions{ForceRefresh: true})
	if err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.Provider != "claude" || snapshot.Profile != "work" || snapshot.Label != "w" {
		t.Fatalf("unexpected snapshot identity: %+v", snapshot)
	}
	if snapshot.Plan != "max 5x" {
		t.Fatalf("unexpected plan label: %q", snapshot.Plan)
	}
	if snapshot.FiveHourRemainingPercent == nil || *snapshot.FiveHourRemainingPercent != 96 {
		t.Fatalf("unexpected five-hour remaining: %+v", snapshot.FiveHourRemainingPercent)
	}
	if snapshot.WeeklyRemainingPercent == nil || *snapshot.WeeklyRemainingPercent != 100 {
		t.Fatalf("unexpected weekly remaining: %+v", snapshot.WeeklyRemainingPercent)
	}
	if snapshot.FiveHourResetAt == nil || !snapshot.FiveHourResetAt.Equal(time.Date(2026, 6, 10, 3, 0, 0, 970493000, time.UTC)) {
		t.Fatalf("unexpected five-hour reset: %+v", snapshot.FiveHourResetAt)
	}
	if snapshot.Source != "api" || snapshot.Error != "" {
		t.Fatalf("unexpected source/error: %+v", snapshot)
	}
	assertPrivateFile(t, filepath.Join(provider.CacheDir, "limits.json"))
}

func TestLimitsToleratesNullWindow(t *testing.T) {
	authHome := t.TempDir()
	writeCreds(t, authHome, "secret-access", "secret-refresh", futureMillis())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 10.0, "resets_at": null},
			"seven_day": {"utilization": null, "resets_at": null}
		}`))
	}))
	defer server.Close()

	cfg := newClaudeConfig(t, server.URL, authHome)
	client := NewLimitsClient(cfg)
	client.now = func() time.Time { return time.UnixMilli(fixedNowMillis).UTC() }
	snapshots, err := client.Limits(context.Background(), LimitsOptions{ForceRefresh: true})
	if err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	if snapshots[0].FiveHourRemainingPercent == nil || *snapshots[0].FiveHourRemainingPercent != 90 {
		t.Fatalf("unexpected five-hour remaining: %+v", snapshots[0].FiveHourRemainingPercent)
	}
	if snapshots[0].WeeklyRemainingPercent != nil {
		t.Fatalf("expected nil weekly remaining for null utilization, got %+v", snapshots[0].WeeklyRemainingPercent)
	}
	if snapshots[0].FiveHourResetAt != nil {
		t.Fatalf("expected nil reset for null resets_at, got %+v", snapshots[0].FiveHourResetAt)
	}
}

func TestLimitsUsesFreshCacheWithoutNetwork(t *testing.T) {
	cacheDir := t.TempDir()
	authHome := t.TempDir()
	writeCreds(t, authHome, "secret-access", "secret-refresh", futureMillis())
	fetchedAt := time.UnixMilli(fixedNowMillis).UTC()
	cache := limitsCache{
		GeneratedAt: fetchedAt,
		Source:      "api",
		Profiles: map[string]cachedProfile{
			"work": {
				Name:            "work",
				Home:            authHome,
				Label:           "w",
				AuthFingerprint: authFingerprint(mustReadCreds(t, authHome)),
				Plan:            "max",
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
	provider := cfg.Usage.Providers["claude"]
	provider.CacheDir = cacheDir
	provider.LimitsURL = "http://127.0.0.1:1"
	provider.Profiles = map[string]config.ProfileConfig{"work": {Home: authHome, Label: "w"}}
	cfg.Usage.Providers["claude"] = provider

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

func TestLimits429SetsStickyCooldownAndServesStale(t *testing.T) {
	cacheDir := t.TempDir()
	authHome := t.TempDir()
	writeCreds(t, authHome, "secret-access", "secret-refresh", futureMillis())
	staleAt := time.UnixMilli(fixedNowMillis).Add(-time.Hour).UTC()
	cache := limitsCache{
		GeneratedAt: staleAt,
		Source:      "api",
		Profiles: map[string]cachedProfile{
			"work": {
				Name:            "work",
				Home:            authHome,
				Label:           "w",
				AuthFingerprint: authFingerprint(mustReadCreds(t, authHome)),
				Plan:            "max",
				Source:          "api",
				FetchedAt:       ptr(staleAt),
				Primary:         cachedWindow{RemainingPercent: ptr(40.0)},
				Secondary:       cachedWindow{RemainingPercent: ptr(50.0)},
			},
		},
	}
	if err := writePrivateJSON(filepath.Join(cacheDir, "limits.json"), cache); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["claude"]
	provider.CacheDir = cacheDir
	provider.LimitsURL = server.URL
	provider.Profiles = map[string]config.ProfileConfig{"work": {Home: authHome, Label: "w"}}
	cfg.Usage.Providers["claude"] = provider

	now := time.UnixMilli(fixedNowMillis).UTC()
	client := NewLimitsClient(cfg)
	client.now = func() time.Time { return now }

	// Cache is stale (1h old), so this triggers a fetch that 429s.
	snapshots, err := client.Limits(context.Background(), LimitsOptions{})
	if err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected exactly 1 request, got %d", requests)
	}
	if snapshots[0].Source != "cache (rate-limited)" {
		t.Fatalf("expected rate-limited source, got %q", snapshots[0].Source)
	}
	if snapshots[0].FiveHourRemainingPercent == nil || *snapshots[0].FiveHourRemainingPercent != 40 {
		t.Fatalf("expected stale data preserved, got %+v", snapshots[0].FiveHourRemainingPercent)
	}

	// Cooldown must be persisted.
	stored, err := readCache(filepath.Join(cacheDir, "limits.json"))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if stored.Profiles["work"].CooldownUntil == nil {
		t.Fatalf("expected cooldown persisted in cache")
	}

	// Second call with ForceRefresh must NOT hit the network during cooldown.
	snapshots, err = client.Limits(context.Background(), LimitsOptions{ForceRefresh: true})
	if err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected no additional request during cooldown, got %d total", requests)
	}
	if snapshots[0].Source != "cache (rate-limited)" {
		t.Fatalf("expected rate-limited source on second call, got %q", snapshots[0].Source)
	}
}

func TestLimits429CooldownExpiresAndRefetches(t *testing.T) {
	cacheDir := t.TempDir()
	authHome := t.TempDir()
	writeCreds(t, authHome, "secret-access", "secret-refresh", futureMillis())
	now := time.UnixMilli(fixedNowMillis).UTC()
	cache := limitsCache{
		GeneratedAt: now,
		Source:      "api",
		Profiles: map[string]cachedProfile{
			"work": {
				Name:            "work",
				Home:            authHome,
				AuthFingerprint: authFingerprint(mustReadCreds(t, authHome)),
				Source:          "cache (rate-limited)",
				FetchedAt:       ptr(now.Add(-time.Hour)),
				CooldownUntil:   ptr(now.Add(-time.Minute)), // expired cooldown
				Primary:         cachedWindow{RemainingPercent: ptr(40.0)},
			},
		},
	}
	if err := writePrivateJSON(filepath.Join(cacheDir, "limits.json"), cache); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":5,"resets_at":null},"seven_day":{"utilization":1,"resets_at":null}}`))
	}))
	defer server.Close()

	cfg := newClaudeConfig(t, server.URL, authHome)
	cfg.Usage.Providers["claude"] = withCacheDir(cfg.Usage.Providers["claude"], cacheDir)
	client := NewLimitsClient(cfg)
	client.now = func() time.Time { return now }
	snapshots, err := client.Limits(context.Background(), LimitsOptions{})
	if err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected refetch after cooldown expiry, got %d requests", requests)
	}
	if snapshots[0].Source != "api" {
		t.Fatalf("expected fresh api source, got %q", snapshots[0].Source)
	}
	if snapshots[0].FiveHourRemainingPercent == nil || *snapshots[0].FiveHourRemainingPercent != 95 {
		t.Fatalf("expected refreshed value, got %+v", snapshots[0].FiveHourRemainingPercent)
	}
}

func TestLimitsRefreshesTokenOnUnauthorized(t *testing.T) {
	authHome := t.TempDir()
	writeCreds(t, authHome, "expired-access", "secret-refresh", futureMillis())
	cacheDir := t.TempDir()

	usageCalls := 0
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usageCalls++
		if usageCalls == 1 {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fresh-access" {
			t.Fatalf("expected refreshed token, got %q", got)
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":30,"resets_at":null},"seven_day":{"utilization":40,"resets_at":null}}`))
	}))
	defer usageServer.Close()
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("expected JSON content-type, got %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("token request body not JSON: %v (%s)", err, body)
		}
		if payload["grant_type"] != "refresh_token" || payload["refresh_token"] != "secret-refresh" || payload["client_id"] != clientID {
			t.Fatalf("unexpected token request body: %v", payload)
		}
		_, _ = w.Write([]byte(`{"access_token":"fresh-access","refresh_token":"rotated-refresh","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["claude"]
	provider.CacheDir = cacheDir
	provider.LimitsURL = usageServer.URL
	provider.TokenURL = tokenServer.URL
	provider.Profiles = map[string]config.ProfileConfig{"work": {Home: authHome}}
	cfg.Usage.Providers["claude"] = provider

	client := NewLimitsClient(cfg)
	client.now = func() time.Time { return time.UnixMilli(fixedNowMillis).UTC() }
	snapshots, err := client.Limits(context.Background(), LimitsOptions{ForceRefresh: true})
	if err != nil {
		t.Fatalf("Limits returned error: %v", err)
	}
	if snapshots[0].FiveHourRemainingPercent == nil || *snapshots[0].FiveHourRemainingPercent != 70 {
		t.Fatalf("expected refreshed snapshot, got %+v", snapshots[0])
	}
	// Rotated refresh token is cached locally, never written back to credentials.
	assertPrivateFile(t, filepath.Join(cacheDir, "tokens.json"))
}

func TestDiscoverProfilesGlobsClaudeHomes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"crazyj", "jerryf"} {
		dir := filepath.Join(home, ".claude-"+name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		writeCreds(t, dir, "a", "r", futureMillis())
	}
	// A directory without credentials must be ignored.
	if err := os.MkdirAll(filepath.Join(home, ".claude-empty"), 0o700); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	profiles, err := DiscoverProfiles(cfg)
	if err != nil {
		t.Fatalf("DiscoverProfiles: %v", err)
	}
	if len(profiles) != 2 || profiles[0].Name != "crazyj" || profiles[1].Name != "jerryf" {
		t.Fatalf("unexpected profiles: %+v", profiles)
	}
}

const fixedNowMillis int64 = 1780000000000

func futureMillis() int64 {
	return fixedNowMillis + 24*60*60*1000
}

func newClaudeConfig(t *testing.T, limitsURL, home string) config.Config {
	t.Helper()
	cfg := config.Defaults()
	provider := cfg.Usage.Providers["claude"]
	provider.CacheDir = filepath.Join(t.TempDir(), "cache")
	provider.LimitsURL = limitsURL
	provider.Profiles = map[string]config.ProfileConfig{"work": {Home: home, Label: "w"}}
	cfg.Usage.Providers["claude"] = provider
	return cfg
}

func withCacheDir(provider config.ProviderConfig, dir string) config.ProviderConfig {
	provider.CacheDir = dir
	return provider
}

func writeCreds(t *testing.T, home, access, refresh string, expiresAtMillis int64) {
	t.Helper()
	file := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      access,
			"refreshToken":     refresh,
			"expiresAt":        expiresAtMillis,
			"subscriptionType": "max",
			"rateLimitTier":    "default_claude_max_5x",
		},
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".credentials.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustReadCreds(t *testing.T, home string) credentials {
	t.Helper()
	creds, err := readCredentials(home)
	if err != nil {
		t.Fatal(err)
	}
	return creds
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
