# Configuration Guide

`agent-tools` loads TOML config from:

```text
~/.config/agent-tools/config.toml
```

Use `--config PATH` to point a command at another config file:

```bash
agent-tools --config ./docs/usage-config.example.toml usage providers
```

Environment overrides use the `AGENT_TOOLS_` prefix. Replace dots with double
underscores:

```bash
AGENT_TOOLS_USAGE__TIMEZONE=Europe/Berlin agent-tools usage doctor
```

## Basic Codex Setup

```toml
[usage]
timezone = "Europe/Berlin"
refresh_limits_seconds = 300
refresh_usage_seconds = 60
refresh_process_seconds = 15

[usage.providers.codex]
enabled = true
default_profile = "default"
ccusage_enabled = true
ccusage_command = "ccusage"

[usage.providers.codex.profiles.default]
home = "~/.codex"
label = "codex"
```

If profiles are not configured explicitly, Codex profile discovery uses:

1. `~/.codex-*` directories that contain `auth.json`.
2. `~/.codex` as the fallback `default` profile.

## Multiple Codex Profiles

```toml
[usage.providers.codex]
enabled = true
default_profile = "jerryf"
ccusage_enabled = true
ccusage_command = "npx --yes ccusage"

[usage.providers.codex.profiles.jerryf]
home = "~/.codex-jerryf"
label = "jf"

[usage.providers.codex.profiles.spark]
home = "~/.codex-spark"
label = "spark"
```

Limits are fetched per configured profile. Usage commands still read the local
session corpus used by `ccusage`; they do not guarantee per-subscription token
splits when profiles share sessions.

## ccusage

Use the global command when installed:

```toml
ccusage_command = "ccusage"
```

Use `npx` when you do not want a global install:

```toml
ccusage_command = "npx --yes ccusage"
```

Disable usage commands while keeping limits:

```toml
ccusage_enabled = false
```

## Cache Directory

By default, Codex limit snapshots and refreshed access tokens are stored under
the OS user cache directory:

```text
<user-cache>/agent-tools/codex
```

Override only when needed:

```toml
[usage.providers.codex]
cache_dir = "~/.cache/agent-tools/codex"
```

Cache files are written with private permissions. Do not commit them.

## Herdr Publisher

The Herdr publisher reads normal config and publishes compact labels:

```bash
agent-tools usage herdr-publisher --provider codex --mode limit
```

Modes:

- `limit`: show compact remaining quota, for example `57%5h 69%wk`.
- `usage`: show current-day token/cost labels for the pane cwd.
- `auto`: prefer usage labels unless quota is low or errored.

Run once for debugging:

```bash
agent-tools usage herdr-publisher --once --dry-run
```

