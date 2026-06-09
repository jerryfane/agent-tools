// Package providers dispatches usage operations to the per-agent provider
// implementations (codex, claude) behind a single, provider-agnostic surface so
// the CLI, TUI, and Herdr publisher do not hardcode a single provider.
package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/jerryfane/agent-tools/internal/claude"
	"github.com/jerryfane/agent-tools/internal/codex"
	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/usage"
)

// Known returns the provider names this package can dispatch to.
func Known() []string {
	return []string{"claude", "codex"}
}

// ProfileInfo is a provider-agnostic view of a discovered profile.
type ProfileInfo struct {
	Name  string `json:"name"`
	Home  string `json:"home"`
	Label string `json:"label,omitempty"`
}

// ProfilesFor discovers the configured profiles for one provider.
func ProfilesFor(cfg config.Config, name string) ([]ProfileInfo, error) {
	switch name {
	case "codex":
		found, err := codex.DiscoverProfiles(cfg)
		if err != nil {
			return nil, err
		}
		return toProfileInfos(len(found), func(i int) ProfileInfo {
			return ProfileInfo{Name: found[i].Name, Home: found[i].Home, Label: found[i].Label}
		}), nil
	case "claude":
		found, err := claude.DiscoverProfiles(cfg)
		if err != nil {
			return nil, err
		}
		return toProfileInfos(len(found), func(i int) ProfileInfo {
			return ProfileInfo{Name: found[i].Name, Home: found[i].Home, Label: found[i].Label}
		}), nil
	default:
		return nil, fmt.Errorf("usage provider %q is not implemented yet", name)
	}
}

func toProfileInfos(n int, at func(int) ProfileInfo) []ProfileInfo {
	out := make([]ProfileInfo, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, at(i))
	}
	return out
}

// Enabled returns the configured-and-enabled provider names, preserving the
// order of Known().
func Enabled(cfg config.Config) []string {
	out := make([]string, 0, len(Known()))
	for _, name := range Known() {
		if p, ok := cfg.Usage.Providers[name]; ok && p.Enabled {
			out = append(out, name)
		}
	}
	return out
}

func ensureEnabled(cfg config.Config, name string) error {
	p, ok := cfg.Usage.Providers[name]
	if !ok {
		return fmt.Errorf("usage provider %q is not implemented yet", name)
	}
	if !p.Enabled {
		return fmt.Errorf("%s provider is disabled", name)
	}
	return nil
}

// LimitsFor returns subscription limit snapshots for one provider.
func LimitsFor(ctx context.Context, cfg config.Config, name string, force bool) ([]usage.LimitSnapshot, error) {
	if err := ensureEnabled(cfg, name); err != nil {
		return nil, err
	}
	switch name {
	case "codex":
		return codex.NewLimitsClient(cfg).Limits(ctx, codex.LimitsOptions{ForceRefresh: force})
	case "claude":
		return claude.NewLimitsClient(cfg).Limits(ctx, claude.LimitsOptions{ForceRefresh: force})
	default:
		return nil, fmt.Errorf("usage provider %q is not implemented yet", name)
	}
}

// UsageFor returns the day's token usage summary for one provider.
func UsageFor(ctx context.Context, cfg config.Config, name string, date time.Time) (usage.UsageSummary, error) {
	if err := ensureEnabled(cfg, name); err != nil {
		return usage.UsageSummary{}, err
	}
	switch name {
	case "codex":
		return codex.NewUsageClient(cfg).Usage(ctx, codex.UsageOptions{Date: date})
	case "claude":
		return claude.NewUsageClient(cfg).Usage(ctx, claude.UsageOptions{Date: date})
	default:
		return usage.UsageSummary{}, fmt.Errorf("usage provider %q is not implemented yet", name)
	}
}

// SessionsFor returns the day's sessions for one provider.
func SessionsFor(ctx context.Context, cfg config.Config, name string, date time.Time, limit int) ([]usage.SessionUsage, error) {
	if err := ensureEnabled(cfg, name); err != nil {
		return nil, err
	}
	switch name {
	case "codex":
		return codex.NewUsageClient(cfg).Sessions(ctx, codex.UsageOptions{Date: date, Limit: limit})
	case "claude":
		return claude.NewUsageClient(cfg).Sessions(ctx, claude.UsageOptions{Date: date, Limit: limit})
	default:
		return nil, fmt.Errorf("usage provider %q is not implemented yet", name)
	}
}
