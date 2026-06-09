package config

func confmapProvider(cfg Config) mapProvider {
	values := map[string]interface{}{
		"usage": map[string]interface{}{
			"timezone":                cfg.Usage.Timezone,
			"refresh_limits_seconds":  cfg.Usage.RefreshLimitsSeconds,
			"refresh_usage_seconds":   cfg.Usage.RefreshUsageSeconds,
			"refresh_process_seconds": cfg.Usage.RefreshProcessSeconds,
			"providers":               map[string]interface{}{},
		},
	}
	providers := values["usage"].(map[string]interface{})["providers"].(map[string]interface{})
	for name, provider := range cfg.Usage.Providers {
		providers[name] = map[string]interface{}{
			"enabled":         provider.Enabled,
			"default_profile": provider.DefaultProfile,
			"ccusage_enabled": provider.CCUsageEnabled,
			"ccusage_command": provider.CCUsageCommand,
			"profiles":        map[string]interface{}{},
		}
	}
	return mapProvider{values: values}
}
