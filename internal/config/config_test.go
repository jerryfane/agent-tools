package config

import "testing"

func TestLoadMissingConfigUsesPublicSafeDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir() + "/missing.toml")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Usage.Timezone == "" {
		t.Fatal("expected default timezone")
	}
	codex := cfg.Usage.Providers["codex"]
	if !codex.Enabled {
		t.Fatal("expected codex provider enabled by default")
	}
	if codex.CCUsageCommand != "ccusage" {
		t.Fatalf("unexpected ccusage command: %q", codex.CCUsageCommand)
	}
	claude := cfg.Usage.Providers["claude"]
	if !claude.Enabled {
		t.Fatal("expected claude provider enabled by default")
	}
	if claude.CCUsageCommand != "ccusage" {
		t.Fatalf("unexpected claude ccusage command: %q", claude.CCUsageCommand)
	}
}
