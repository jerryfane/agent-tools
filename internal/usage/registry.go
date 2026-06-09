package usage

import (
	"fmt"
	"sort"

	"github.com/jerryfane/agent-tools/internal/config"
)

type Registry struct {
	providers []ProviderSummary
}

func NewRegistryFromConfig(cfg config.Config) *Registry {
	summaries := make([]ProviderSummary, 0, len(cfg.Usage.Providers))
	for name, provider := range cfg.Usage.Providers {
		detail := "disabled"
		if provider.Enabled {
			detail = "enabled"
			if len(provider.Profiles) > 0 {
				detail = fmt.Sprintf("enabled, %d configured profile(s)", len(provider.Profiles))
			}
		}
		summaries = append(summaries, ProviderSummary{
			Name:    name,
			Enabled: provider.Enabled,
			Detail:  detail,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Name < summaries[j].Name
	})
	return &Registry{providers: summaries}
}

func (r *Registry) List() []ProviderSummary {
	out := make([]ProviderSummary, len(r.providers))
	copy(out, r.providers)
	return out
}
