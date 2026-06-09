package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jerryfane/agent-tools/internal/config"
	"github.com/jerryfane/agent-tools/internal/usage"
)

const defaultSessionType = "other"
const sessionTailBytes int64 = 4 * 1024 * 1024

var sessionUUIDPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

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
		Provider:    "codex",
		Date:        date.Format("2006-01-02"),
		TotalTokens: daily.Totals.TotalTokens,
		CostUSD:     daily.Totals.CostUSD,
		Groups:      groupSessions(sessions),
	}, nil
}

func (c *UsageClient) Sessions(ctx context.Context, opts UsageOptions) ([]usage.SessionUsage, error) {
	date := c.date(opts.Date)
	provider := c.provider()
	raw, err := c.runCCUsageSessions(ctx, provider, date)
	if err != nil {
		return nil, err
	}
	active, _ := c.Active(ctx)
	activeByID := map[string]bool{}
	for _, item := range active {
		activeByID[item.SessionID] = true
	}
	out := make([]usage.SessionUsage, 0, len(raw.Sessions))
	for _, item := range raw.Sessions {
		meta := c.readSessionMetadata(item)
		id := sessionUUID(firstNonEmpty(item.SessionID, item.SessionFile))
		active := activeByID[id] || activeByID[item.SessionID]
		repo := meta.CWD
		if repo == "" {
			repo = "unknown"
		}
		sessionType := classifySession(meta)
		out = append(out, usage.SessionUsage{
			Provider:   "codex",
			SessionID:  idOrFallback(id, item.SessionID),
			Repo:       repo,
			Type:       sessionType,
			Tokens:     item.TotalTokens,
			CostUSD:    item.CostUSD,
			Active:     active,
			LastPrompt: meta.LastPrompt,
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

func (c *UsageClient) Active(ctx context.Context) ([]usage.ActiveSession, error) {
	lines, err := c.psLines()
	if err != nil {
		return nil, err
	}
	out := []usage.ActiveSession{}
	seen := map[string]bool{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "codex") {
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
		cmdline := strings.Join(fields[1:], " ")
		for _, id := range sessionUUIDPattern.FindAllString(cmdline, -1) {
			key := fmt.Sprintf("%d:%s", pid, id)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, usage.ActiveSession{
				Provider:  "codex",
				SessionID: id,
				PID:       pid,
				Command:   cmdline,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SessionID == out[j].SessionID {
			return out[i].PID < out[j].PID
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out, nil
}

func (c *UsageClient) date(value time.Time) time.Time {
	if !value.IsZero() {
		return value
	}
	return c.now().In(configLocation(c.cfg.Usage.Timezone))
}

func (c *UsageClient) provider() config.ProviderConfig {
	return c.cfg.Usage.Providers["codex"]
}

func (c *UsageClient) runCCUsageDaily(ctx context.Context, provider config.ProviderConfig, date time.Time) (ccusageDailyResponse, error) {
	var out ccusageDailyResponse
	if err := c.runCCUsage(ctx, provider, "daily", date, &out); err != nil {
		return ccusageDailyResponse{}, err
	}
	return out, nil
}

func (c *UsageClient) runCCUsageSessions(ctx context.Context, provider config.ProviderConfig, date time.Time) (ccusageSessionResponse, error) {
	var out ccusageSessionResponse
	if err := c.runCCUsage(ctx, provider, "session", date, &out); err != nil {
		return ccusageSessionResponse{}, err
	}
	return out, nil
}

func (c *UsageClient) runCCUsage(ctx context.Context, provider config.ProviderConfig, subcommand string, date time.Time, out any) error {
	if !provider.CCUsageEnabled {
		return errors.New("codex ccusage integration is disabled; enable usage.providers.codex.ccusage_enabled")
	}
	parts, err := splitCommand(firstNonEmpty(provider.CCUsageCommand, "ccusage"))
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return errors.New("codex ccusage command is empty")
	}
	day := date.Format("2006-01-02")
	args := append([]string{}, parts[1:]...)
	args = append(args, "codex", subcommand, "--json", "--since", day, "--until", day)
	if tz := strings.TrimSpace(c.cfg.Usage.Timezone); tz != "" && !strings.EqualFold(tz, "local") {
		args = append(args, "--timezone", tz)
	}
	cmd := exec.CommandContext(ctx, parts[0], args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ccusage command %q failed: %w; install ccusage or set usage.providers.codex.ccusage_command = \"npx --yes ccusage\"; stderr: %s", provider.CCUsageCommand, err, strings.TrimSpace(stderr.String()))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("ccusage %s JSON decode failed: %w", subcommand, err)
	}
	return nil
}

func (c *UsageClient) readSessionMetadata(item ccusageSession) sessionMetadata {
	path := c.sessionPath(item)
	if path == "" {
		return sessionMetadata{}
	}
	meta, err := readSessionMetadata(path)
	if err != nil {
		return sessionMetadata{}
	}
	return meta
}

func (c *UsageClient) sessionPath(item ccusageSession) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := filepath.Join(home, ".codex", "sessions")
	if strings.TrimSpace(item.SessionID) != "" {
		path := filepath.Join(root, filepath.FromSlash(item.SessionID)+".jsonl")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if strings.TrimSpace(item.Directory) != "" && strings.TrimSpace(item.SessionFile) != "" {
		path := filepath.Join(root, filepath.FromSlash(item.Directory), item.SessionFile+".jsonl")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
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
	CostUSD     float64 `json:"costUSD"`
}

type ccusageTotals struct {
	InputTokens           int64   `json:"inputTokens"`
	OutputTokens          int64   `json:"outputTokens"`
	CacheCreationTokens   int64   `json:"cacheCreationTokens"`
	CacheReadTokens       int64   `json:"cacheReadTokens"`
	ReasoningOutputTokens int64   `json:"reasoningOutputTokens"`
	TotalTokens           int64   `json:"totalTokens"`
	CostUSD               float64 `json:"costUSD"`
}

type ccusageSession struct {
	SessionID    string  `json:"sessionId"`
	SessionFile  string  `json:"sessionFile"`
	Directory    string  `json:"directory"`
	LastActivity string  `json:"lastActivity"`
	TotalTokens  int64   `json:"totalTokens"`
	CostUSD      float64 `json:"costUSD"`
}

type sessionMetadata struct {
	ID         string
	CWD        string
	Originator string
	Source     string
	LastPrompt string
	UserText   string
}

func readSessionMetadata(path string) (sessionMetadata, error) {
	var meta sessionMetadata
	if err := readSessionHeader(path, &meta); err != nil {
		return meta, err
	}
	if err := readSessionTail(path, &meta); err != nil {
		return meta, err
	}
	meta.LastPrompt = cleanPrompt(meta.LastPrompt, 240)
	return meta, nil
}

func readSessionHeader(path string, meta *sessionMetadata) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var entry sessionEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type == "session_meta" {
			readSessionMeta(entry.Payload, meta)
			break
		}
	}
	return scanner.Err()
}

func readSessionTail(path string, meta *sessionMetadata) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	offset := info.Size() - sessionTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, 0); err != nil {
		return err
	}
	reader := bufio.NewReader(file)
	if offset > 0 {
		if _, err := reader.ReadString('\n'); err != nil {
			return nil
		}
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var entry sessionEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		switch entry.Type {
		case "session_meta":
			if meta.ID == "" || meta.CWD == "" {
				readSessionMeta(entry.Payload, meta)
			}
		case "event_msg":
			readEventMessage(entry.Payload, meta)
		case "response_item":
			readResponseItem(entry.Payload, meta)
		}
	}
	return scanner.Err()
}

type sessionEntry struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func readSessionMeta(raw json.RawMessage, meta *sessionMetadata) {
	var payload struct {
		ID         string `json:"id"`
		CWD        string `json:"cwd"`
		Originator string `json:"originator"`
		Source     any    `json:"source"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	meta.ID = firstNonEmpty(meta.ID, payload.ID)
	meta.CWD = firstNonEmpty(meta.CWD, payload.CWD)
	meta.Originator = firstNonEmpty(meta.Originator, payload.Originator)
	meta.Source = firstNonEmpty(meta.Source, sourceText(payload.Source))
}

func readEventMessage(raw json.RawMessage, meta *sessionMetadata) {
	var payload struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	if payload.Type != "user_message" || strings.TrimSpace(payload.Message) == "" {
		return
	}
	appendUserText(meta, payload.Message)
}

func readResponseItem(raw json.RawMessage, meta *sessionMetadata) {
	var payload struct {
		Type    string        `json:"type"`
		Role    string        `json:"role"`
		Content []contentPart `json:"content"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	if payload.Type != "message" || payload.Role != "user" {
		return
	}
	for _, part := range payload.Content {
		text := firstNonEmpty(part.Text, part.InputText)
		if strings.TrimSpace(text) != "" {
			appendUserText(meta, text)
		}
	}
}

type contentPart struct {
	Text      string `json:"text"`
	InputText string `json:"input_text"`
}

func appendUserText(meta *sessionMetadata, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if meta.UserText != "" {
		meta.UserText += "\n"
	}
	meta.UserText += text
	meta.LastPrompt = text
}

func classifySession(meta sessionMetadata) string {
	text := strings.ToLower(meta.UserText + "\n" + meta.Originator + "\n" + meta.Source)
	switch {
	case strings.EqualFold(meta.Source, "review"),
		strings.EqualFold(meta.Originator, "review"),
		strings.Contains(strings.ToLower(meta.Source), "review"):
		return "review"
	case strings.Contains(text, "review --uncommitted"),
		strings.Contains(text, "review the current code changes"),
		strings.Contains(text, "prioritized findings"):
		return "review"
	case strings.Contains(text, "/goal"),
		strings.Contains(text, "active thread goal"),
		strings.Contains(text, "goal file"),
		strings.Contains(text, "resume "),
		strings.Contains(text, "process only"):
		return "goal/resume"
	default:
		return defaultSessionType
	}
}

func sourceText(source any) string {
	switch value := source.(type) {
	case nil:
		return ""
	case string:
		return value
	case map[string]any:
		if subagent, ok := value["subagent"].(string); ok {
			return subagent
		}
		if name, ok := value["name"].(string); ok {
			return name
		}
	}
	return ""
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

func splitCommand(command string) ([]string, error) {
	var out []string
	var current strings.Builder
	var quote rune
	escaped := false
	for _, r := range command {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			if current.Len() > 0 {
				out = append(out, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in ccusage command %q", command)
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out, nil
}

func sessionUUID(value string) string {
	matches := sessionUUIDPattern.FindAllString(value, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func idOrFallback(id, fallback string) string {
	if id != "" {
		return id
	}
	return fallback
}

func cleanPrompt(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	if limit <= 1 {
		return value[:limit]
	}
	return value[:limit-1] + "..."
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
