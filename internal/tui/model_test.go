package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/usage"
)

func TestModelRendersSidebarPages(t *testing.T) {
	model := New(config.Defaults())
	model.limits = []usage.LimitSnapshot{{Provider: "codex", Profile: "default", Source: "cache"}}
	model.summary = usage.UsageSummary{Provider: "codex", Date: "2026-06-09", TotalTokens: 10}
	model.sessions = []usage.SessionUsage{{Provider: "codex", SessionID: "019e91f4-6e02-7fe1-99d6-1d1cc675ddd0", Repo: "/root/repo", Type: "goal/resume", Tokens: 10}}
	model.providers = []usage.ProviderSummary{{Name: "codex", Enabled: true, Detail: "enabled"}}
	model.viewport.SetContent(model.content())

	for _, want := range []string{"PROFILE", "Date 2026-06-09", "/root/repo", "enabled", "No alerts"} {
		view := model.View()
		if !strings.Contains(view, want) {
			t.Fatalf("expected view to contain %q, got %q", want, view)
		}
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
		var ok bool
		model, ok = next.(Model)
		if !ok {
			t.Fatalf("unexpected model type %T", next)
		}
	}
}

func TestPageNavigationDoesNotScrollNewPage(t *testing.T) {
	model := New(config.Defaults())
	model.viewport.SetContent(strings.Repeat("line\n", 40))
	model.viewport.ScrollDown(5)

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if updated.selected != 1 {
		t.Fatalf("expected next page selected, got %d", updated.selected)
	}
	if updated.viewport.YOffset != 0 {
		t.Fatalf("expected new page to start at top, got offset %d", updated.viewport.YOffset)
	}
}

func TestQueueLoadSuppressesOverlappingRefresh(t *testing.T) {
	model := New(config.Defaults())
	model.inFlight[pageSessions] = false

	if cmd := model.queueLoad(pageSessions); cmd == nil {
		t.Fatal("expected first sessions refresh to be queued")
	}
	if cmd := model.queueLoad(pageSessions); cmd != nil {
		t.Fatal("expected overlapping sessions refresh to be suppressed")
	}

	next, _ := model.Update(sessionsMsg{})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if updated.inFlight[pageSessions] {
		t.Fatal("expected sessions refresh to clear in-flight state")
	}
}

func TestCommandTimeoutIsBounded(t *testing.T) {
	if got := commandTimeout(300, 12*time.Second); got != 12*time.Second {
		t.Fatalf("expected cap timeout, got %s", got)
	}
	if got := commandTimeout(0, 12*time.Second); got != 12*time.Second {
		t.Fatalf("expected fallback timeout, got %s", got)
	}
	if got := commandTimeout(1, 12*time.Second); got != time.Second {
		t.Fatalf("expected one-second timeout, got %s", got)
	}
}

func TestLoadedEmptySessionsShowsEmptyState(t *testing.T) {
	model := New(config.Defaults())
	next, _ := model.Update(sessionsMsg{})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	updated.selected = int(pageSessions)
	updated.viewport.SetContent(updated.content())
	view := updated.View()
	if strings.Contains(view, "Loading sessions") {
		t.Fatalf("expected empty sessions state, got %q", view)
	}
	if !strings.Contains(view, "No Codex sessions") {
		t.Fatalf("expected empty sessions message, got %q", view)
	}
}
