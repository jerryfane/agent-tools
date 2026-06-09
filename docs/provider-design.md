# Provider Design

`agent-tools usage` is provider-shaped. Codex and Claude Code are implemented
today; new agent CLIs slot in behind the same contract.

The CLI, TUI, and Herdr publisher select a provider through
`internal/providers`, which dispatches `Limits`/`Usage`/`Sessions` to the
matching per-agent package (`internal/codex`, `internal/claude`) rather than
hardcoding one provider.

The shared provider contract is:

```go
type Provider interface {
    Name() string
    Enabled() bool
    Limits(context.Context) ([]LimitSnapshot, error)
    Usage(context.Context, time.Time) (UsageSummary, error)
    Sessions(context.Context, time.Time) ([]SessionUsage, error)
    Active(context.Context) ([]ActiveSession, error)
    Doctor(context.Context) []Check
}
```

Shared output types normalize provider-specific data into:

- `LimitSnapshot`: quota and reset windows.
- `UsageSummary`: current-day total usage.
- `UsageGroup`: usage grouped by provider, repo/cwd, task type, and active
  state.
- `SessionUsage`: per-session token/cost rows.
- `ActiveSession`: currently running local process mappings.
- `Check`: local doctor checks.

## Codex V1

Codex implements:

- profile discovery from explicit config, `~/.codex-*`, and `~/.codex`;
- per-profile limit fetching from the ChatGPT/Codex quota endpoint;
- private local limit and token cache files;
- current-day usage and sessions through `ccusage`;
- session metadata enrichment from Codex JSONL logs;
- active process detection from local process state;
- Herdr pane metadata publishing through a separate command.

Important limitation: Codex limits are per profile, while token usage is based
on the local session corpus consumed by `ccusage`.

## Claude V1

Claude (`internal/claude`) mirrors the Codex shape:

- profile discovery from explicit config, `~/.claude-*`, and `~/.claude`;
- per-profile limit fetching from Claude Code's OAuth usage endpoint, with a
  sticky cooldown on `429` (the endpoint rate-limits aggressive polling);
- private local limit and token cache files (the credentials file is read, never
  written);
- current-day usage and sessions through `ccusage claude ...`, with sessions
  filtered client-side by last-activity date;
- active Claude Code process detection (sessions are not correlated to processes
  in v1).

Important limitation: macOS Keychain credentials are not yet read; see
[claude.md](claude.md).

## Adding Another Provider

For Gemini or another agent CLI:

1. Add provider config under `usage.providers.<name>`.
2. Implement provider-specific limit, usage, session, active, and doctor logic
   behind an internal package.
3. Normalize output into the shared `internal/usage` structs.
4. Keep provider-specific auth, cache, and log parsing out of core CLI/TUI
   rendering code.
5. Add tests using structured fixtures instead of private local state.
6. Document provider limitations in README and this file.

Provider-specific commands can exist, but normal status surfaces should prefer
the shared `agent-tools usage` commands so the TUI and JSON outputs remain
composable.

