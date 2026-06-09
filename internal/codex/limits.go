package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/usage"
)

const (
	defaultUsageURL = "https://chatgpt.com/backend-api/wham/usage"
	defaultTokenURL = "https://auth.openai.com/oauth/token"
	clientID        = "app_EMoamEEZ73f0CkXaXp7hrann"
)

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
	profiles, err := (&LimitsClient{cfg: cfg}).discoverProfiles(cfg.Usage.Providers["codex"])
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
	provider := c.cfg.Usage.Providers["codex"]
	profiles, err := c.discoverProfiles(provider)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, errors.New("no Codex profiles found")
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
		if !opts.ForceRefresh && c.cachedProfileFresh(existing, prof, c.now(), c.cacheTTL()) {
			continue
		}
		fetched, err := c.fetchProfile(ctx, provider, prof, &tokenCache)
		if err != nil {
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
				return nil, fmt.Errorf("codex profile %q has empty home", name)
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
	matches, _ := filepath.Glob(filepath.Join(home, ".codex-*"))
	profiles := make([]profile, 0, len(matches))
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(match, "auth.json")); err != nil {
			continue
		}
		name := strings.TrimPrefix(filepath.Base(match), ".codex-")
		profiles = append(profiles, profile{name: name, home: match, label: name})
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].name < profiles[j].name
	})
	if len(profiles) > 0 {
		return profiles, nil
	}
	return []profile{{name: "default", home: filepath.Join(home, ".codex"), label: "codex"}}, nil
}

func (c *LimitsClient) fetchProfile(ctx context.Context, provider config.ProviderConfig, prof profile, tokens *tokenCache) (cachedProfile, error) {
	auth, err := readAuth(prof.home)
	if err != nil {
		return cachedProfile{}, err
	}
	accessToken := cachedAccessToken(prof, auth, *tokens, c.now())
	accountID := auth.Tokens.AccountID

	var raw usageResponse
	for attempt := 0; attempt < 2; attempt++ {
		raw, err = c.requestUsage(ctx, provider, accessToken, accountID)
		if err == nil {
			return normalizeProfile(prof, auth, raw, c.now()), nil
		}
		var httpErr httpStatusError
		if !errors.As(err, &httpErr) || attempt > 0 || (httpErr.Code != http.StatusUnauthorized && httpErr.Code != http.StatusForbidden) {
			return cachedProfile{}, err
		}
		refreshed, refreshErr := c.refreshAccessToken(ctx, provider, prof, auth, *tokens)
		if refreshErr != nil {
			return cachedProfile{}, refreshErr
		}
		storeToken(prof, auth, refreshed, tokens, c.now())
		if err := writePrivateJSON(c.tokenCachePath(provider), tokens); err != nil {
			return cachedProfile{}, err
		}
		accessToken = refreshed.AccessToken
	}
	return cachedProfile{}, errors.New("usage fetch failed after token refresh")
}

func (c *LimitsClient) requestUsage(ctx context.Context, provider config.ProviderConfig, accessToken, accountID string) (usageResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL(provider), nil)
	if err != nil {
		return usageResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("chatgpt-account-id", accountID)
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
	body := url.Values{
		"grant_type":    []string{"refresh_token"},
		"refresh_token": []string{token},
		"client_id":     []string{clientID},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL(provider), strings.NewReader(body))
	if err != nil {
		return refreshedToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

func (c *LimitsClient) refreshAccessToken(ctx context.Context, provider config.ProviderConfig, prof profile, auth authFile, cache tokenCache) (refreshedToken, error) {
	var lastErr error
	for _, token := range refreshTokenCandidates(prof, auth, cache) {
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
	return filepath.Join(base, "agent-tools", "codex")
}

func (c *LimitsClient) usageCachePath(provider config.ProviderConfig) string {
	return filepath.Join(c.cacheDir(provider), "limits.json")
}

func (c *LimitsClient) tokenCachePath(provider config.ProviderConfig) string {
	return filepath.Join(c.cacheDir(provider), "tokens.json")
}

func (c *LimitsClient) cacheTTL() time.Duration {
	if c.cfg.Usage.RefreshLimitsSeconds <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(c.cfg.Usage.RefreshLimitsSeconds) * time.Second
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

type authFile struct {
	Tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

func readAuth(home string) (authFile, error) {
	path := filepath.Join(home, "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return authFile{}, fmt.Errorf("missing %s", path)
	}
	var auth authFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return authFile{}, fmt.Errorf("invalid JSON in %s", path)
	}
	if auth.Tokens.AccessToken == "" {
		return authFile{}, fmt.Errorf("missing access_token in %s", path)
	}
	if auth.Tokens.AccountID == "" {
		return authFile{}, fmt.Errorf("missing account_id in %s", path)
	}
	return auth, nil
}

type usageResponse struct {
	PlanType             string `json:"plan_type"`
	RateLimitReachedType any    `json:"rate_limit_reached_type"`
	RateLimit            struct {
		Allowed         *bool     `json:"allowed"`
		LimitReached    *bool     `json:"limit_reached"`
		PrimaryWindow   rawWindow `json:"primary_window"`
		SecondaryWindow rawWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

type rawWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	ResetAt            *int64   `json:"reset_at"`
	ResetsAt           *int64   `json:"resets_at"`
	LimitWindowSeconds *int64   `json:"limit_window_seconds"`
}

type cachedWindow struct {
	UsedPercent        *float64 `json:"used_percent,omitempty"`
	RemainingPercent   *float64 `json:"remaining_percent,omitempty"`
	ResetAt            *int64   `json:"reset_at,omitempty"`
	LimitWindowSeconds *int64   `json:"limit_window_seconds,omitempty"`
}

type cachedProfile struct {
	Name                 string       `json:"name"`
	Home                 string       `json:"home"`
	Label                string       `json:"label,omitempty"`
	AuthFingerprint      string       `json:"auth_fingerprint,omitempty"`
	Plan                 string       `json:"plan,omitempty"`
	Allowed              *bool        `json:"allowed,omitempty"`
	LimitReached         *bool        `json:"limit_reached,omitempty"`
	RateLimitReachedType string       `json:"rate_limit_reached_type,omitempty"`
	Primary              cachedWindow `json:"primary"`
	Secondary            cachedWindow `json:"secondary"`
	FetchedAt            *time.Time   `json:"fetched_at,omitempty"`
	Source               string       `json:"source"`
	Error                string       `json:"error,omitempty"`
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

func normalizeProfile(prof profile, auth authFile, raw usageResponse, now time.Time) cachedProfile {
	plan := raw.PlanType
	if plan == "" {
		plan = "unknown"
	}
	return cachedProfile{
		Name:                 prof.name,
		Home:                 prof.home,
		Label:                prof.label,
		AuthFingerprint:      authFingerprint(auth),
		Plan:                 plan,
		Allowed:              raw.RateLimit.Allowed,
		LimitReached:         raw.RateLimit.LimitReached,
		RateLimitReachedType: stringValue(raw.RateLimitReachedType),
		Primary:              normalizeWindow(raw.RateLimit.PrimaryWindow),
		Secondary:            normalizeWindow(raw.RateLimit.SecondaryWindow),
		FetchedAt:            ptr(now.UTC()),
		Source:               "api",
	}
}

func normalizeWindow(raw rawWindow) cachedWindow {
	var remaining *float64
	if raw.UsedPercent != nil {
		used := clamp(*raw.UsedPercent, 0, 100)
		remaining = ptr(clamp(100-used, 0, 100))
		raw.UsedPercent = ptr(used)
	}
	resetAt := raw.ResetAt
	if resetAt == nil {
		resetAt = raw.ResetsAt
	}
	return cachedWindow{
		UsedPercent:        raw.UsedPercent,
		RemainingPercent:   remaining,
		ResetAt:            resetAt,
		LimitWindowSeconds: raw.LimitWindowSeconds,
	}
}

func snapshotsFromCache(cached map[string]cachedProfile, profiles []profile, now time.Time, sourceOverride string) []usage.LimitSnapshot {
	out := make([]usage.LimitSnapshot, 0, len(profiles))
	for _, prof := range profiles {
		item := cached[prof.name]
		source := item.Source
		if sourceOverride != "" && source != "error" {
			source = sourceOverride
		}
		out = append(out, usage.LimitSnapshot{
			Provider:                 "codex",
			Profile:                  prof.name,
			Label:                    firstNonEmpty(item.Label, prof.label),
			Plan:                     item.Plan,
			FiveHourRemainingPercent: item.Primary.RemainingPercent,
			WeeklyRemainingPercent:   item.Secondary.RemainingPercent,
			FiveHourResetAt:          epochTime(item.Primary.ResetAt),
			WeeklyResetAt:            epochTime(item.Secondary.ResetAt),
			Source:                   source,
			CacheAgeSeconds:          cacheAge(item.FetchedAt, now),
			Error:                    item.Error,
		})
	}
	return out
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
	auth, err := readAuth(prof.home)
	if err != nil {
		return false
	}
	return profile.AuthFingerprint == authFingerprint(auth)
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

func epochTime(value *int64) *time.Time {
	if value == nil {
		return nil
	}
	t := time.Unix(*value, 0).UTC()
	return &t
}

func cachedAccessToken(prof profile, auth authFile, cache tokenCache, now time.Time) string {
	key := tokenCacheKey(prof, auth.Tokens.AccountID)
	cached := cache.Tokens[key]
	if cached.AccessToken != "" && cached.ExpiresAt > now.Unix()+60 {
		return cached.AccessToken
	}
	return auth.Tokens.AccessToken
}

func refreshTokenCandidates(prof profile, auth authFile, cache tokenCache) []string {
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
	key := tokenCacheKey(prof, auth.Tokens.AccountID)
	if cache.Tokens[key].RefreshToken != "" {
		add(cache.Tokens[key].RefreshToken)
	}
	add(auth.Tokens.RefreshToken)
	return out
}

func storeToken(prof profile, auth authFile, refreshed refreshedToken, cache *tokenCache, now time.Time) {
	if cache.Tokens == nil {
		cache.Tokens = map[string]cachedToken{}
	}
	refresh := refreshed.RefreshToken
	if refresh == "" {
		refresh = auth.Tokens.RefreshToken
	}
	expires := refreshed.ExpiresIn
	if expires == 0 {
		expires = 3600
	}
	cache.Tokens[tokenCacheKey(prof, auth.Tokens.AccountID)] = cachedToken{
		AccessToken:  refreshed.AccessToken,
		RefreshToken: refresh,
		ExpiresAt:    now.Unix() + expires,
		UpdatedAt:    now.UTC(),
	}
}

func tokenCacheKey(prof profile, accountID string) string {
	return prof.name + ":" + prof.home + ":" + accountID
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

func authFingerprint(auth authFile) string {
	sum := sha256.Sum256([]byte(auth.Tokens.AccountID))
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

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
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
