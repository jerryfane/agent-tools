package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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

	codexProvider := cfg.Usage.Providers["codex"]
	if !codexProvider.Enabled {
		checks = append(checks, usage.Check{Name: "codex provider", OK: true, Message: "disabled"})
		return appendOptionalChecks(checks)
	}
	checks = append(checks, usage.Check{Name: "codex provider", OK: true, Message: "enabled"})

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

	if codexProvider.CCUsageEnabled {
		ccusageCommand := codexProvider.CCUsageCommand
		if ccusageCommand == "" {
			ccusageCommand = "ccusage"
		}
		parts, err := codex.SplitCommand(ccusageCommand)
		if err != nil {
			checks = append(checks, usage.Check{Name: "ccusage command", OK: false, Message: err.Error()})
		} else if len(parts) == 0 {
			checks = append(checks, usage.Check{Name: "ccusage command", OK: false, Message: "empty command"})
		} else if path, err := exec.LookPath(parts[0]); err == nil {
			checks = append(checks, usage.Check{Name: "ccusage command", OK: true, Message: path})
		} else {
			checks = append(checks, usage.Check{Name: "ccusage command", OK: false, Message: fmt.Sprintf("%q not found; install ccusage or set ccusage_command", parts[0])})
		}
	} else {
		checks = append(checks, usage.Check{Name: "ccusage command", OK: true, Message: "disabled"})
	}

	return appendOptionalChecks(checks)
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
