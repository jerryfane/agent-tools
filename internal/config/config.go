package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/knadh/koanf"
)

const envPrefix = "AGENT_TOOLS_"

type Config struct {
	Usage UsageConfig `koanf:"usage" json:"usage"`
}

type UsageConfig struct {
	Timezone              string                    `koanf:"timezone" json:"timezone"`
	RefreshLimitsSeconds  int                       `koanf:"refresh_limits_seconds" json:"refresh_limits_seconds"`
	RefreshUsageSeconds   int                       `koanf:"refresh_usage_seconds" json:"refresh_usage_seconds"`
	RefreshProcessSeconds int                       `koanf:"refresh_process_seconds" json:"refresh_process_seconds"`
	Providers             map[string]ProviderConfig `koanf:"providers" json:"providers"`
}

type ProviderConfig struct {
	Enabled        bool                     `koanf:"enabled" json:"enabled"`
	DefaultProfile string                   `koanf:"default_profile" json:"default_profile,omitempty"`
	CCUsageEnabled bool                     `koanf:"ccusage_enabled" json:"ccusage_enabled,omitempty"`
	CCUsageCommand string                   `koanf:"ccusage_command" json:"ccusage_command,omitempty"`
	CacheDir       string                   `koanf:"cache_dir" json:"cache_dir,omitempty"`
	LimitsURL      string                   `koanf:"limits_url" json:"limits_url,omitempty"`
	TokenURL       string                   `koanf:"token_url" json:"token_url,omitempty"`
	Profiles       map[string]ProfileConfig `koanf:"profiles" json:"profiles,omitempty"`
}

type ProfileConfig struct {
	Home  string `koanf:"home" json:"home"`
	Label string `koanf:"label" json:"label"`
}

func DefaultPath() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "agent-tools", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".config", "agent-tools", "config.toml")
	}
	return filepath.Join(home, ".config", "agent-tools", "config.toml")
}

func Defaults() Config {
	return Config{
		Usage: UsageConfig{
			Timezone:              "Local",
			RefreshLimitsSeconds:  int((5 * time.Minute).Seconds()),
			RefreshUsageSeconds:   int(time.Minute.Seconds()),
			RefreshProcessSeconds: int((15 * time.Second).Seconds()),
			Providers: map[string]ProviderConfig{
				"codex": {
					Enabled:        true,
					DefaultProfile: "default",
					CCUsageEnabled: true,
					CCUsageCommand: "ccusage",
					Profiles:       map[string]ProfileConfig{},
				},
				"claude": {
					Enabled:        true,
					DefaultProfile: "default",
					CCUsageEnabled: true,
					CCUsageCommand: "ccusage",
					Profiles:       map[string]ProfileConfig{},
				},
			},
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	k := koanf.New(".")
	if err := k.Load(confmapProvider(cfg), nil); err != nil {
		return Config{}, err
	}

	configPath := path
	if configPath == "" {
		configPath = DefaultPath()
	}
	if _, err := os.Stat(configPath); err == nil {
		if err := k.Load(fileProvider(configPath), tomlParser{}); err != nil {
			return Config{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}

	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok || !strings.HasPrefix(key, envPrefix) {
			continue
		}
		key = strings.TrimPrefix(key, envPrefix)
		key = strings.ToLower(strings.ReplaceAll(key, "__", "."))
		if err := k.Set(key, value); err != nil {
			return Config{}, err
		}
	}

	if err := k.Unmarshal("", &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Usage.Providers == nil {
		cfg.Usage.Providers = map[string]ProviderConfig{}
	}
	return cfg, nil
}
