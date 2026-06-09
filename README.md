# agent-tools

Open-source terminal tools for supervising coding agents.

`agent-tools` is a Go CLI for local usage visibility across coding-agent
workflows. Codex is the first implemented provider. The core is shaped so
Claude, Gemini, and other agent CLIs can be added later behind the same usage
interfaces.

Current capabilities:

- Codex subscription limits by local Codex profile.
- Current-day token usage via `ccusage`.
- Session grouping by repository/cwd, task type, and active state.
- A terminal TUI with internal sidebar pages for limits, usage, sessions,
  providers, and alerts.
- A Herdr metadata publisher for compact Codex labels in Herdr's agent sidebar.
- A local `doctor` command for install/config checks.

This project is MIT licensed.

## Install

Prerequisites:

- Go 1.24.2 or newer.
- Codex installed and logged in if you want Codex quota checks.
- `ccusage` on `PATH` for token usage commands, or `npx` if you configure
  `ccusage_command = "npx --yes ccusage"`.
- Herdr only if you use `usage herdr-publisher`.

Build from source:

```bash
git clone https://github.com/jerryfane/agent-tools.git
cd agent-tools
go build -o ./bin/agent-tools ./cmd/agent-tools
./bin/agent-tools usage doctor
```

Optional install into your user bin:

```bash
mkdir -p ~/.local/bin
go build -o ~/.local/bin/agent-tools ./cmd/agent-tools
agent-tools usage doctor
```

## Commands

```bash
agent-tools usage providers
agent-tools usage doctor
agent-tools usage limits --provider codex
agent-tools usage limits --provider codex --json
agent-tools usage today --provider codex
agent-tools usage today --provider codex --json
agent-tools usage sessions --provider codex
agent-tools usage sessions --provider codex --json
agent-tools usage tui --provider codex
agent-tools usage herdr-publisher --provider codex --mode limit
```

`usage doctor` reports local checks. It may print local profile paths; review
the output before sharing it publicly.

`usage limits` is Codex-only right now. It reads local Codex profile
`auth.json` files and calls the ChatGPT/Codex quota endpoint to report
remaining 5-hour and weekly subscription quota. It does not start a Codex
session or send prompts to a model, but the endpoint is internal and may
change.

`usage today` and `usage sessions` are Codex-only right now and use `ccusage`
as an external structured JSON backend. These commands read local
`~/.codex/sessions` JSONL files to enrich sessions with cwd/repo, task type,
active state, and last prompt preview.

`usage tui` opens an interactive terminal dashboard with internal sidebar pages
for limits, usage, sessions, providers, and alerts.

`usage herdr-publisher` is separate from the internal TUI. It publishes compact
Codex labels to Herdr pane metadata with `herdr pane report-metadata`, so
Herdr's own agent sidebar can show labels such as `57%5h 69%wk`. Use
`--mode limit` for compact quota labels, `--mode usage` for current-day token
labels, and `--mode auto` to prefer usage labels unless a profile is low or
errored.

## Configure

The default config path is:

```text
~/.config/agent-tools/config.toml
```

Start from the public example:

```bash
mkdir -p ~/.config/agent-tools
cp docs/usage-config.example.toml ~/.config/agent-tools/config.toml
```

Minimal Codex config:

```toml
[usage.providers.codex]
enabled = true
default_profile = "default"
ccusage_enabled = true
ccusage_command = "ccusage"

[usage.providers.codex.profiles.default]
home = "~/.codex"
label = "codex"
```

For machines without a global `ccusage` binary:

```toml
[usage.providers.codex]
ccusage_command = "npx --yes ccusage"
```

Claude Code (Pro/Max) is enabled by default and discovered from `~/.claude`
(and `~/.claude-<name>` profiles). Override the discovery explicitly with:

```toml
[usage.providers.claude]
enabled = true
ccusage_enabled = true
ccusage_command = "ccusage"

[usage.providers.claude.profiles.default]
home = "~/.claude"
label = "claude"
```

More details:

- [Configuration Guide](docs/config.md)
- [Privacy And Local Data](docs/privacy.md)
- [Provider Design](docs/provider-design.md)
- [Claude Provider](docs/claude.md)

## Codex Limits Versus Usage

Codex limits are per profile/subscription because they come from each profile's
authenticated quota endpoint.

Token usage is local-session based because `ccusage` reads local Codex session
logs. If multiple Codex subscriptions share the same `~/.codex/sessions`
corpus, usage totals may not split cleanly by subscription.

The Claude provider works the same way, with one important difference: its
quota endpoint rate-limits aggressively, so agent-tools refreshes it at most
every five minutes and, on a `429`, stops calling it for 30 minutes while
serving cached numbers (even under `--force-refresh`). See
[docs/claude.md](docs/claude.md).

## Compatibility Wrappers

Old one-off commands should translate to `agent-tools`; they are migration
targets, not drop-in symlinks:

```bash
codex-usage-all -> agent-tools usage limits --provider codex
codex-usage-api --json -> agent-tools usage limits --provider codex --json
herdr-codex-usage-sidebar -> agent-tools usage herdr-publisher --provider codex --mode limit
```

Legacy `codex-usage-api` flags such as `--list-profiles`, `--profiles-json`,
and `--compact-profile` need a translating wrapper or a future native command.

## Development

```bash
gofmt -w $(find . -name '*.go' -not -path './vendor/*')
go test ./...
go vet ./...
go build -o /tmp/agent-tools ./cmd/agent-tools
```

CI runs formatting, tests, vet, and a build on pull requests and pushes to
`main`.

Release scaffolding is included in `.goreleaser.yml`. A tagged release can be
built with GoReleaser once the GitHub release workflow is enabled with a token
that can publish releases.
