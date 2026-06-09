package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jerryfane/agent-tools/internal/codex"
	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/usage"
	"github.com/spf13/cobra"
)

type rootOptions struct {
	configPath string
}

func NewRootCommand() *cobra.Command {
	opts := &rootOptions{}
	cmd := &cobra.Command{
		Use:   "agent-tools",
		Short: "Terminal tools for supervising coding agents",
	}
	cmd.PersistentFlags().StringVar(&opts.configPath, "config", "", "path to config file")
	cmd.AddCommand(newUsageCommand(opts))
	return cmd
}

func newUsageCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Inspect coding-agent limits, token usage, sessions, and alerts",
	}
	cmd.AddCommand(newUsageProvidersCommand(root))
	cmd.AddCommand(newUsageLimitsCommand(root))
	return cmd
}

func loadAppConfig(root *rootOptions) (config.Config, error) {
	return config.Load(root.configPath)
}

func newUsageLimitsCommand(root *rootOptions) *cobra.Command {
	var provider string
	var jsonOut bool
	var forceRefresh bool
	cmd := &cobra.Command{
		Use:   "limits",
		Short: "Show subscription limit snapshots",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadAppConfig(root)
			if err != nil {
				return err
			}
			if provider == "" {
				provider = "codex"
			}
			if provider != "codex" {
				return fmt.Errorf("usage limits provider %q is not implemented yet", provider)
			}
			if !cfg.Usage.Providers["codex"].Enabled {
				return fmt.Errorf("codex provider is disabled")
			}
			client := codex.NewLimitsClient(cfg)
			snapshots, err := client.Limits(context.Background(), codex.LimitsOptions{
				ForceRefresh: forceRefresh,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), snapshots)
			}
			location := configLocation(cfg.Usage.Timezone)
			table := newTable("PROFILE", "5H LEFT", "WEEK LEFT", "5H RESET", "WEEK RESET", "SOURCE", "ERROR")
			for _, snapshot := range snapshots {
				table.Add(
					labelOrProfile(snapshot),
					percentValue(snapshot.FiveHourRemainingPercent),
					percentValue(snapshot.WeeklyRemainingPercent),
					timeValue(snapshot.FiveHourResetAt, location, false),
					timeValue(snapshot.WeeklyResetAt, location, true),
					sourceValue(snapshot),
					snapshot.Error,
				)
			}
			table.Render(cmd.OutOrStdout())
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "codex", "usage provider")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	cmd.Flags().BoolVar(&forceRefresh, "force-refresh", false, "bypass local cache once")
	return cmd
}

func labelOrProfile(snapshot usage.LimitSnapshot) string {
	if snapshot.Label != "" {
		return snapshot.Label
	}
	return snapshot.Profile
}

func percentValue(value *float64) string {
	if value == nil {
		return "??%"
	}
	return fmt.Sprintf("%.0f%%", *value)
}

func configLocation(name string) *time.Location {
	if name == "" || strings.EqualFold(name, "local") {
		return time.Local
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return location
}

func timeValue(value *time.Time, location *time.Location, includeDate bool) string {
	if value == nil {
		return "unknown"
	}
	t := value.In(location)
	if includeDate {
		return t.Format("2006-01-02 15:04:05 MST")
	}
	return t.Format("15:04:05 MST")
}

func sourceValue(snapshot usage.LimitSnapshot) string {
	if snapshot.CacheAgeSeconds == nil {
		return snapshot.Source
	}
	return fmt.Sprintf("%s %s old", snapshot.Source, durationText(time.Duration(*snapshot.CacheAgeSeconds)*time.Second))
}

func durationText(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	minutes := int(duration.Minutes())
	hours := minutes / 60
	minutes = minutes % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func newUsageProvidersCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "Show configured usage providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadAppConfig(root)
			if err != nil {
				return err
			}
			registry := usage.NewRegistryFromConfig(cfg)
			providers := registry.List()
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), providers)
			}
			table := newTable("PROVIDER", "ENABLED", "DETAIL")
			for _, provider := range providers {
				table.Add(provider.Name, boolText(provider.Enabled), provider.Detail)
			}
			table.Render(cmd.OutOrStdout())
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	return cmd
}
