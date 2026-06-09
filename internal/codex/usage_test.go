package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/usage"
)

func TestCCUsageSessionJSONNormalizes(t *testing.T) {
	raw := []byte(`{
		"sessions": [{
			"sessionId": "2026/06/09/rollout-2026-06-09T08-55-18-019eab2a-063b-7170-88ee-fb0412a6e438",
			"sessionFile": "rollout-2026-06-09T08-55-18-019eab2a-063b-7170-88ee-fb0412a6e438",
			"directory": "2026/06/09",
			"totalTokens": 1234,
			"costUSD": 0.42
		}],
		"totals": {"totalTokens": 1234, "costUSD": 0.42}
	}`)
	var parsed ccusageSessionResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal ccusage session JSON: %v", err)
	}
	if len(parsed.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(parsed.Sessions))
	}
	item := parsed.Sessions[0]
	if item.TotalTokens != 1234 || item.CostUSD != 0.42 {
		t.Fatalf("unexpected normalized session: %+v", item)
	}
	if got := sessionUUID(item.SessionID); got != "019eab2a-063b-7170-88ee-fb0412a6e438" {
		t.Fatalf("unexpected session UUID: %q", got)
	}
}

func TestSplitCommandSupportsQuotedArgs(t *testing.T) {
	parts, err := SplitCommand(`npx --yes "ccusage"`)
	if err != nil {
		t.Fatalf("splitCommand returned error: %v", err)
	}
	want := []string{"npx", "--yes", "ccusage"}
	if len(parts) != len(want) {
		t.Fatalf("unexpected parts length: got %+v want %+v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("part %d: got %q want %q", i, parts[i], want[i])
		}
	}
}

func TestSessionMetadataAndClassification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data := `{"type":"session_meta","payload":{"id":"019eab2a-063b-7170-88ee-fb0412a6e438","cwd":"/root/agent-tools","originator":"codex_exec","source":{"subagent":"review"}}}
{"type":"event_msg","payload":{"type":"user_message","message":"Review the current code changes (staged, unstaged, and untracked files)."}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Review the current code changes and provide prioritized findings."}]}}
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write session fixture: %v", err)
	}
	meta, err := readSessionMetadata(path)
	if err != nil {
		t.Fatalf("readSessionMetadata returned error: %v", err)
	}
	if meta.CWD != "/root/agent-tools" {
		t.Fatalf("unexpected cwd: %q", meta.CWD)
	}
	if got := classifySession(meta); got != "review" {
		t.Fatalf("unexpected classification: %q", got)
	}
	if meta.LastPrompt == "" {
		t.Fatal("expected last prompt preview")
	}
}

func TestClassifyGoalResume(t *testing.T) {
	meta := sessionMetadata{UserText: "Continue working toward the active thread goal using docs/goals/GOAL.md"}
	if got := classifySession(meta); got != "goal/resume" {
		t.Fatalf("unexpected classification: %q", got)
	}
}

func TestActiveParsesResumeProcessIDs(t *testing.T) {
	cfg := config.Defaults()
	client := NewUsageClient(cfg)
	client.psLines = func() ([]string, error) {
		return []string{
			"123 codex resume 019e91f4-6e02-7fe1-99d6-1d1cc675ddd0 --yolo",
			"124 rg codex",
		}, nil
	}
	active, err := client.Active(context.Background())
	if err != nil {
		t.Fatalf("Active returned error: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("expected one active session, got %+v", active)
	}
	if active[0].SessionID != "019e91f4-6e02-7fe1-99d6-1d1cc675ddd0" || active[0].PID != 123 {
		t.Fatalf("unexpected active session: %+v", active[0])
	}
}

func TestUsageGroupsByRepoTypeAndActive(t *testing.T) {
	groups := groupSessions(usageSessionFixtures{
		{repo: "/root/a", typ: "review", active: true, tokens: 10, cost: 1},
		{repo: "/root/a", typ: "review", active: true, tokens: 15, cost: 2},
		{repo: "/root/a", typ: "review", active: false, tokens: 30, cost: 3},
	}.sessions())
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %+v", groups)
	}
	if groups[0].Tokens != 30 || groups[0].Active {
		t.Fatalf("expected inactive largest group first, got %+v", groups[0])
	}
}

type usageSessionFixture struct {
	repo   string
	typ    string
	active bool
	tokens int64
	cost   float64
}

type usageSessionFixtures []usageSessionFixture

func (items usageSessionFixtures) sessions() []usage.SessionUsage {
	out := make([]usage.SessionUsage, 0, len(items))
	for _, item := range items {
		out = append(out, usage.SessionUsage{
			Provider: "codex",
			Repo:     item.repo,
			Type:     item.typ,
			Active:   item.active,
			Tokens:   item.tokens,
			CostUSD:  item.cost,
		})
	}
	return out
}
