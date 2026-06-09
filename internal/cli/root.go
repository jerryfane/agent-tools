package cli

import (
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
	return cmd
}

func loadAppConfig(root *rootOptions) (config.Config, error) {
	return config.Load(root.configPath)
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
