package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jerryfane/agent-tools/internal/codex"
	"github.com/jerryfane/agent-tools/internal/config"
	herdrpub "github.com/jerryfane/agent-tools/internal/herdr"
	usagetui "github.com/jerryfane/agent-tools/internal/tui"
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
	cmd.AddCommand(newUsageTodayCommand(root))
	cmd.AddCommand(newUsageSessionsCommand(root))
	cmd.AddCommand(newUsageTUICommand(root))
	cmd.AddCommand(newUsageHerdrPublisherCommand(root))
	cmd.AddCommand(newUsageDoctorCommand(root))
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

func newUsageTodayCommand(root *rootOptions) *cobra.Command {
	var provider string
	var jsonOut bool
	var dateValue string
	cmd := &cobra.Command{
		Use:   "today",
		Short: "Show current-day token usage grouped by repo and task type",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadAppConfig(root)
			if err != nil {
				return err
			}
			if provider == "" {
				provider = "codex"
			}
			if provider != "codex" {
				return fmt.Errorf("usage today provider %q is not implemented yet", provider)
			}
			if !cfg.Usage.Providers["codex"].Enabled {
				return fmt.Errorf("codex provider is disabled")
			}
			date, err := parseDate(dateValue, cfg.Usage.Timezone)
			if err != nil {
				return err
			}
			client := codex.NewUsageClient(cfg)
			summary, err := client.Usage(context.Background(), codex.UsageOptions{Date: date})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), summary)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Provider: %s  Date: %s  Tokens: %s  Cost: $%.2f\n\n", summary.Provider, summary.Date, intValue(summary.TotalTokens), summary.CostUSD)
			table := newTable("REPO/CWD", "TYPE", "ACTIVE", "SESSIONS", "TOKENS", "COST")
			for _, group := range summary.Groups {
				table.Add(shortPath(group.Repo), group.Type, boolText(group.Active), fmt.Sprint(group.Sessions), intValue(group.Tokens), fmt.Sprintf("$%.2f", group.CostUSD))
			}
			table.Render(cmd.OutOrStdout())
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "codex", "usage provider")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	cmd.Flags().StringVar(&dateValue, "date", "", "date in YYYY-MM-DD format, default today in configured timezone")
	return cmd
}

func newUsageSessionsCommand(root *rootOptions) *cobra.Command {
	var provider string
	var jsonOut bool
	var dateValue string
	var limit int
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Show highest-token sessions for a day",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadAppConfig(root)
			if err != nil {
				return err
			}
			if provider == "" {
				provider = "codex"
			}
			if provider != "codex" {
				return fmt.Errorf("usage sessions provider %q is not implemented yet", provider)
			}
			if !cfg.Usage.Providers["codex"].Enabled {
				return fmt.Errorf("codex provider is disabled")
			}
			date, err := parseDate(dateValue, cfg.Usage.Timezone)
			if err != nil {
				return err
			}
			client := codex.NewUsageClient(cfg)
			sessions, err := client.Sessions(context.Background(), codex.UsageOptions{
				Date:  date,
				Limit: limit,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), sessions)
			}
			table := newTable("SESSION", "REPO/CWD", "TYPE", "ACTIVE", "TOKENS", "COST", "LAST PROMPT")
			for _, session := range sessions {
				table.Add(session.SessionID, shortPath(session.Repo), session.Type, boolText(session.Active), intValue(session.Tokens), fmt.Sprintf("$%.2f", session.CostUSD), session.LastPrompt)
			}
			table.Render(cmd.OutOrStdout())
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "codex", "usage provider")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	cmd.Flags().StringVar(&dateValue, "date", "", "date in YYYY-MM-DD format, default today in configured timezone")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum sessions to show, use 0 for all")
	return cmd
}

func newUsageTUICommand(root *rootOptions) *cobra.Command {
	var provider string
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive usage dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadAppConfig(root)
			if err != nil {
				return err
			}
			if provider == "" {
				provider = "codex"
			}
			if provider != "codex" {
				return fmt.Errorf("usage tui provider %q is not implemented yet", provider)
			}
			if !cfg.Usage.Providers["codex"].Enabled {
				return fmt.Errorf("codex provider is disabled")
			}
			program := tea.NewProgram(usagetui.New(cfg), tea.WithAltScreen(), tea.WithMouseCellMotion())
			_, err = program.Run()
			return err
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "codex", "usage provider")
	return cmd
}

func newUsageHerdrPublisherCommand(root *rootOptions) *cobra.Command {
	var provider string
	var mode string
	var interval time.Duration
	var source string
	var herdrCommand string
	var once bool
	var dryRun bool
	var forceRefresh bool
	cmd := &cobra.Command{
		Use:   "herdr-publisher",
		Short: "Publish compact usage labels to Herdr pane metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadAppConfig(root)
			if err != nil {
				return err
			}
			if provider == "" {
				provider = "codex"
			}
			if provider != "codex" {
				return fmt.Errorf("usage herdr-publisher provider %q is not implemented yet", provider)
			}
			if !cfg.Usage.Providers["codex"].Enabled {
				return fmt.Errorf("codex provider is disabled")
			}
			publisher := herdrpub.NewPublisher(cfg, herdrpub.PublisherOptions{
				Mode:       mode,
				Interval:   interval,
				Source:     source,
				Command:    herdrCommand,
				Once:       once,
				DryRun:     dryRun,
				ForceLimit: forceRefresh,
				Out:        cmd.OutOrStdout(),
			})
			return publisher.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "codex", "usage provider")
	cmd.Flags().StringVar(&mode, "mode", herdrpub.ModeLimit, "publisher mode: limit, usage, or auto")
	cmd.Flags().DurationVar(&interval, "interval", 30*time.Second, "refresh interval")
	cmd.Flags().StringVar(&source, "source", "local:agent-tools:herdr-publisher", "Herdr metadata source id")
	cmd.Flags().StringVar(&herdrCommand, "herdr-command", "herdr", "Herdr command")
	cmd.Flags().BoolVar(&once, "once", false, "publish once and exit")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print metadata reports instead of calling Herdr")
	cmd.Flags().BoolVar(&forceRefresh, "force-refresh", false, "bypass Codex limit cache once")
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

func parseDate(value, timezone string) (time.Time, error) {
	location := configLocation(timezone)
	if strings.TrimSpace(value) == "" {
		now := time.Now().In(location)
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location), nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q, expected YYYY-MM-DD", value)
	}
	return parsed, nil
}

func intValue(value int64) string {
	return fmt.Sprintf("%d", value)
}

func shortPath(value string) string {
	if value == "" {
		return "unknown"
	}
	const max = 48
	if len(value) <= max {
		return value
	}
	return "..." + value[len(value)-max+3:]
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
