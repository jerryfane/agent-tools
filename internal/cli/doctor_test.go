package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/usage"
)

func TestRunDoctorReportsConfiguredCodexProfile(t *testing.T) {
	dir := t.TempDir()
	authHome := filepath.Join(dir, "codex-home")
	if err := os.MkdirAll(authHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authHome, "auth.json"), []byte(`{"tokens":{"access_token":"x","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["codex"]
	provider.CCUsageEnabled = false
	provider.Profiles = map[string]config.ProfileConfig{
		"default": {Home: authHome, Label: "codex"},
	}
	cfg.Usage.Providers["codex"] = provider

	checks := runDoctor(cfg)
	if !hasDoctorCheck(checks, "codex profiles", true) {
		t.Fatalf("expected codex profiles check to pass, got %+v", checks)
	}
	if !hasDoctorCheck(checks, "codex auth default", true) {
		t.Fatalf("expected codex auth check to pass, got %+v", checks)
	}
}

func TestRunDoctorRejectsInvalidCodexAuth(t *testing.T) {
	dir := t.TempDir()
	authHome := filepath.Join(dir, "codex-home")
	if err := os.MkdirAll(authHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authHome, "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["codex"]
	provider.CCUsageEnabled = false
	provider.Profiles = map[string]config.ProfileConfig{
		"default": {Home: authHome, Label: "codex"},
	}
	cfg.Usage.Providers["codex"] = provider

	checks := runDoctor(cfg)
	if !hasDoctorCheck(checks, "codex auth default", false) {
		t.Fatalf("expected codex auth check to fail, got %+v", checks)
	}
}

func TestRunDoctorUsesCCUsageCommandParser(t *testing.T) {
	cfg := config.Defaults()
	provider := cfg.Usage.Providers["codex"]
	provider.CCUsageEnabled = true
	provider.CCUsageCommand = `"sh" -c ccusage`
	provider.Profiles = map[string]config.ProfileConfig{}
	cfg.Usage.Providers["codex"] = provider

	checks := runDoctor(cfg)
	if !hasDoctorCheck(checks, "ccusage command", true) {
		t.Fatalf("expected quoted command check to pass, got %+v", checks)
	}
}

func TestRunDoctorDefaultsEmptyCCUsageCommand(t *testing.T) {
	cfg := config.Defaults()
	provider := cfg.Usage.Providers["codex"]
	provider.CCUsageEnabled = true
	provider.CCUsageCommand = ""
	provider.Profiles = map[string]config.ProfileConfig{}
	cfg.Usage.Providers["codex"] = provider

	check := findDoctorCheck(runDoctor(cfg), "ccusage command")
	if check == nil {
		t.Fatal("expected ccusage command check")
	}
	if strings.Contains(check.Message, "empty command") {
		t.Fatalf("expected empty command to fall back to ccusage, got %+v", check)
	}
}

func hasDoctorCheck(checks []usage.Check, name string, ok bool) bool {
	for _, check := range checks {
		if check.Name == name && check.OK == ok {
			return true
		}
	}
	return false
}

func findDoctorCheck(checks []usage.Check, name string) *usage.Check {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}
