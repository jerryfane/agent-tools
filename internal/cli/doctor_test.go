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
	if !hasDoctorCheck(checks, "codex ccusage", true) {
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

	check := findDoctorCheck(runDoctor(cfg), "codex ccusage")
	if check == nil {
		t.Fatal("expected codex ccusage check")
	}
	if strings.Contains(check.Message, "empty command") {
		t.Fatalf("expected empty command to fall back to ccusage, got %+v", check)
	}
}

func TestRunDoctorReportsConfiguredClaudeProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "claude-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"SUPER-SECRET-TOKEN","refreshToken":"r","expiresAt":99999999999999}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["claude"]
	provider.CCUsageEnabled = false
	provider.Profiles = map[string]config.ProfileConfig{
		"default": {Home: home, Label: "claude"},
	}
	cfg.Usage.Providers["claude"] = provider

	checks := runDoctor(cfg)
	if !hasDoctorCheck(checks, "claude profiles", true) {
		t.Fatalf("expected claude profiles check to pass, got %+v", checks)
	}
	if !hasDoctorCheck(checks, "claude auth default", true) {
		t.Fatalf("expected claude auth check to pass, got %+v", checks)
	}
	for _, check := range checks {
		if strings.Contains(check.Message, "SUPER-SECRET-TOKEN") {
			t.Fatalf("doctor leaked a token value in a message: %+v", check)
		}
	}
}

func TestRunDoctorRejectsInvalidClaudeCreds(t *testing.T) {
	home := filepath.Join(t.TempDir(), "claude-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".credentials.json"), []byte(`{"claudeAiOauth":{"refreshToken":"r"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["claude"]
	provider.CCUsageEnabled = false
	provider.Profiles = map[string]config.ProfileConfig{
		"default": {Home: home, Label: "claude"},
	}
	cfg.Usage.Providers["claude"] = provider

	if !hasDoctorCheck(runDoctor(cfg), "claude auth default", false) {
		t.Fatalf("expected claude auth check to fail for missing accessToken")
	}
}

func TestRunDoctorReportsClaudeDisabled(t *testing.T) {
	cfg := config.Defaults()
	provider := cfg.Usage.Providers["claude"]
	provider.Enabled = false
	cfg.Usage.Providers["claude"] = provider

	check := findDoctorCheck(runDoctor(cfg), "claude provider")
	if check == nil || check.Message != "disabled" {
		t.Fatalf("expected claude provider disabled check, got %+v", check)
	}
	if findDoctorCheck(runDoctor(cfg), "claude profiles") != nil {
		t.Fatalf("expected no claude profiles check when disabled")
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
