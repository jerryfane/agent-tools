# agent-tools

Open-source terminal tools for supervising coding agents.

`agent-tools` is planned as a composable Go CLI for agent operations. The first
focus area is usage visibility:

- subscription limits, starting with Codex profiles;
- current-day token usage, initially via `ccusage`;
- session grouping by repository and task type;
- a terminal TUI with an internal sidebar for limits, usage, sessions,
  providers, and alerts.

The project is MIT licensed and intentionally starts with Codex as the first
provider while keeping the provider interface open for Claude, Gemini, and
other agent CLIs later.

## Planned CLI

```bash
agent-tools usage tui
agent-tools usage limits
agent-tools usage today
agent-tools usage sessions
agent-tools usage providers
agent-tools usage doctor
agent-tools usage herdr-publisher
```

## Current CLI

```bash
agent-tools usage providers
agent-tools usage limits --provider codex
agent-tools usage limits --provider codex --json
```

`usage limits` is Codex-only right now. It reads local Codex profile
`auth.json` files and calls the ChatGPT/Codex quota endpoint to report
remaining 5-hour and weekly subscription quota. It does not start a Codex
session or send prompts to a model, but the endpoint is internal and may
change.

Profiles can be configured explicitly in `~/.config/agent-tools/config.toml`.
If no Codex profiles are configured, the tool discovers `~/.codex-*`
directories containing `auth.json`, then falls back to `~/.codex`.

Limit snapshots and refreshed access tokens are cached under the OS user cache
directory with private permissions. Do not commit local config, auth files, or
cache files.

## Status

This repository is newly created. See the initial planning issue for the first
implementation milestone.
