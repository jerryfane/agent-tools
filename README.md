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

## Status

This repository is newly created. See the initial planning issue for the first
implementation milestone.
