package herdr

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/providers"
	"github.com/jerryfane/agent-tools/internal/usage"
)

func TestBuildReportsPublishesLimitLabels(t *testing.T) {
	five := 57.0
	week := 69.0
	reports := BuildReports(
		[]Pane{{PaneID: "p1", Agent: "codex", Label: "work jerryf"}, {PaneID: "p2", Agent: "bash"}},
		[]providers.ProfileInfo{{Name: "jerryf", Home: "/root/.codex-jerryf", Label: "jf"}},
		[]usage.LimitSnapshot{{Provider: "codex", Profile: "jerryf", FiveHourRemainingPercent: &five, WeeklyRemainingPercent: &week}},
		usage.UsageSummary{},
		nil,
		BuildOptions{Mode: ModeLimit, Source: "test", TTL: time.Minute, DefaultProfile: "jerryf", Seq: 10},
	)
	if len(reports) != 1 {
		t.Fatalf("expected one report, got %+v", reports)
	}
	report := reports[0]
	if report.Labels["working"] != "jf 57%5h 69%wk" {
		t.Fatalf("unexpected working label: %q", report.Labels["working"])
	}
	if !report.ClearDisplay {
		t.Fatal("expected default profile display-agent to be cleared")
	}
	if !report.AgentGuard {
		t.Fatal("expected codex pane report to keep agent guard")
	}
}

func TestBuildReportsUsesProcProfileOverride(t *testing.T) {
	reports := BuildReports(
		[]Pane{{PaneID: "p1", Agent: "codex", Label: "manager"}},
		[]providers.ProfileInfo{{Name: "default", Home: "/root/.codex"}, {Name: "spark", Home: "/root/.codex-spark"}},
		[]usage.LimitSnapshot{{Provider: "codex", Profile: "spark"}},
		usage.UsageSummary{},
		map[string]string{"p1": "spark"},
		BuildOptions{Mode: ModeLimit, Source: "test", TTL: time.Minute, DefaultProfile: "default", Seq: 10},
	)
	if len(reports) != 1 {
		t.Fatalf("expected one report, got %+v", reports)
	}
	if reports[0].Profile != "spark" || reports[0].DisplayAgent != "codex-spark" {
		t.Fatalf("unexpected report profile/display: %+v", reports[0])
	}
}

func TestBuildReportsPublishesProcMappedPaneWithoutAgent(t *testing.T) {
	reports := BuildReports(
		[]Pane{{PaneID: "p1", Label: "shell"}},
		[]providers.ProfileInfo{{Name: "default", Home: "/root/.codex"}, {Name: "spark", Home: "/root/.codex-spark"}},
		[]usage.LimitSnapshot{{Provider: "codex", Profile: "spark"}},
		usage.UsageSummary{},
		map[string]string{"p1": "spark"},
		BuildOptions{Mode: ModeLimit, Source: "test", TTL: time.Minute, DefaultProfile: "default", Seq: 10},
	)
	if len(reports) != 1 {
		t.Fatalf("expected proc-mapped pane to be published, got %+v", reports)
	}
	if reports[0].Profile != "spark" || reports[0].DisplayAgent != "codex-spark" {
		t.Fatalf("unexpected proc-mapped report: %+v", reports[0])
	}
	if reports[0].AgentGuard {
		t.Fatal("expected proc-mapped non-agent pane report to omit agent guard")
	}
}

func TestBuildReportsIgnoresStaleDisplayAgent(t *testing.T) {
	reports := BuildReports(
		[]Pane{{PaneID: "p1", Agent: "codex", Label: "manager", DisplayAgent: "codex-spark"}},
		[]providers.ProfileInfo{{Name: "default", Home: "/root/.codex"}, {Name: "spark", Home: "/root/.codex-spark"}},
		[]usage.LimitSnapshot{{Provider: "codex", Profile: "default"}},
		usage.UsageSummary{},
		nil,
		BuildOptions{Mode: ModeLimit, Source: "test", TTL: time.Minute, DefaultProfile: "default", Seq: 10},
	)
	if len(reports) != 1 {
		t.Fatalf("expected one report, got %+v", reports)
	}
	if reports[0].Profile != "default" || !reports[0].ClearDisplay {
		t.Fatalf("unexpected report from stale display-agent: %+v", reports[0])
	}
}

func TestBuildReportsInfersProfileFromHerdrTitle(t *testing.T) {
	reports := BuildReports(
		[]Pane{{PaneID: "p1", Agent: "codex", Title: "manager spark"}},
		[]providers.ProfileInfo{{Name: "default", Home: "/root/.codex"}, {Name: "spark", Home: "/root/.codex-spark"}},
		[]usage.LimitSnapshot{{Provider: "codex", Profile: "spark"}},
		usage.UsageSummary{},
		nil,
		BuildOptions{Mode: ModeLimit, Source: "test", TTL: time.Minute, DefaultProfile: "default", Seq: 10},
	)
	if len(reports) != 1 {
		t.Fatalf("expected one report, got %+v", reports)
	}
	if reports[0].Profile != "spark" || reports[0].DisplayAgent != "codex-spark" {
		t.Fatalf("unexpected title-inferred report: %+v", reports[0])
	}
}

func TestBuildReportsUsageModeMatchesPaneCWD(t *testing.T) {
	reports := BuildReports(
		[]Pane{{PaneID: "p1", Agent: "codex", CWD: "/root/repo"}},
		[]providers.ProfileInfo{{Name: "default", Home: "/root/.codex", Label: "codex"}},
		nil,
		usage.UsageSummary{
			Date:        "2026-06-09",
			TotalTokens: 10_000,
			CostUSD:     1,
			Groups: []usage.UsageGroup{{
				Repo:    "/root/repo",
				Tokens:  1_250_000,
				CostUSD: 0.42,
			}},
		},
		nil,
		BuildOptions{Mode: ModeUsage, Source: "test", TTL: time.Minute, DefaultProfile: "default", Seq: 10},
	)
	if got := reports[0].Labels["idle"]; got != "codex 1.2Mtok $0.42" {
		t.Fatalf("unexpected usage label: %q", got)
	}
}

func TestBuildReportsUsageModeDoesNotFallbackToGlobalForUnmatchedCWD(t *testing.T) {
	reports := BuildReports(
		[]Pane{{PaneID: "p1", Agent: "codex", CWD: "/root/other"}},
		[]providers.ProfileInfo{{Name: "default", Home: "/root/.codex", Label: "codex"}},
		nil,
		usage.UsageSummary{
			Date:        "2026-06-09",
			TotalTokens: 10_000,
			CostUSD:     1,
			Groups: []usage.UsageGroup{{
				Repo:    "/root/repo",
				Tokens:  1_250_000,
				CostUSD: 0.42,
			}},
		},
		nil,
		BuildOptions{Mode: ModeUsage, Source: "test", TTL: time.Minute, DefaultProfile: "default", Seq: 10},
	)
	if got := reports[0].Labels["idle"]; got != "codex 0tok $0.00" {
		t.Fatalf("unexpected unmatched cwd usage label: %q", got)
	}
}

func TestAutoModeKeepsLowQuotaVisible(t *testing.T) {
	five := 5.0
	reports := BuildReports(
		[]Pane{{PaneID: "p1", Agent: "codex"}},
		[]providers.ProfileInfo{{Name: "default", Home: "/root/.codex"}},
		[]usage.LimitSnapshot{{Provider: "codex", Profile: "default", FiveHourRemainingPercent: &five}},
		usage.UsageSummary{Date: "2026-06-09", TotalTokens: 1_000, CostUSD: 0.01},
		nil,
		BuildOptions{Mode: ModeAuto, Source: "test", TTL: time.Minute, DefaultProfile: "default", Seq: 10},
	)
	if got := reports[0].Labels["blocked"]; got != "default 5%5h ??%wk" {
		t.Fatalf("unexpected auto label: %q", got)
	}
}

func TestBuildReportsHandlesClaudeProvider(t *testing.T) {
	five := 42.0
	reports := BuildReports(
		[]Pane{{PaneID: "p1", Agent: "claude", Label: "work crazyj"}, {PaneID: "p2", Agent: "codex", Label: "work jerryf"}},
		[]providers.ProfileInfo{{Name: "crazyj", Home: "/root/.claude-crazyj", Label: "cj"}},
		[]usage.LimitSnapshot{{Provider: "claude", Profile: "crazyj", FiveHourRemainingPercent: &five}},
		usage.UsageSummary{},
		nil,
		BuildOptions{Provider: "claude", Mode: ModeLimit, Source: "test", TTL: time.Minute, DefaultProfile: "default", Seq: 10},
	)
	if len(reports) != 1 {
		t.Fatalf("expected only the claude pane, got %+v", reports)
	}
	report := reports[0]
	if report.Provider != "claude" || report.DisplayAgent != "claude-crazyj" || !report.AgentGuard {
		t.Fatalf("unexpected claude report: %+v", report)
	}
	if report.Labels["working"] != "cj 42%5h ??%wk" {
		t.Fatalf("unexpected claude label: %q", report.Labels["working"])
	}
}

func TestProviderActiveMatchesAgentOrProcMapping(t *testing.T) {
	panes := []Pane{{PaneID: "p1", Agent: "codex"}, {PaneID: "p2", Label: "shell"}}
	if !providerActive("codex", panes, nil) {
		t.Fatal("expected codex active via agent")
	}
	if providerActive("claude", panes, nil) {
		t.Fatal("expected claude inactive without a matching pane")
	}
	if !providerActive("claude", panes, map[string]string{"p2": "default"}) {
		t.Fatal("expected claude active via proc mapping")
	}
}

func TestTakeForceLimitConsumesFlagOnce(t *testing.T) {
	publisher := NewPublisher(config.Defaults(), PublisherOptions{ForceLimit: true})
	if !publisher.takeForceLimit() {
		t.Fatal("expected first force-limit read to be true")
	}
	if publisher.takeForceLimit() {
		t.Fatal("expected force-limit flag to be consumed")
	}
}

func TestPublishOnceReturnsErrorWhenReportsFail(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "codex-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "fake-herdr")
	body := `#!/bin/sh
if [ "$1 $2" = "pane list" ]; then
  echo '{"result":{"panes":[{"pane_id":"p1","agent":"codex","label":"work"}]}}'
  exit 0
fi
echo report failed >&2
exit 42
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	provider := cfg.Usage.Providers["codex"]
	provider.CacheDir = filepath.Join(dir, "cache")
	provider.Profiles = map[string]config.ProfileConfig{
		"default": {Home: home, Label: "codex"},
	}
	cfg.Usage.Providers["codex"] = provider

	var out bytes.Buffer
	publisher := NewPublisher(cfg, PublisherOptions{
		Command:  script,
		Once:     true,
		Interval: time.Second,
		Out:      &out,
	})
	err := publisher.PublishOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "publisher report failed for 1/1 pane(s)") {
		t.Fatalf("expected report failure error, got %v", err)
	}
	if !strings.Contains(out.String(), "publisher report error for pane p1") {
		t.Fatalf("expected logged report error, got %q", out.String())
	}
}

// With claude enabled by default but no claude pane present, the publisher must
// still produce codex reports and must not touch claude's network endpoint.
func TestPublishOnceDryRunSkipsInactiveProvider(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "codex-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "fake-herdr")
	body := `#!/bin/sh
if [ "$1 $2" = "pane list" ]; then
  echo '{"result":{"panes":[{"pane_id":"p1","agent":"codex","label":"work"}]}}'
  exit 0
fi
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults() // codex AND claude enabled
	codexProvider := cfg.Usage.Providers["codex"]
	codexProvider.CacheDir = filepath.Join(dir, "codex-cache")
	codexProvider.Profiles = map[string]config.ProfileConfig{"default": {Home: home, Label: "codex"}}
	cfg.Usage.Providers["codex"] = codexProvider

	var out bytes.Buffer
	publisher := NewPublisher(cfg, PublisherOptions{
		Command:  script,
		Once:     true,
		DryRun:   true,
		Interval: time.Second,
		Out:      &out,
	})
	if err := publisher.PublishOnce(context.Background()); err != nil {
		t.Fatalf("PublishOnce returned error: %v", err)
	}
	if !strings.Contains(out.String(), `"provider": "codex"`) {
		t.Fatalf("expected a codex report, got %q", out.String())
	}
	if strings.Contains(out.String(), "claude collect error") {
		t.Fatalf("claude has no pane and must be skipped, not collected: %q", out.String())
	}
}
