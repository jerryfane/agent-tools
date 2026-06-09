package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jerryfane/agent-tools/internal/codex"
	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/usage"
)

const defaultSessionType = "other"

type UsageClient struct {
	cfg     config.Config
	now     func() time.Time
	psLines func() ([]string, error)
}

type UsageOptions struct {
	Date  time.Time
	Limit int
}

func NewUsageClient(cfg config.Config) *UsageClient {
	return &UsageClient{
		cfg:     cfg,
		now:     time.Now,
		psLines: defaultProcessLines,
	}
}

func (c *UsageClient) Usage(ctx context.Context, opts UsageOptions) (usage.UsageSummary, error) {
	date := c.date(opts.Date)
	provider := c.provider()
	daily, err := c.runCCUsageDaily(ctx, provider, date)
	if err != nil {
		return usage.UsageSummary{}, err
	}
	sessions, err := c.Sessions(ctx, UsageOptions{Date: date})
	if err != nil {
		return usage.UsageSummary{}, err
	}
	return usage.UsageSummary{
		Provider:    "claude",
		Date:        date.Format("2006-01-02"),
		TotalTokens: daily.Totals.TotalTokens,
		CostUSD:     daily.Totals.TotalCost,
		Groups:      groupSessions(sessions),
	}, nil
}

func (c *UsageClient) Sessions(ctx context.Context, opts UsageOptions) ([]usage.SessionUsage, error) {
	date := c.date(opts.Date)
	provider := c.provider()
	// ccusage's `claude session` subcommand does not honor a single-day
	// --since/--until window (it returns nothing), so fetch all sessions and
	// filter client-side by last-activity date in the configured timezone, to
	// match how `claude daily` groups by day.
	raw, err := c.runCCUsageSessions(ctx, provider)
	if err != nil {
		return nil, err
	}
	loc := configLocation(c.cfg.Usage.Timezone)
	target := date.In(loc).Format("2006-01-02")
	out := make([]usage.SessionUsage, 0, len(raw.Sessions))
	for _, item := range raw.Sessions {
		if !sessionOnDate(item.LastActivity, loc, target) {
			continue
		}
		repo := item.ProjectPath
		if repo == "" {
			repo = "unknown"
		}
		// Claude Code processes do not expose the session UUID in their argv, so
		// v1 cannot correlate sessions to running processes; Active stays false.
		out = append(out, usage.SessionUsage{
			Provider:  "claude",
			SessionID: item.SessionID,
			Repo:      repo,
			Type:      defaultSessionType,
			Tokens:    item.TotalTokens,
			CostUSD:   item.TotalCost,
			Active:    false,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Tokens > out[j].Tokens
	})
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

// Active reports running Claude Code processes. It matches on the executable
// path (not the full command line) so commands that merely reference a
// ~/.claude path in their arguments are not misreported. Session correlation is
// not available, so SessionID is empty.
func (c *UsageClient) Active(ctx context.Context) ([]usage.ActiveSession, error) {
	lines, err := c.psLines()
	if err != nil {
		return nil, err
	}
	out := []usage.ActiveSession{}
	seen := map[int]bool{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		if !isClaudeExecutable(fields[1]) {
			continue
		}
		if seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, usage.ActiveSession{
			Provider:  "claude",
			SessionID: "",
			PID:       pid,
			Command:   strings.Join(fields[1:], " "),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PID < out[j].PID
	})
	return out, nil
}

// isClaudeExecutable reports whether a process executable path belongs to Claude
// Code (the launcher at .../bin/claude or a versioned binary under .../claude/).
func isClaudeExecutable(exe string) bool {
	if filepath.Base(exe) == "claude" {
		return true
	}
	return strings.Contains(exe, "/claude/") || strings.HasSuffix(exe, "/claude")
}

func (c *UsageClient) date(value time.Time) time.Time {
	if !value.IsZero() {
		return value
	}
	return c.now().In(configLocation(c.cfg.Usage.Timezone))
}

func (c *UsageClient) provider() config.ProviderConfig {
	return c.cfg.Usage.Providers["claude"]
}

func (c *UsageClient) runCCUsageDaily(ctx context.Context, provider config.ProviderConfig, date time.Time) (ccusageDailyResponse, error) {
	var out ccusageDailyResponse
	if err := c.runCCUsage(ctx, provider, "daily", &date, &out); err != nil {
		return ccusageDailyResponse{}, err
	}
	return out, nil
}

func (c *UsageClient) runCCUsageSessions(ctx context.Context, provider config.ProviderConfig) (ccusageSessionResponse, error) {
	var out ccusageSessionResponse
	if err := c.runCCUsage(ctx, provider, "session", nil, &out); err != nil {
		return ccusageSessionResponse{}, err
	}
	return out, nil
}

func (c *UsageClient) runCCUsage(ctx context.Context, provider config.ProviderConfig, subcommand string, date *time.Time, out any) error {
	if !provider.CCUsageEnabled {
		return errors.New("claude ccusage integration is disabled; enable usage.providers.claude.ccusage_enabled")
	}
	parts, err := codex.SplitCommand(firstNonEmpty(provider.CCUsageCommand, "ccusage"))
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return errors.New("claude ccusage command is empty")
	}
	args := append([]string{}, parts[1:]...)
	args = append(args, "claude", subcommand, "--json")
	if date != nil {
		day := date.Format("2006-01-02")
		args = append(args, "--since", day, "--until", day)
	}
	if tz := strings.TrimSpace(c.cfg.Usage.Timezone); tz != "" && !strings.EqualFold(tz, "local") {
		args = append(args, "--timezone", tz)
	}
	cmd := exec.CommandContext(ctx, parts[0], args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ccusage command %q failed: %w; install ccusage or set usage.providers.claude.ccusage_command = \"npx --yes ccusage\"; stderr: %s", provider.CCUsageCommand, err, strings.TrimSpace(stderr.String()))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("ccusage %s JSON decode failed: %w", subcommand, err)
	}
	return nil
}

type ccusageDailyResponse struct {
	Daily  []ccusageDaily `json:"daily"`
	Totals ccusageTotals  `json:"totals"`
}

type ccusageSessionResponse struct {
	Sessions []ccusageSession `json:"sessions"`
	Totals   ccusageTotals    `json:"totals"`
}

type ccusageDaily struct {
	Date        string  `json:"date"`
	TotalTokens int64   `json:"totalTokens"`
	TotalCost   float64 `json:"totalCost"`
}

type ccusageTotals struct {
	InputTokens         int64   `json:"inputTokens"`
	OutputTokens        int64   `json:"outputTokens"`
	CacheCreationTokens int64   `json:"cacheCreationTokens"`
	CacheReadTokens     int64   `json:"cacheReadTokens"`
	TotalTokens         int64   `json:"totalTokens"`
	TotalCost           float64 `json:"totalCost"`
}

type ccusageSession struct {
	SessionID    string  `json:"sessionId"`
	ProjectPath  string  `json:"projectPath"`
	LastActivity string  `json:"lastActivity"`
	TotalTokens  int64   `json:"totalTokens"`
	TotalCost    float64 `json:"totalCost"`
}

func groupSessions(sessions []usage.SessionUsage) []usage.UsageGroup {
	type key struct {
		provider string
		repo     string
		typ      string
		active   bool
	}
	groups := map[key]usage.UsageGroup{}
	for _, session := range sessions {
		k := key{provider: session.Provider, repo: session.Repo, typ: session.Type, active: session.Active}
		group := groups[k]
		group.Provider = session.Provider
		group.Repo = session.Repo
		group.Type = session.Type
		group.Active = session.Active
		group.Sessions++
		group.Tokens += session.Tokens
		group.CostUSD += session.CostUSD
		groups[k] = group
	}
	out := make([]usage.UsageGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, group)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Tokens > out[j].Tokens
	})
	return out
}

func defaultProcessLines() ([]string, error) {
	cmd := exec.Command("ps", "-eo", "pid=,args=")
	data, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	return lines, nil
}

// sessionOnDate reports whether a session's last-activity timestamp (RFC3339,
// UTC) falls on the target YYYY-MM-DD date in the given location. Unparseable or
// empty timestamps are treated as not matching.
func sessionOnDate(lastActivity string, loc *time.Location, target string) bool {
	if strings.TrimSpace(lastActivity) == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, lastActivity)
	if err != nil {
		return false
	}
	return t.In(loc).Format("2006-01-02") == target
}

func configLocation(name string) *time.Location {
	if name == "" || strings.EqualFold(name, "local") {
		return time.Local
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return location
}
