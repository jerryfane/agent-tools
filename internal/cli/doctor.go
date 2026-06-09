package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jerryfane/agent-tools/internal/claude"
	"github.com/jerryfane/agent-tools/internal/codex"
	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/usage"
	"github.com/spf13/cobra"
)

func newUsageDoctorCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check local usage-tool configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadAppConfig(root)
			if err != nil {
				return err
			}
			checks := runDoctor(cfg)
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), checks)
			}
			table := newTable("CHECK", "OK", "MESSAGE")
			for _, check := range checks {
				table.Add(check.Name, boolText(check.OK), check.Message)
			}
			table.Render(cmd.OutOrStdout())
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	return cmd
}

func runDoctor(cfg config.Config) []usage.Check {
	checks := []usage.Check{{
		Name:    "config",
		OK:      true,
		Message: "loaded",
	}}
	timezone := cfg.Usage.Timezone
	if timezone == "" {
		timezone = "Local"
	}
	if _, err := time.LoadLocation(cfg.Usage.Timezone); cfg.Usage.Timezone == "" || strings.EqualFold(cfg.Usage.Timezone, "local") || err == nil {
		checks = append(checks, usage.Check{Name: "timezone", OK: true, Message: timezone})
	} else {
		checks = append(checks, usage.Check{Name: "timezone", OK: false, Message: err.Error()})
	}

	checks = append(checks, codexChecks(cfg)...)
	checks = append(checks, claudeChecks(cfg)...)
	return appendOptionalChecks(checks)
}

func codexChecks(cfg config.Config) []usage.Check {
	provider := cfg.Usage.Providers["codex"]
	if !provider.Enabled {
		return []usage.Check{{Name: "codex provider", OK: true, Message: "disabled"}}
	}
	checks := []usage.Check{{Name: "codex provider", OK: true, Message: "enabled"}}
	profiles, err := codex.DiscoverProfiles(cfg)
	if err != nil {
		checks = append(checks, usage.Check{Name: "codex profiles", OK: false, Message: err.Error()})
	} else {
		checks = append(checks, usage.Check{Name: "codex profiles", OK: len(profiles) > 0, Message: fmt.Sprintf("%d profile(s)", len(profiles))})
		for _, profile := range profiles {
			authPath := filepath.Join(profile.Home, "auth.json")
			if err := validateCodexAuth(authPath); err == nil {
				checks = append(checks, usage.Check{Name: "codex auth " + profile.Name, OK: true, Message: authPath})
			} else {
				checks = append(checks, usage.Check{Name: "codex auth " + profile.Name, OK: false, Message: authPath + ": " + err.Error()})
			}
		}
	}
	return append(checks, providerCCUsageCheck("codex", provider))
}

func claudeChecks(cfg config.Config) []usage.Check {
	provider := cfg.Usage.Providers["claude"]
	if !provider.Enabled {
		return []usage.Check{{Name: "claude provider", OK: true, Message: "disabled"}}
	}
	checks := []usage.Check{{Name: "claude provider", OK: true, Message: "enabled"}}
	profiles, err := claude.DiscoverProfiles(cfg)
	if err != nil {
		checks = append(checks, usage.Check{Name: "claude profiles", OK: false, Message: err.Error()})
	} else {
		checks = append(checks, usage.Check{Name: "claude profiles", OK: len(profiles) > 0, Message: fmt.Sprintf("%d profile(s)", len(profiles))})
		for _, profile := range profiles {
			credPath := filepath.Join(profile.Home, ".credentials.json")
			ok, message := validateClaudeCreds(credPath)
			checks = append(checks, usage.Check{Name: "claude auth " + profile.Name, OK: ok, Message: message})
		}
	}
	return append(checks, providerCCUsageCheck("claude", provider))
}

func providerCCUsageCheck(name string, provider config.ProviderConfig) usage.Check {
	checkName := name + " ccusage"
	if !provider.CCUsageEnabled {
		return usage.Check{Name: checkName, OK: true, Message: "disabled"}
	}
	command := provider.CCUsageCommand
	if command == "" {
		command = "ccusage"
	}
	parts, err := codex.SplitCommand(command)
	if err != nil {
		return usage.Check{Name: checkName, OK: false, Message: err.Error()}
	}
	if len(parts) == 0 {
		return usage.Check{Name: checkName, OK: false, Message: "empty command"}
	}
	if path, err := exec.LookPath(parts[0]); err == nil {
		return usage.Check{Name: checkName, OK: true, Message: path}
	}
	return usage.Check{Name: checkName, OK: false, Message: fmt.Sprintf("%q not found; install ccusage or set ccusage_command", parts[0])}
}

func appendOptionalChecks(checks []usage.Check) []usage.Check {
	if path, err := exec.LookPath("herdr"); err == nil {
		return append(checks, usage.Check{Name: "herdr command", OK: true, Message: path})
	}
	return append(checks, usage.Check{Name: "herdr command", OK: true, Message: "not installed; optional unless using usage herdr-publisher"})
}

func validateCodexAuth(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var auth struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return fmt.Errorf("invalid JSON")
	}
	if auth.Tokens.AccessToken == "" {
		return fmt.Errorf("missing access_token")
	}
	if auth.Tokens.AccountID == "" {
		return fmt.Errorf("missing account_id")
	}
	return nil
}

// validateClaudeCreds checks a Claude Code credentials file without ever
// printing token values. Returns (ok, message) where message is a path or a
// short, secret-free status.
func validateClaudeCreds(path string) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && runtime.GOOS == "darwin" {
			return false, path + ": not found (macOS Keychain credentials are not yet supported)"
		}
		if os.IsNotExist(err) {
			return false, path + ": not found (run `claude` to sign in)"
		}
		return false, path + ": " + err.Error()
	}
	var file struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return false, path + ": invalid JSON"
	}
	if file.ClaudeAiOauth.AccessToken == "" {
		return false, path + ": missing accessToken"
	}
	if file.ClaudeAiOauth.ExpiresAt > 0 && file.ClaudeAiOauth.ExpiresAt < time.Now().UnixMilli() {
		return true, path + " (access token expired; will refresh on next use)"
	}
	return true, path
}
