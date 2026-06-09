# Goal: Add Claude (Pro/Max) Usage Provider

Add a `claude` usage provider so `agent-tools` reports Claude Code subscription
quota and token usage alongside Codex, across the CLI, TUI, and Herdr publisher.
Each task is reviewed, opened as its own pull request, merged, and verified
before moving on.

Primary tracker:

- GitHub issue: https://github.com/jerryfane/agent-tools/issues/8
- Target repo: `jerryfane/agent-tools`
- Target base branch: `main`

## Verified Data Sources (discovery spike)

- **Quota**: `GET https://api.anthropic.com/api/oauth/usage` with
  `Authorization: Bearer <accessToken>`, `anthropic-beta: oauth-2025-04-20`,
  `anthropic-version: 2023-06-01`. Response: `five_hour` / `seven_day` (plus
  nullable `seven_day_opus` / `seven_day_sonnet` / `extra_usage`) each shaped
  `{"utilization": <0-100 percent used>, "resets_at": <RFC3339|null>}`.
  Remaining = `100 - utilization` (same polarity handling as codex
  `normalizeWindow`).
- **Auth**: `~/.claude/.credentials.json` →
  `claudeAiOauth.{accessToken, refreshToken, expiresAt(ms), subscriptionType, rateLimitTier}`.
  macOS stores the same blob in the Keychain (deferred; Linux is the use case).
  Refresh: `POST https://platform.claude.com/v1/oauth/token` with a JSON body
  `{"grant_type":"refresh_token","refresh_token":...,"client_id":"9d1c250a-e61b-44d9-88ed-5944d1962f5e"}`.
- **Token usage**: `ccusage claude daily|session --json --since D --until D`.
  Claude daily totals use `totalCost`/`totalTokens`; sessions use
  `sessionId`/`projectPath`/`lastActivity`/`totalCost`/`totalTokens`.

## Critical Constraint

The `/api/oauth/usage` endpoint aggressively rate-limits: polling at 30-60s
triggers HTTP 429 with no `Retry-After`, and the 429 state persists 30+ minutes
(anthropics/claude-code#31637, #31021). The provider must be cache-first with a
persisted 429 cooldown:

- Refresh TTL clamped to `max(cfg.Usage.RefreshLimitsSeconds, 300s)`.
- On 429: serve stale cache, record a sticky cooldown (30 min) in the cache
  file, do not retry until it expires, and surface
  `Source: "cache (rate-limited)"` plus an `Error` message.
- The cooldown must not be bypassed by `--force-refresh`.
- Display layers (TUI, Herdr publisher) read cache and never force-fetch.

## Core Rules

- Work one task at a time in order.
- Keep `internal/codex` byte-identical; build `internal/claude` as a sibling.
  Copy the small cache/lock/JSON helpers into the claude package rather than
  refactoring codex. Reuse the exported `codex.SplitCommand`.
- Keep the repo public-safe: never commit auth files, token caches, or print
  token values in logs, errors, or doctor output.
- Verify external contracts (ccusage JSON, the OAuth endpoints) against the
  local tools before relying on field names.

## Implementation Tasks

### Task 4001: `internal/claude` limits client
Branch `task/claude-limits`. OAuth usage endpoint, credentials parsing, token
refresh (JSON), cache + flock, 429 cooldown, TTL clamp. Tests via
`httptest.Server` and `t.TempDir` credential fixtures.

### Task 4002: `internal/claude` usage client
Branch `task/claude-usage`. `ccusage claude daily|session --json`. Minimal v1
sessions (repo from `projectPath`, `Type: "other"`). `Active()` word-boundary
`claude` ps match.

### Task 4003: provider dispatch + CLI wiring
Branch `task/claude-cli`. New `internal/providers` dispatch package; wire
`limits`/`today`/`sessions` in `root.go`; enable claude by default in
`config.Defaults()`.

### Task 4004: doctor + docs
Branch `task/claude-doctor-docs`. Claude doctor checks (credentials validation,
never print tokens, darwin Keychain note); README + provider docs.

### Task 4005: multi-provider TUI limits
Branch `task/multi-provider-tui`. Aggregate limits across enabled providers;
per-provider errors as rows; provider column; de-hardcode "Codex" strings.

### Task 4006: Herdr publisher claude support
Branch `task/claude-herdr`. Per-provider collection; `pane.Agent == "claude"`,
`--agent claude`, `HERDR_CLAUDE_PROFILE`/`CLAUDE_CONFIG_DIR`; codex behavior
byte-identical.

## Per-Task Workflow

1. Branch, implement only that task, add focused tests.
2. `gofmt -w` touched files, `go test ./...`, `go vet ./...`, `git diff --check`.
3. Commit, push, open one PR with WHAT/WHY/CHANGES/RESULTS/RISK.
4. Merge after checks pass; update local `main`; verify clean worktree.

## Completion Criteria

All six tasks merged, issue #8 closed with a summary, local `main` clean and
verified, and a Telegram handoff sent via Agentgram.
