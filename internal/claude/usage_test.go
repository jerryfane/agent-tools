package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jerryfane/agent-tools/internal/config"
)

func writeFakeCCUsage(t *testing.T, argsFile string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-ccusage")
	body := `#!/bin/sh
echo "$@" >> "` + argsFile + `"
if [ "$2" = "daily" ]; then
  cat <<'JSON'
{"daily":[{"date":"2026-06-08","totalTokens":956723,"totalCost":0.74}],"totals":{"totalTokens":956723,"totalCost":0.74}}
JSON
else
  cat <<'JSON'
{"sessions":[{"sessionId":"uuid-low","projectPath":"-root-app","totalTokens":500,"totalCost":0.10},{"sessionId":"uuid-high","projectPath":"-root-other","totalTokens":1500,"totalCost":0.30}],"totals":{"totalTokens":2000,"totalCost":0.40}}
JSON
fi
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func newUsageConfig(t *testing.T, ccusageCommand string) config.Config {
	t.Helper()
	cfg := config.Defaults()
	provider := cfg.Usage.Providers["claude"]
	provider.CCUsageEnabled = true
	provider.CCUsageCommand = ccusageCommand
	cfg.Usage.Providers["claude"] = provider
	return cfg
}

func TestUsageInvokesClaudeSubcommandAndBuildsSummary(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeCCUsage(t, argsFile)
	cfg := newUsageConfig(t, script)

	client := NewUsageClient(cfg)
	client.now = func() time.Time { return time.UnixMilli(fixedNowMillis).UTC() }
	client.psLines = func() ([]string, error) { return nil, nil }

	date := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	summary, err := client.Usage(context.Background(), UsageOptions{Date: date})
	if err != nil {
		t.Fatalf("Usage returned error: %v", err)
	}
	if summary.Provider != "claude" {
		t.Fatalf("unexpected provider: %q", summary.Provider)
	}
	if summary.TotalTokens != 956723 || summary.CostUSD != 0.74 {
		t.Fatalf("unexpected totals: %+v", summary)
	}
	if len(summary.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(summary.Groups))
	}
	if summary.Groups[0].Tokens != 1500 || summary.Groups[0].Repo != "-root-other" {
		t.Fatalf("expected highest-token group first, got %+v", summary.Groups[0])
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(data)
	if strings.Contains(got, "codex") {
		t.Fatalf("ccusage must not be invoked with the codex subcommand: %q", got)
	}
	if !strings.Contains(got, "claude daily --json --since 2026-06-08 --until 2026-06-08") {
		t.Fatalf("unexpected daily args: %q", got)
	}
	if !strings.Contains(got, "claude session --json --since 2026-06-08 --until 2026-06-08") {
		t.Fatalf("unexpected session args: %q", got)
	}
}

func TestSessionsSortAndLimit(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	cfg := newUsageConfig(t, writeFakeCCUsage(t, argsFile))
	client := NewUsageClient(cfg)
	client.now = func() time.Time { return time.UnixMilli(fixedNowMillis).UTC() }

	sessions, err := client.Sessions(context.Background(), UsageOptions{Date: time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC), Limit: 1})
	if err != nil {
		t.Fatalf("Sessions returned error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected limit of 1, got %d", len(sessions))
	}
	if sessions[0].SessionID != "uuid-high" || sessions[0].Provider != "claude" {
		t.Fatalf("expected highest-token session first, got %+v", sessions[0])
	}
	if sessions[0].Active {
		t.Fatalf("expected Active=false in v1, got true")
	}
}

func TestActiveMatchesClaudeExecutableOnly(t *testing.T) {
	cfg := config.Defaults()
	client := NewUsageClient(cfg)
	client.psLines = func() ([]string, error) {
		return []string{
			"111 /root/.local/share/claude/versions/2.1.170 --foo",
			"222 cat /root/.claude/settings.json",
			"333 /root/.local/bin/claude",
			"444 /usr/bin/codex resume abc",
			"",
		}, nil
	}
	active, err := client.Active(context.Background())
	if err != nil {
		t.Fatalf("Active returned error: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 claude processes, got %d: %+v", len(active), active)
	}
	if active[0].PID != 111 || active[1].PID != 333 {
		t.Fatalf("unexpected active PIDs: %+v", active)
	}
}

func TestRunCCUsageDisabled(t *testing.T) {
	cfg := config.Defaults() // claude provider has CCUsageEnabled=false by default
	client := NewUsageClient(cfg)
	_, err := client.Sessions(context.Background(), UsageOptions{Date: time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)})
	if err == nil || !strings.Contains(err.Error(), "ccusage integration is disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
}
