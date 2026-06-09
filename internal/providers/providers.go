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
