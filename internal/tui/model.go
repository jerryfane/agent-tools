package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jerryfane/agent-tools/internal/codex"
	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/usage"
)

type page int

const (
	pageLimit page = iota
	pageUsage
	pageSessions
	pageProviders
	pageAlerts
)

var pages = []struct {
	page  page
	label string
}{
	{pageLimit, "Limit"},
	{pageUsage, "Usage"},
	{pageSessions, "Sessions"},
	{pageProviders, "Providers"},
	{pageAlerts, "Alerts"},
}

type Model struct {
	cfg      config.Config
	selected int
	width    int
	height   int
	viewport viewport.Model

	limits    []usage.LimitSnapshot
	summary   usage.UsageSummary
	sessions  []usage.SessionUsage
	providers []usage.ProviderSummary

	errors    map[page]string
	updatedAt map[page]time.Time
	inFlight  map[page]bool
}

func New(cfg config.Config) Model {
	vp := viewport.New(80, 20)
	model := Model{
		cfg:       cfg,
		width:     100,
		height:    30,
		viewport:  vp,
		errors:    map[page]string{},
		updatedAt: map[page]time.Time{},
		inFlight: map[page]bool{
			pageLimit:     true,
			pageUsage:     true,
			pageSessions:  true,
			pageProviders: true,
		},
	}
	model.resizeViewport()
	model.viewport.SetContent(model.content())
	return model
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadLimits(m.cfg),
		loadUsage(m.cfg),
		loadSessions(m.cfg),
		loadProviders(m.cfg),
		tick(pageLimit, refreshDuration(m.cfg.Usage.RefreshLimitsSeconds, 5*time.Minute)),
		tick(pageUsage, refreshDuration(m.cfg.Usage.RefreshUsageSeconds, time.Minute)),
		tick(pageSessions, refreshDuration(m.cfg.Usage.RefreshProcessSeconds, 15*time.Second)),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 && msg.Height > 0 {
			m.width = msg.Width
			m.height = msg.Height
			m.resizeViewport()
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "tab", "right", "down":
			m.selected = (m.selected + 1) % len(pages)
			m.viewport.GotoTop()
			m.viewport.SetContent(m.content())
			return m, tea.Batch(cmds...)
		case "shift+tab", "left", "up":
			m.selected--
			if m.selected < 0 {
				m.selected = len(pages) - 1
			}
			m.viewport.GotoTop()
			m.viewport.SetContent(m.content())
			return m, tea.Batch(cmds...)
		case "r":
			cmds = append(cmds, m.loadCurrentPage()...)
			m.viewport.SetContent(m.content())
			return m, tea.Batch(cmds...)
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	case limitsMsg:
		m.inFlight[pageLimit] = false
		if msg.err != nil {
			m.errors[pageLimit] = msg.err.Error()
		} else {
			delete(m.errors, pageLimit)
			m.limits = msg.items
			m.updatedAt[pageLimit] = msg.at
		}
	case usageMsg:
		m.inFlight[pageUsage] = false
		if msg.err != nil {
			m.errors[pageUsage] = msg.err.Error()
		} else {
			delete(m.errors, pageUsage)
			m.summary = msg.summary
			m.updatedAt[pageUsage] = msg.at
		}
	case sessionsMsg:
		m.inFlight[pageSessions] = false
		if msg.err != nil {
			m.errors[pageSessions] = msg.err.Error()
		} else {
			delete(m.errors, pageSessions)
			m.sessions = msg.items
			m.updatedAt[pageSessions] = msg.at
		}
	case providersMsg:
		m.inFlight[pageProviders] = false
		if msg.err != nil {
			m.errors[pageProviders] = msg.err.Error()
		} else {
			delete(m.errors, pageProviders)
			m.providers = msg.items
			m.updatedAt[pageProviders] = msg.at
		}
	case refreshMsg:
		if cmd := m.queueLoad(msg.page); cmd != nil {
			cmds = append(cmds, cmd)
		}
		switch msg.page {
		case pageLimit:
			cmds = append(cmds, tick(pageLimit, refreshDuration(m.cfg.Usage.RefreshLimitsSeconds, 5*time.Minute)))
		case pageUsage:
			cmds = append(cmds, tick(pageUsage, refreshDuration(m.cfg.Usage.RefreshUsageSeconds, time.Minute)))
		case pageSessions:
			cmds = append(cmds, tick(pageSessions, refreshDuration(m.cfg.Usage.RefreshProcessSeconds, 15*time.Second)))
		}
	}
	m.viewport.SetContent(m.content())
	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}
	sidebarWidth := sidebarWidth(m.width)
	sidebar := renderSidebar(m.selected, sidebarWidth, m.height)
	body := bodyStyle.Width(max(0, m.width-sidebarWidth-1)).Height(max(0, m.height)).Render(m.viewport.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, body)
}

func (m *Model) resizeViewport() {
	sidebar := sidebarWidth(m.width)
	m.viewport.Width = max(20, m.width-sidebar-3)
	m.viewport.Height = max(5, m.height-2)
}

func (m *Model) loadCurrentPage() []tea.Cmd {
	switch pages[m.selected].page {
	case pageLimit:
		return compactCmds(m.queueLoad(pageLimit))
	case pageUsage:
		return compactCmds(m.queueLoad(pageUsage))
	case pageSessions:
		return compactCmds(m.queueLoad(pageSessions))
	case pageProviders:
		return compactCmds(m.queueLoad(pageProviders))
	default:
		return compactCmds(
			m.queueLoad(pageLimit),
			m.queueLoad(pageUsage),
			m.queueLoad(pageSessions),
			m.queueLoad(pageProviders),
		)
	}
}

func (m *Model) queueLoad(p page) tea.Cmd {
	if m.inFlight[p] {
		return nil
	}
	m.inFlight[p] = true
	switch p {
	case pageLimit:
		return loadLimits(m.cfg)
	case pageUsage:
		return loadUsage(m.cfg)
	case pageSessions:
		return loadSessions(m.cfg)
	case pageProviders:
		return loadProviders(m.cfg)
	default:
		m.inFlight[p] = false
		return nil
	}
}

func compactCmds(cmds ...tea.Cmd) []tea.Cmd {
	out := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			out = append(out, cmd)
		}
	}
	return out
}

func (m Model) content() string {
	current := pages[m.selected].page
	var b strings.Builder
	title := pages[m.selected].label
	b.WriteString(titleStyle.Render(title))
	if updated, ok := m.updatedAt[current]; ok {
		b.WriteString("  ")
		b.WriteString(mutedStyle.Render("updated " + updated.Format("15:04:05")))
	}
	b.WriteString("\n\n")
	if errText := m.errors[current]; errText != "" {
		b.WriteString(errorStyle.Render(errText))
		b.WriteString("\n\n")
	}
	switch current {
	case pageLimit:
		b.WriteString(m.limitContent())
	case pageUsage:
		b.WriteString(m.usageContent())
	case pageSessions:
		b.WriteString(m.sessionsContent())
	case pageProviders:
		b.WriteString(m.providersContent())
	case pageAlerts:
		b.WriteString(m.alertsContent())
	}
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render("tab/up/down switch page  j/k or wheel scroll  r refresh  q quit"))
	return b.String()
}

func (m Model) limitContent() string {
	if len(m.limits) == 0 && m.errors[pageLimit] == "" {
		if _, loaded := m.updatedAt[pageLimit]; loaded || !m.inFlight[pageLimit] {
			return "No Codex limit snapshots from the latest refresh."
		}
		return mutedStyle.Render("Loading Codex limits...")
	}
	rows := [][]string{{"PROFILE", "5H LEFT", "WEEK LEFT", "5H RESET", "WEEK RESET", "SOURCE", "ERROR"}}
	loc := location(m.cfg.Usage.Timezone)
	for _, item := range m.limits {
		rows = append(rows, []string{
			firstNonEmpty(item.Label, item.Profile),
			percent(item.FiveHourRemainingPercent),
			percent(item.WeeklyRemainingPercent),
			timeValue(item.FiveHourResetAt, loc, false),
			timeValue(item.WeeklyResetAt, loc, true),
			item.Source,
			item.Error,
		})
	}
	return renderRows(rows)
}

func (m Model) usageContent() string {
	if m.summary.Date == "" && m.errors[pageUsage] == "" {
		if _, loaded := m.updatedAt[pageUsage]; loaded || !m.inFlight[pageUsage] {
			return "No usage totals from the latest refresh."
		}
		return mutedStyle.Render("Loading usage totals...")
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Date %s  Tokens %d  Cost $%.2f\n\n", m.summary.Date, m.summary.TotalTokens, m.summary.CostUSD))
	rows := [][]string{{"REPO/CWD", "TYPE", "ACTIVE", "SESSIONS", "TOKENS", "COST"}}
	for _, group := range m.summary.Groups {
		rows = append(rows, []string{
			shortPath(group.Repo, 54),
			group.Type,
			yesNo(group.Active),
			fmt.Sprint(group.Sessions),
			fmt.Sprint(group.Tokens),
			fmt.Sprintf("$%.2f", group.CostUSD),
		})
	}
	b.WriteString(renderRows(rows))
	return b.String()
}

func (m Model) sessionsContent() string {
	if len(m.sessions) == 0 && m.errors[pageSessions] == "" {
		if _, loaded := m.updatedAt[pageSessions]; loaded || !m.inFlight[pageSessions] {
			return "No Codex sessions from the latest refresh."
		}
		return mutedStyle.Render("Loading sessions...")
	}
	rows := [][]string{{"SESSION", "REPO/CWD", "TYPE", "ACTIVE", "TOKENS", "COST", "LAST PROMPT"}}
	for _, session := range m.sessions {
		rows = append(rows, []string{
			shortSession(session.SessionID),
			shortPath(session.Repo, 36),
			session.Type,
			yesNo(session.Active),
			fmt.Sprint(session.Tokens),
			fmt.Sprintf("$%.2f", session.CostUSD),
			truncate(session.LastPrompt, 72),
		})
	}
	return renderRows(rows)
}

func (m Model) providersContent() string {
	if len(m.providers) == 0 && m.errors[pageProviders] == "" {
		if _, loaded := m.updatedAt[pageProviders]; loaded || !m.inFlight[pageProviders] {
			return "No configured usage providers."
		}
		return mutedStyle.Render("Loading providers...")
	}
	rows := [][]string{{"PROVIDER", "ENABLED", "DETAIL"}}
	for _, provider := range m.providers {
		rows = append(rows, []string{provider.Name, yesNo(provider.Enabled), provider.Detail})
	}
	return renderRows(rows)
}

func (m Model) alertsContent() string {
	var alerts []string
	for _, item := range m.limits {
		if item.Error != "" {
			alerts = append(alerts, fmt.Sprintf("%s limit error: %s", firstNonEmpty(item.Label, item.Profile), item.Error))
		}
		if item.FiveHourRemainingPercent != nil && *item.FiveHourRemainingPercent <= 15 {
			alerts = append(alerts, fmt.Sprintf("%s low 5h quota: %.0f%% left", firstNonEmpty(item.Label, item.Profile), *item.FiveHourRemainingPercent))
		}
		if item.WeeklyRemainingPercent != nil && *item.WeeklyRemainingPercent <= 15 {
			alerts = append(alerts, fmt.Sprintf("%s low weekly quota: %.0f%% left", firstNonEmpty(item.Label, item.Profile), *item.WeeklyRemainingPercent))
		}
	}
	for _, session := range m.sessions {
		if session.Active && session.Tokens > 10_000_000 {
			alerts = append(alerts, fmt.Sprintf("High-burn active session %s in %s: %d tokens", shortSession(session.SessionID), shortPath(session.Repo, 42), session.Tokens))
		}
	}
	if m.errors[pageLimit] != "" {
		alerts = append(alerts, "Limit refresh error: "+m.errors[pageLimit])
	}
	if m.errors[pageUsage] != "" {
		alerts = append(alerts, "Usage refresh error: "+m.errors[pageUsage])
	}
	if m.errors[pageSessions] != "" {
		alerts = append(alerts, "Session refresh error: "+m.errors[pageSessions])
	}
	if len(alerts) == 0 {
		return "No alerts from the latest data."
	}
	var b strings.Builder
	for _, alert := range alerts {
		b.WriteString("- ")
		b.WriteString(alert)
		b.WriteByte('\n')
	}
	return b.String()
}

type limitsMsg struct {
	items []usage.LimitSnapshot
	err   error
	at    time.Time
}

type usageMsg struct {
	summary usage.UsageSummary
	err     error
	at      time.Time
}

type sessionsMsg struct {
	items []usage.SessionUsage
	err   error
	at    time.Time
}

type providersMsg struct {
	items []usage.ProviderSummary
	err   error
	at    time.Time
}

type refreshMsg struct {
	page page
}

func loadLimits(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout(cfg.Usage.RefreshLimitsSeconds, 45*time.Second))
		defer cancel()
		items, err := codex.NewLimitsClient(cfg).Limits(ctx, codex.LimitsOptions{})
		return limitsMsg{items: items, err: err, at: time.Now()}
	}
}

func loadUsage(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout(cfg.Usage.RefreshUsageSeconds, 45*time.Second))
		defer cancel()
		summary, err := codex.NewUsageClient(cfg).Usage(ctx, codex.UsageOptions{})
		return usageMsg{summary: summary, err: err, at: time.Now()}
	}
}

func loadSessions(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout(cfg.Usage.RefreshProcessSeconds, 12*time.Second))
		defer cancel()
		items, err := codex.NewUsageClient(cfg).Sessions(ctx, codex.UsageOptions{Limit: 50})
		return sessionsMsg{items: items, err: err, at: time.Now()}
	}
}

func loadProviders(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		return providersMsg{items: usage.NewRegistryFromConfig(cfg).List(), at: time.Now()}
	}
}

func tick(p page, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return refreshMsg{page: p}
	})
}

func refreshDuration(seconds int, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func commandTimeout(seconds int, cap time.Duration) time.Duration {
	duration := refreshDuration(seconds, cap)
	if duration > cap {
		return cap
	}
	if duration < time.Second {
		return time.Second
	}
	return duration
}
