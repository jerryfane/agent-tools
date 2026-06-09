package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/usage"
)

const (
	defaultUsageURL = "https://api.anthropic.com/api/oauth/usage"
	defaultTokenURL = "https://platform.claude.com/v1/oauth/token"
	clientID        = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	oauthBeta       = "oauth-2025-04-20"
	anthropicAPIVer = "2023-06-01"
	// minLimitsTTL is the floor for the limits refresh interval. The Anthropic
	// usage endpoint rate-limits aggressively, so never poll faster than this.
	minLimitsTTL = 5 * time.Minute
	// cooldownDuration is how long the provider stops calling the usage
	// endpoint after a 429. The 429 state is sticky and carries no Retry-After,
	// so the cooldown is long and serves cached data meanwhile.
	cooldownDuration = 30 * time.Minute
)

// tierMultiplier extracts a plan multiplier like "5x" from a rate-limit tier
// string such as "default_claude_max_5x".
var tierMultiplier = regexp.MustCompile(`([0-9]+x)`)

type LimitsOptions struct {
	ForceRefresh bool
}

type LimitsClient struct {
	cfg        config.Config
	httpClient *http.Client
	now        func() time.Time
}

func NewLimitsClient(cfg config.Config) *LimitsClient {
	return &LimitsClient{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 20 * time.Second},
		now:        time.Now,
	}
}

type profile struct {
	name  string
	home  string
	label string
}

type ProfileInfo struct {
	Name  string `json:"name"`
	Home  string `json:"home"`
	Label string `json:"label,omitempty"`
}

func DiscoverProfiles(cfg config.Config) ([]ProfileInfo, error) {
	profiles, err := (&LimitsClient{cfg: cfg}).discoverProfiles(cfg.Usage.Providers["claude"])
	if err != nil {
		return nil, err
	}
	out := make([]ProfileInfo, 0, len(profiles))
	for _, prof := range profiles {
		out = append(out, ProfileInfo{Name: prof.name, Home: prof.home, Label: prof.label})
	}
	return out, nil
}

func (c *LimitsClient) Limits(ctx context.Context, opts LimitsOptions) ([]usage.LimitSnapshot, error) {
	provider := c.cfg.Usage.Providers["claude"]
	profiles, err := c.discoverProfiles(provider)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, errors.New("no Claude profiles found")
	}

	unlock, err := lockCacheDir(c.cacheDir(provider))
	if err != nil {
		return nil, err
	}
	defer unlock()

	cache, _ := readCache(c.usageCachePath(provider))
	if !opts.ForceRefresh && c.cacheFresh(cache, profiles, c.now(), c.cacheTTL()) {
		return snapshotsFromCache(cache.Profiles, profiles, c.now(), "cache"), nil
	}

	tokenCache, _ := readTokenCache(c.tokenCachePath(provider))
	nextProfiles := map[string]cachedProfile{}
	for _, prof := range profiles {
		if cached, ok := cache.Profiles[prof.name]; ok {
			nextProfiles[prof.name] = cached
		}
	}

	for _, prof := range profiles {
		existing := nextProfiles[prof.name]

		// A sticky 429 cooldown overrides everything, including ForceRefresh:
		// the endpoint will keep returning 429 and re-calling it only extends
		// the lockout, so serve cached data until the cooldown expires.
		if existing.CooldownUntil != nil && c.now().Before(*existing.CooldownUntil) {
			existing = markCooldown(existing, prof, *existing.CooldownUntil)
			nextProfiles[prof.name] = existing
			continue
		}

		if !opts.ForceRefresh && c.cachedProfileFresh(existing, prof, c.now(), c.cacheTTL()) {
			continue
		}

		fetched, err := c.fetchProfile(ctx, provider, prof, &tokenCache)
		if err != nil {
			var httpErr httpStatusError
			if errors.As(err, &httpErr) && httpErr.Code == http.StatusTooManyRequests {
				until := c.now().Add(cooldownDuration)
				nextProfiles[prof.name] = markCooldown(existing, prof, until)
				continue
			}
			if existing.Name != "" && existing.FetchedAt != nil && cachedProfileMatchesCurrentAuth(existing, prof) {
				existing.Source = "cache"
				existing.Error = err.Error()
				nextProfiles[prof.name] = existing
				continue
			}
			nextProfiles[prof.name] = errorProfile(prof, err)
			continue
		}
		nextProfiles[prof.name] = fetched
	}

	nextCache := limitsCache{
		GeneratedAt: c.now().UTC(),
		Source:      "api",
		Profiles:    nextProfiles,
	}
	if err := writePrivateJSON(c.usageCachePath(provider), nextCache); err != nil {
		return nil, err
	}
	return snapshotsFromCache(nextCache.Profiles, profiles, c.now(), ""), nil
}

func (c *LimitsClient) discoverProfiles(provider config.ProviderConfig) ([]profile, error) {
	if len(provider.Profiles) > 0 {
		names := make([]string, 0, len(provider.Profiles))
		for name := range provider.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		profiles := make([]profile, 0, len(names))
		for _, name := range names {
			item := provider.Profiles[name]
			if strings.TrimSpace(item.Home) == "" {
				return nil, fmt.Errorf("claude profile %q has empty home", name)
			}
			profiles = append(profiles, profile{
				name:  name,
				home:  expandPath(item.Home),
				label: item.Label,
			})
		}
		return profiles, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".claude-*"))
	profiles := make([]profile, 0, len(matches))
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(match, ".credentials.json")); err != nil {
			continue
		}
		name := strings.TrimPrefix(filepath.Base(match), ".claude-")
		profiles = append(profiles, profile{name: name, home: match, label: name})
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].name < profiles[j].name
	})
	if len(profiles) > 0 {
		return profiles, nil
	}
	return []profile{{name: "default", home: filepath.Join(home, ".claude"), label: "claude"}}, nil
}

func (c *LimitsClient) fetchProfile(ctx context.Context, provider config.ProviderConfig, prof profile, tokens *tokenCache) (cachedProfile, error) {
	creds, err := readCredentials(prof.home)
	if err != nil {
		return cachedProfile{}, err
	}
	accessToken := cachedAccessToken(prof, creds, *tokens, c.now())

	var raw usageResponse
	for attempt := 0; attempt < 2; attempt++ {
		raw, err = c.requestUsage(ctx, provider, accessToken)
		if err == nil {
			return normalizeProfile(prof, creds, raw, c.now()), nil
		}
		var httpErr httpStatusError
		if !errors.As(err, &httpErr) || attempt > 0 || (httpErr.Code != http.StatusUnauthorized && httpErr.Code != http.StatusForbidden) {
			return cachedProfile{}, err
		}
		refreshed, refreshErr := c.refreshAccessToken(ctx, provider, prof, creds, *tokens)
		if refreshErr != nil {
			return cachedProfile{}, refreshErr
		}
		storeToken(prof, creds, refreshed, tokens, c.now())
		if err := writePrivateJSON(c.tokenCachePath(provider), tokens); err != nil {
			return cachedProfile{}, err
		}
		accessToken = refreshed.AccessToken
	}
	return cachedProfile{}, errors.New("usage fetch failed after token refresh")
}

func (c *LimitsClient) requestUsage(ctx context.Context, provider config.ProviderConfig, accessToken string) (usageResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL(provider), nil)
	if err != nil {
		return usageResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", oauthBeta)
	req.Header.Set("anthropic-version", anthropicAPIVer)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agent-tools")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return usageResponse{}, fmt.Errorf("usage fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, resp.Body)
		return usageResponse{}, httpStatusError{Code: resp.StatusCode, Message: "usage fetch failed"}
	}
	var out usageResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return usageResponse{}, fmt.Errorf("usage response JSON decode failed: %w", err)
	}
	return out, nil
}

func (c *LimitsClient) refreshToken(ctx context.Context, provider config.ProviderConfig, token string) (refreshedToken, error) {
	if token == "" {
		return refreshedToken{}, errors.New("missing refresh_token")
	}
	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": token,
		"client_id":     clientID,
	})
	if err != nil {
		return refreshedToken{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL(provider), strings.NewReader(string(body)))
	if err != nil {
		return refreshedToken{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", oauthBeta)
	req.Header.Set("User-Agent", "agent-tools")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return refreshedToken{}, fmt.Errorf("token refresh failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, resp.Body)
		return refreshedToken{}, httpStatusError{Code: resp.StatusCode, Message: "token refresh failed"}
	}
	var out refreshedToken
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return refreshedToken{}, fmt.Errorf("token refresh JSON decode failed: %w", err)
	}
	if out.AccessToken == "" {
		return refreshedToken{}, errors.New("token refresh response did not include access_token")
	}
	return out, nil
}

func (c *LimitsClient) refreshAccessToken(ctx context.Context, provider config.ProviderConfig, prof profile, creds credentials, cache tokenCache) (refreshedToken, error) {
	var lastErr error
	for _, token := range refreshTokenCandidates(prof, creds, cache) {
		refreshed, err := c.refreshToken(ctx, provider, token)
		if err == nil {
			return refreshed, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return refreshedToken{}, lastErr
	}
	return refreshedToken{}, errors.New("missing refresh_token")
}

func (c *LimitsClient) cacheDir(provider config.ProviderConfig) string {
	if strings.TrimSpace(provider.CacheDir) != "" {
		return expandPath(provider.CacheDir)
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "agent-tools", "claude")
}

func (c *LimitsClient) usageCachePath(provider config.ProviderConfig) string {
	return filepath.Join(c.cacheDir(provider), "limits.json")
}

func (c *LimitsClient) tokenCachePath(provider config.ProviderConfig) string {
	return filepath.Join(c.cacheDir(provider), "tokens.json")
}

// cacheTTL returns the limits refresh interval, never shorter than minLimitsTTL
// because the usage endpoint rate-limits aggressive polling.
func (c *LimitsClient) cacheTTL() time.Duration {
	ttl := minLimitsTTL
	if c.cfg.Usage.RefreshLimitsSeconds > 0 {
		ttl = time.Duration(c.cfg.Usage.RefreshLimitsSeconds) * time.Second
	}
	if ttl < minLimitsTTL {
		ttl = minLimitsTTL
	}
	return ttl
}

func usageURL(provider config.ProviderConfig) string {
	if provider.LimitsURL != "" {
		return provider.LimitsURL
	}
	return defaultUsageURL
}

func tokenURL(provider config.ProviderConfig) string {
	if provider.TokenURL != "" {
		return provider.TokenURL
	}
	return defaultTokenURL
}

func expandPath(path string) string {
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

// credentials holds the fields agent-tools reads from ~/.claude/.credentials.json.
type credentials struct {
	AccessToken      string
	RefreshToken     string
	ExpiresAt        int64 // milliseconds since epoch
	SubscriptionType string
	RateLimitTier    string
}

type credentialsFile struct {
	ClaudeAiOauth struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		ExpiresAt        int64  `json:"expiresAt"`
		SubscriptionType string `json:"subscriptionType"`
		RateLimitTier    string `json:"rateLimitTier"`
	} `json:"claudeAiOauth"`
}

func readCredentials(home string) (credentials, error) {
	path := filepath.Join(home, ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && runtime.GOOS == "darwin" {
			return credentials{}, fmt.Errorf("%s not found; on macOS Claude Code stores credentials in the Keychain, which agent-tools does not yet read", path)
		}
		return credentials{}, fmt.Errorf("missing %s", path)
	}
	var file credentialsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return credentials{}, fmt.Errorf("invalid JSON in %s", path)
	}
	creds := credentials{
		AccessToken:      file.ClaudeAiOauth.AccessToken,
		RefreshToken:     file.ClaudeAiOauth.RefreshToken,
		ExpiresAt:        file.ClaudeAiOauth.ExpiresAt,
		SubscriptionType: file.ClaudeAiOauth.SubscriptionType,
		RateLimitTier:    file.ClaudeAiOauth.RateLimitTier,
	}
	if creds.AccessToken == "" {
		return credentials{}, fmt.Errorf("missing accessToken in %s", path)
	}
	return creds, nil
}

type usageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

type usageResponse struct {
	FiveHour usageWindow `json:"five_hour"`
	SevenDay usageWindow `json:"seven_day"`
}

type cachedWindow struct {
	UtilizationPercent *float64   `json:"utilization_percent,omitempty"`
	RemainingPercent   *float64   `json:"remaining_percent,omitempty"`
	ResetAt            *time.Time `json:"reset_at,omitempty"`
}

type cachedProfile struct {
	Name            string       `json:"name"`
	Home            string       `json:"home"`
	Label           string       `json:"label,omitempty"`
	AuthFingerprint string       `json:"auth_fingerprint,omitempty"`
	Plan            string       `json:"plan,omitempty"`
	Primary         cachedWindow `json:"primary"`
	Secondary       cachedWindow `json:"secondary"`
	FetchedAt       *time.Time   `json:"fetched_at,omitempty"`
	CooldownUntil   *time.Time   `json:"cooldown_until,omitempty"`
	Source          string       `json:"source"`
	Error           string       `json:"error,omitempty"`
}

type limitsCache struct {
	GeneratedAt time.Time                `json:"generated_at"`
	Source      string                   `json:"source"`
	Profiles    map[string]cachedProfile `json:"profiles"`
}

type cachedToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    int64     `json:"expires_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type tokenCache struct {
	Tokens map[string]cachedToken `json:"tokens"`
}

type refreshedToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func normalizeProfile(prof profile, creds credentials, raw usageResponse, now time.Time) cachedProfile {
	return cachedProfile{
		Name:            prof.name,
		Home:            prof.home,
		Label:           prof.label,
		AuthFingerprint: authFingerprint(creds),
		Plan:            planLabel(creds),
		Primary:         normalizeWindow(raw.FiveHour),
		Secondary:       normalizeWindow(raw.SevenDay),
		FetchedAt:       ptr(now.UTC()),
		Source:          "api",
	}
}

func normalizeWindow(raw usageWindow) cachedWindow {
	var utilization, remaining *float64
	if raw.Utilization != nil {
		used := clamp(*raw.Utilization, 0, 100)
		utilization = ptr(used)
		remaining = ptr(clamp(100-used, 0, 100))
	}
	return cachedWindow{
		UtilizationPercent: utilization,
		RemainingPercent:   remaining,
		ResetAt:            parseResetAt(raw.ResetsAt),
	}
}

func parseResetAt(value *string) *time.Time {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// planLabel renders a short plan name from the subscription type and rate-limit
// tier, e.g. subscriptionType "max" + tier "default_claude_max_5x" -> "max 5x".
func planLabel(creds credentials) string {
	plan := strings.TrimSpace(creds.SubscriptionType)
	if plan == "" {
		plan = "unknown"
	}
	if match := tierMultiplier.FindString(creds.RateLimitTier); match != "" {
		return plan + " " + match
	}
	return plan
}

func snapshotsFromCache(cached map[string]cachedProfile, profiles []profile, now time.Time, sourceOverride string) []usage.LimitSnapshot {
	out := make([]usage.LimitSnapshot, 0, len(profiles))
	for _, prof := range profiles {
		item := cached[prof.name]
		source := item.Source
		if sourceOverride != "" && source != "error" && source != "cache (rate-limited)" {
			source = sourceOverride
		}
		out = append(out, usage.LimitSnapshot{
			Provider:                 "claude",
			Profile:                  prof.name,
			Label:                    firstNonEmpty(item.Label, prof.label),
			Plan:                     item.Plan,
			FiveHourRemainingPercent: item.Primary.RemainingPercent,
			WeeklyRemainingPercent:   item.Secondary.RemainingPercent,
			FiveHourResetAt:          item.Primary.ResetAt,
			WeeklyResetAt:            item.Secondary.ResetAt,
			Source:                   source,
			CacheAgeSeconds:          cacheAge(item.FetchedAt, now),
			Error:                    item.Error,
		})
	}
	return out
}

// markCooldown records a sticky 429 cooldown on a profile while preserving any
// previously fetched window data so the snapshot still shows the last numbers.
func markCooldown(existing cachedProfile, prof profile, until time.Time) cachedProfile {
	if existing.Name == "" {
		existing.Name = prof.name
		existing.Home = prof.home
		existing.Label = prof.label
	}
	existing.CooldownUntil = ptr(until.UTC())
	existing.Source = "cache (rate-limited)"
	existing.Error = fmt.Sprintf("rate-limited by Anthropic; retrying after %s", until.UTC().Format("15:04:05 MST"))
	return existing
}

func errorProfile(prof profile, err error) cachedProfile {
	return cachedProfile{
		Name:      prof.name,
		Home:      prof.home,
		Label:     prof.label,
		Primary:   cachedWindow{},
		Secondary: cachedWindow{},
		Source:    "error",
		Error:     err.Error(),
	}
}

func (c *LimitsClient) cacheFresh(cache limitsCache, profiles []profile, now time.Time, ttl time.Duration) bool {
	if len(cache.Profiles) != len(profiles) {
		return false
	}
	for _, prof := range profiles {
		if !c.cachedProfileFresh(cache.Profiles[prof.name], prof, now, ttl) {
			return false
		}
	}
	return true
}

func (c *LimitsClient) cachedProfileFresh(profile cachedProfile, prof profile, now time.Time, ttl time.Duration) bool {
	if profile.Name == "" || profile.FetchedAt == nil {
		return false
	}
	if !cachedProfileMatchesCurrentAuth(profile, prof) {
		return false
	}
	return now.Sub(*profile.FetchedAt) <= ttl
}

func cachedProfileMatchesCurrentAuth(profile cachedProfile, prof profile) bool {
	if profile.Name != prof.name || profile.Home != prof.home || profile.AuthFingerprint == "" {
		return false
	}
	creds, err := readCredentials(prof.home)
	if err != nil {
		return false
	}
	return profile.AuthFingerprint == authFingerprint(creds)
}

func cacheAge(fetchedAt *time.Time, now time.Time) *int64 {
	if fetchedAt == nil {
		return nil
	}
	seconds := int64(now.Sub(*fetchedAt).Seconds())
	if seconds < 0 {
		seconds = 0
	}
	return &seconds
}

func cachedAccessToken(prof profile, creds credentials, cache tokenCache, now time.Time) string {
	// Claude Code keeps the credentials file's access token fresh, so prefer it
	// whenever it is still valid by its own expiry.
	if creds.ExpiresAt > 0 && creds.ExpiresAt > now.UnixMilli()+60_000 {
		return creds.AccessToken
	}
	key := tokenCacheKey(prof)
	cached := cache.Tokens[key]
	if cached.AccessToken != "" && cached.ExpiresAt > now.Unix()+60 {
		return cached.AccessToken
	}
	return creds.AccessToken
}

func refreshTokenCandidates(prof profile, creds credentials, cache tokenCache) []string {
	var out []string
	add := func(token string) {
		if token == "" {
			return
		}
		for _, existing := range out {
			if existing == token {
				return
			}
		}
		out = append(out, token)
	}
	if cached := cache.Tokens[tokenCacheKey(prof)]; cached.RefreshToken != "" {
		add(cached.RefreshToken)
	}
	add(creds.RefreshToken)
	return out
}

func storeToken(prof profile, creds credentials, refreshed refreshedToken, cache *tokenCache, now time.Time) {
	if cache.Tokens == nil {
		cache.Tokens = map[string]cachedToken{}
	}
	refresh := refreshed.RefreshToken
	if refresh == "" {
		refresh = creds.RefreshToken
	}
	expires := refreshed.ExpiresIn
	if expires == 0 {
		expires = 3600
	}
	cache.Tokens[tokenCacheKey(prof)] = cachedToken{
		AccessToken:  refreshed.AccessToken,
		RefreshToken: refresh,
		ExpiresAt:    now.Unix() + expires,
		UpdatedAt:    now.UTC(),
	}
}

func tokenCacheKey(prof profile) string {
	return prof.name + ":" + prof.home
}

func readCache(path string) (limitsCache, error) {
	var cache limitsCache
	if err := readJSON(path, &cache); err != nil {
		return limitsCache{}, err
	}
	if cache.Profiles == nil {
		cache.Profiles = map[string]cachedProfile{}
	}
	return cache, nil
}

func readTokenCache(path string) (tokenCache, error) {
	var cache tokenCache
	if err := readJSON(path, &cache); err != nil {
		return tokenCache{}, err
	}
	if cache.Tokens == nil {
		cache.Tokens = map[string]cachedToken{}
	}
	return cache, nil
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func ensurePrivateDir(path string) {
	_ = os.MkdirAll(path, 0o700)
	_ = os.Chmod(path, 0o700)
}

func writePrivateJSON(path string, value any) error {
	ensurePrivateDir(filepath.Dir(path))
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := fmt.Sprintf("%s.%d.%d.tmp", path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func lockCacheDir(path string) (func(), error) {
	ensurePrivateDir(path)
	lockPath := filepath.Join(path, "limits.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(lockPath, 0o600)
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

// authFingerprint is derived from the refresh token, which is stable for an
// account until the user re-authenticates Claude Code. agent-tools never writes
// the credentials file, so our own refreshes do not change it.
func authFingerprint(creds credentials) string {
	seed := creds.RefreshToken
	if seed == "" {
		seed = creds.AccessToken
	}
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func ptr[T any](value T) *T {
	return &value
}

type httpStatusError struct {
	Code    int
	Message string
}

func (e httpStatusError) Error() string {
	return fmt.Sprintf("%s with HTTP %d", e.Message, e.Code)
}
