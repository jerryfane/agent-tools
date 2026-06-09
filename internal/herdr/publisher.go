package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/providers"
	"github.com/jerryfane/agent-tools/internal/usage"
)

const (
	ModeLimit = "limit"
	ModeUsage = "usage"
	ModeAuto  = "auto"
)

type PublisherOptions struct {
	Mode       string
	Interval   time.Duration
	Source     string
	Command    string
	Once       bool
	DryRun     bool
	ForceLimit bool
	// Provider optionally restricts publishing to a single provider. Empty
	// means publish every enabled provider that has matching panes.
	Provider string
	Out      io.Writer
}

type Publisher struct {
	cfg     config.Config
	options PublisherOptions
	now     func() time.Time
}

type Pane struct {
	PaneID        string            `json:"pane_id"`
	Agent         string            `json:"agent"`
	DisplayAgent  string            `json:"display_agent"`
	Label         string            `json:"label"`
	Title         string            `json:"title"`
	CWD           string            `json:"cwd"`
	ForegroundCWD string            `json:"foreground_cwd"`
	StateLabels   map[string]string `json:"state_labels,omitempty"`
}

type Report struct {
	PaneID       string            `json:"pane_id"`
	Provider     string            `json:"provider,omitempty"`
	Profile      string            `json:"profile"`
	DisplayAgent string            `json:"display_agent,omitempty"`
	ClearDisplay bool              `json:"clear_display_agent,omitempty"`
	AgentGuard   bool              `json:"agent_guard,omitempty"`
	Labels       map[string]string `json:"state_labels"`
	Source       string            `json:"source"`
	TTL          time.Duration     `json:"ttl"`
	Seq          int64             `json:"seq"`
}

type paneListResponse struct {
	Result struct {
		Panes []Pane `json:"panes"`
	} `json:"result"`
}

func NewPublisher(cfg config.Config, options PublisherOptions) *Publisher {
	if options.Mode == "" {
		options.Mode = ModeLimit
	}
	if options.Interval <= 0 {
		options.Interval = 30 * time.Second
	}
	if options.Source == "" {
		options.Source = "local:agent-tools:herdr-publisher"
	}
	if options.Command == "" {
		options.Command = "herdr"
	}
	if options.Out == nil {
		options.Out = io.Discard
	}
	return &Publisher{cfg: cfg, options: options, now: time.Now}
}

func (p *Publisher) Run(ctx context.Context) error {
	if err := validateMode(p.options.Mode); err != nil {
		return err
	}
	if p.options.Once {
		return p.PublishOnce(ctx)
	}
	for {
		if err := p.PublishOnce(ctx); err != nil {
			fmt.Fprintf(p.options.Out, "publisher refresh error: %v\n", err)
		}
		timer := time.NewTimer(p.options.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *Publisher) PublishOnce(ctx context.Context) error {
	panes, err := p.listPanes(ctx)
	if err != nil {
		return err
	}

	seq := p.now().UnixNano()
	labelPrefixes := parseLabelPrefixes(os.Getenv("CODEX_USAGE_LABEL_PREFIXES"))
	force := p.takeForceLimit()
	var reports []Report
	var firstCollectErr error
	// Each provider is collected independently: one provider failing (e.g. a
	// rate-limited claude endpoint) must not stop another provider's panes from
	// being published.
	for _, name := range p.targetProviders() {
		profiles, err := providers.ProfilesFor(p.cfg, name)
		if err != nil {
			firstCollectErr = recordCollectError(p.options.Out, firstCollectErr, name, err)
			continue
		}
		if len(profiles) == 0 {
			continue
		}
		procProfiles := discoverPaneProfiles(name, profiles)
		// Skip a provider with no matching panes: there is nothing to annotate,
		// and we must not call its (rate-limited) quota endpoint needlessly.
		if !providerActive(name, panes, procProfiles) {
			continue
		}
		limits, summary, err := p.collectData(ctx, name, force)
		if err != nil {
			firstCollectErr = recordCollectError(p.options.Out, firstCollectErr, name, err)
			continue
		}
		reports = append(reports, BuildReports(panes, profiles, limits, summary, procProfiles, BuildOptions{
			Provider:       name,
			Mode:           p.options.Mode,
			Source:         p.options.Source,
			TTL:            p.options.Interval * 3,
			DefaultProfile: p.defaultProfile(name, profiles),
			LabelPrefixes:  labelPrefixes,
			Seq:            seq,
		})...)
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].PaneID < reports[j].PaneID
	})
	// Only surface a collection error when no provider produced anything;
	// otherwise the healthy providers' reports are published and the failure was
	// already logged.
	if len(reports) == 0 {
		if firstCollectErr != nil {
			return firstCollectErr
		}
		if p.options.DryRun {
			return writeJSON(p.options.Out, reports)
		}
		return nil
	}
	if p.options.DryRun {
		return writeJSON(p.options.Out, reports)
	}
	failures := 0
	var firstErr error
	for _, report := range reports {
		if err := p.report(ctx, report); err != nil {
			failures++
			if firstErr == nil {
				firstErr = err
			}
			fmt.Fprintf(p.options.Out, "publisher report error for pane %s: %v\n", report.PaneID, err)
		}
	}
	if failures > 0 && (p.options.Once || failures == len(reports)) {
		return fmt.Errorf("publisher report failed for %d/%d pane(s): %w", failures, len(reports), firstErr)
	}
	return nil
}

// recordCollectError logs a per-provider collection failure and keeps the first
// one so PublishOnce can surface it when no provider produced any reports.
func recordCollectError(out io.Writer, first error, name string, err error) error {
	fmt.Fprintf(out, "publisher %s collect error: %v\n", name, err)
	if first == nil {
		return err
	}
	return first
}

func (p *Publisher) targetProviders() []string {
	if p.options.Provider != "" {
		return []string{p.options.Provider}
	}
	return providers.Enabled(p.cfg)
}

// providerActive reports whether any listed pane is annotated by this provider,
// either by its agent name or a process-environment profile mapping.
func providerActive(name string, panes []Pane, procProfiles map[string]string) bool {
	for _, pane := range panes {
		if pane.Agent == name || procProfiles[pane.PaneID] != "" {
			return true
		}
	}
	return false
}

func (p *Publisher) collectData(ctx context.Context, name string, force bool) ([]usage.LimitSnapshot, usage.UsageSummary, error) {
	var limits []usage.LimitSnapshot
	var limitsErr error
	if p.options.Mode == ModeLimit || p.options.Mode == ModeAuto {
		limits, limitsErr = providers.LimitsFor(ctx, p.cfg, name, force)
	}
	var summary usage.UsageSummary
	var usageErr error
	if p.options.Mode == ModeUsage || p.options.Mode == ModeAuto {
		summary, usageErr = providers.UsageFor(ctx, p.cfg, name, time.Time{})
	}
	switch p.options.Mode {
	case ModeLimit:
		if limitsErr != nil {
			return nil, usage.UsageSummary{}, fmt.Errorf("collect %s limits: %w", name, limitsErr)
		}
	case ModeUsage:
		if usageErr != nil {
			return nil, usage.UsageSummary{}, fmt.Errorf("collect %s usage: %w", name, usageErr)
		}
		if summary.Date == "" {
			return nil, usage.UsageSummary{}, fmt.Errorf("collect %s usage: empty usage summary", name)
		}
	case ModeAuto:
		if limitsErr != nil {
			if usageErr != nil {
				return nil, usage.UsageSummary{}, fmt.Errorf("collect %s limits and usage: limits: %v; usage: %v", name, limitsErr, usageErr)
			}
			return nil, usage.UsageSummary{}, fmt.Errorf("collect %s limits: %w", name, limitsErr)
		}
	}
	return limits, summary, nil
}

func (p *Publisher) takeForceLimit() bool {
	force := p.options.ForceLimit
	p.options.ForceLimit = false
	return force
}

type BuildOptions struct {
	Provider       string
	Mode           string
	Source         string
	TTL            time.Duration
	DefaultProfile string
	LabelPrefixes  map[string]string
	Seq            int64
}

func BuildReports(panes []Pane, profiles []providers.ProfileInfo, limits []usage.LimitSnapshot, summary usage.UsageSummary, procProfiles map[string]string, opts BuildOptions) []Report {
	provider := opts.Provider
	if provider == "" {
		provider = "codex"
	}
	profileNames := map[string]bool{}
	profileLabels := map[string]string{}
	for _, profile := range profiles {
		profileNames[profile.Name] = true
		profileLabels[profile.Name] = firstNonEmpty(opts.LabelPrefixes[profile.Name], profile.Label, profile.Name)
	}
	limitsByProfile := map[string]usage.LimitSnapshot{}
	for _, item := range limits {
		limitsByProfile[item.Profile] = item
	}
	reports := []Report{}
	for _, pane := range panes {
		mapped := procProfiles[pane.PaneID]
		if pane.Agent != provider && mapped == "" {
			continue
		}
		profile := inferProfile(pane, profileNames, opts.DefaultProfile)
		if mapped != "" && profileNames[mapped] {
			profile = mapped
		}
		if profile == "" {
			continue
		}
		prefix := firstNonEmpty(profileLabels[profile], profile)
		status := compactStatus(opts.Mode, pane, profile, limitsByProfile[profile], summary)
		text := strings.TrimSpace(prefix + " " + status)
		labels := map[string]string{}
		for _, state := range []string{"working", "idle", "blocked", "done", "unknown"} {
			labels[state] = text
		}
		report := Report{
			PaneID:     pane.PaneID,
			Provider:   provider,
			Profile:    profile,
			AgentGuard: pane.Agent == provider,
			Labels:     labels,
			Source:     opts.Source,
			TTL:        opts.TTL,
			Seq:        opts.Seq,
		}
		if profile == opts.DefaultProfile && pane.Agent == provider {
			report.ClearDisplay = true
		} else {
			report.DisplayAgent = provider + "-" + profile
		}
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].PaneID < reports[j].PaneID
	})
	return reports
}

func (p *Publisher) listPanes(ctx context.Context) ([]Pane, error) {
	cmd := exec.CommandContext(ctx, p.options.Command, "pane", "list")
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s pane list failed: %w", p.options.Command, err)
	}
	var out paneListResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%s pane list JSON decode failed: %w", p.options.Command, err)
	}
	return out.Result.Panes, nil
}

func (p *Publisher) report(ctx context.Context, report Report) error {
	args := []string{
		"pane", "report-metadata", report.PaneID,
		"--source", report.Source,
	}
	if report.AgentGuard {
		args = append(args, "--agent", firstNonEmpty(report.Provider, "codex"))
	}
	if report.ClearDisplay {
		args = append(args, "--clear-display-agent")
	} else if report.DisplayAgent != "" {
		args = append(args, "--display-agent", report.DisplayAgent)
	}
	args = append(args, "--clear-custom-status")
	for _, state := range []string{"working", "idle", "blocked", "done", "unknown"} {
		args = append(args, "--state-label", state+"="+report.Labels[state])
	}
	args = append(args, "--seq", fmt.Sprint(report.Seq), "--ttl-ms", fmt.Sprint(report.TTL.Milliseconds()))
	cmd := exec.CommandContext(ctx, p.options.Command, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s failed: %w: %s", p.options.Command, strings.Join(args[:3], " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (p *Publisher) defaultProfile(name string, profiles []providers.ProfileInfo) string {
	configured := p.cfg.Usage.Providers[name].DefaultProfile
	for _, profile := range profiles {
		if profile.Name == configured {
			return configured
		}
	}
	if len(profiles) == 0 {
		return ""
	}
	return profiles[0].Name
}

func compactStatus(mode string, pane Pane, profile string, limit usage.LimitSnapshot, summary usage.UsageSummary) string {
	limitStatus := compactLimit(limit)
	usageStatus := compactUsage(pane, summary)
	switch mode {
	case ModeUsage:
		return firstNonEmpty(usageStatus, limitStatus)
	case ModeAuto:
		if limit.Error != "" || isLow(limit.FiveHourRemainingPercent) || isLow(limit.WeeklyRemainingPercent) {
			return limitStatus
		}
		return firstNonEmpty(usageStatus, limitStatus)
	default:
		return limitStatus
	}
}

func compactLimit(item usage.LimitSnapshot) string {
	return fmt.Sprintf("%s5h %swk", percent(item.FiveHourRemainingPercent), percent(item.WeeklyRemainingPercent))
}

func compactUsage(pane Pane, summary usage.UsageSummary) string {
	if summary.Date == "" {
		return ""
	}
	targets := map[string]bool{}
	for _, value := range []string{pane.ForegroundCWD, pane.CWD} {
		if strings.TrimSpace(value) != "" {
			targets[value] = true
		}
	}
	var tokens int64
	var cost float64
	for _, group := range summary.Groups {
		if targets[group.Repo] {
			tokens += group.Tokens
			cost += group.CostUSD
		}
	}
	if tokens == 0 && len(targets) == 0 && summary.TotalTokens > 0 {
		tokens = summary.TotalTokens
		cost = summary.CostUSD
	}
	return fmt.Sprintf("%s $%.2f", compactTokens(tokens), cost)
}

func inferProfile(pane Pane, profileNames map[string]bool, fallback string) string {
	text := strings.ToLower(firstNonEmpty(pane.Title, pane.Label))
	names := make([]string, 0, len(profileNames))
	for name := range profileNames {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) == len(names[j]) {
			return names[i] < names[j]
		}
		return len(names[i]) > len(names[j])
	})
	for _, name := range names {
		if strings.Contains(text, strings.ToLower(name)) {
			return name
		}
	}
	return fallback
}

// providerEnvKeys returns the process-environment variable names that map a
// pane to a profile for a given provider: an explicit Herdr profile var and the
// provider's home/config-dir var.
func providerEnvKeys(name string) (profileEnv, homeEnv string) {
	switch name {
	case "claude":
		return "HERDR_CLAUDE_PROFILE", "CLAUDE_CONFIG_DIR"
	default:
		return "HERDR_CODEX_PROFILE", "CODEX_HOME"
	}
}

func discoverPaneProfiles(name string, profiles []providers.ProfileInfo) map[string]string {
	profileEnv, homeEnv := providerEnvKeys(name)
	homeToProfile := map[string]string{}
	profileNames := map[string]bool{}
	for _, profile := range profiles {
		homeToProfile[cleanPath(profile.Home)] = profile.Name
		profileNames[profile.Name] = true
	}
	out := map[string]string{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() || !isPID(entry.Name()) {
			continue
		}
		env, err := readProcEnv(filepath.Join("/proc", entry.Name(), "environ"), profileEnv, homeEnv)
		if err != nil {
			continue
		}
		paneID := env["HERDR_PANE_ID"]
		if paneID == "" {
			continue
		}
		if profile := env[profileEnv]; profileNames[profile] {
			out[paneID] = profile
			continue
		}
		if profile := homeToProfile[cleanPath(env[homeEnv])]; profile != "" {
			out[paneID] = profile
		}
	}
	return out
}

func readProcEnv(path string, keys ...string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{"HERDR_PANE_ID": true}
	for _, key := range keys {
		wanted[key] = true
	}
	out := map[string]string{}
	for _, item := range bytes.Split(data, []byte{0}) {
		key, value, ok := bytes.Cut(item, []byte("="))
		if !ok {
			continue
		}
		name := string(key)
		if wanted[name] {
			out[name] = string(value)
		}
	}
	return out, nil
}

func validateMode(mode string) error {
	switch mode {
	case ModeLimit, ModeUsage, ModeAuto:
		return nil
	default:
		return fmt.Errorf("invalid herdr publisher mode %q", mode)
	}
}

func parseLabelPrefixes(value string) map[string]string {
	out := map[string]string{}
	for _, item := range strings.Split(value, ",") {
		key, label, ok := strings.Cut(strings.TrimSpace(item), "=")
		if ok && strings.TrimSpace(key) != "" {
			out[strings.TrimSpace(key)] = strings.TrimSpace(label)
		}
	}
	return out
}

func percent(value *float64) string {
	if value == nil {
		return "??%"
	}
	return fmt.Sprintf("%.0f%%", *value)
}

func compactTokens(value int64) string {
	switch {
	case value >= 1_000_000_000:
		return fmt.Sprintf("%.1fBtok", float64(value)/1_000_000_000)
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fMtok", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fKtok", float64(value)/1_000)
	default:
		return fmt.Sprintf("%dtok", value)
	}
}

func isLow(value *float64) bool {
	return value != nil && *value <= 15
}

func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func isPID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
