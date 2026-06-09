# Goal: Build The Composable Agent Usage CLI And TUI

Implement the `agent-tools usage` plan task by task. Each task must be
developed, reviewed, opened as its own pull request, merged, and verified
before moving on, unless tasks are explicitly safe to run in parallel.

The user-facing outcome is a public MIT-licensed Go CLI named `agent-tools`
with a composable usage subsystem. Codex is provider v1, with a provider
interface ready for Claude, Gemini, and other agent CLIs later. The tool must
offer normal CLI output, JSON output, and a terminal TUI with an internal
sidebar for `Limit`, `Usage`, `Sessions`, `Providers`, and `Alerts`.

Primary tracker:

- GitHub issue: https://github.com/jerryfane/agent-tools/issues/1
- Target repo: `jerryfane/agent-tools`
- Target base branch: `main`

## Core Rules

- Work one task at a time in the listed order by default.
- Do not start dependent work until the previous task has passed checks, passed
  `codex exec review --uncommitted`, been pushed, opened as a PR, merged, and
  verified on `main`.
- Keep the repo public-safe. Do not commit auth files, token caches, local
  profile config, private session archives, local generated reports, or host
  specific secrets.
- Preserve the existing scaffold unless the current task explicitly changes it.
- Keep provider-specific logic behind provider boundaries. Do not leak Codex
  assumptions into the core CLI or TUI state model.
- Use `ccusage` as an external structured JSON backend in v1. Do not reimplement
  native local token parsing unless a later task explicitly asks for it.
- Document that Codex limits are per profile/subscription, while `ccusage`
  usage is local-session based and may not split cleanly across shared Codex
  profiles.
- Verify third-party CLI/library behavior with local commands and/or official
  sources before relying on it in code.

## Before Starting

1. Inspect repo state:
   - `git status --short --branch`
   - `git remote -v`
   - `git log --oneline -8`
2. Confirm the target branch is `main`.
3. Confirm GitHub tooling:
   - `gh auth status`
   - `gh repo view jerryfane/agent-tools`
4. Confirm Go tooling:
   - `go version`
5. Inspect the current scaffold and issue #1 before editing.

## Per-Task Workflow

1. Create a task branch from the latest `main`.
2. Implement only the current task.
3. Add focused tests/checks for the changed behavior.
4. Run:
   - `gofmt -w` on touched Go files
   - `go test ./...`
   - `go vet ./...` once the module has Go packages
   - any task-specific smoke commands listed below
5. Run `codex exec review --uncommitted` and preserve the exact raw output.
6. Fix review findings and rerun checks/review until clean or explicitly
   blocked.
7. Run `git diff --check`, inspect the final diff, commit, push, and open one
   PR for the task.
8. PR body must include:
   - WHAT
   - WHY
   - CHANGES
   - RESULTS
   - RISK
   - exact final raw `codex exec review --uncommitted` output
9. Merge the PR after checks/review pass, update local `main`, and verify the
   worktree is clean.

## Implementation Tasks

### Task 2001: Scaffold The Go CLI, Config, And Provider Registry

Scope:

- Add the Cobra CLI entrypoint for `agent-tools`.
- Add `agent-tools usage` command group.
- Add Koanf-backed config loading from `~/.config/agent-tools/config.toml`,
  optional `--config`, and environment overrides.
- Add core usage domain structs and a provider registry with an initial
  disabled placeholder for future providers.
- Add JSON and table output helpers used by subcommands.

Acceptance criteria:

- `agent-tools --help` works.
- `agent-tools usage --help` works.
- `agent-tools usage providers --json` shows configured/enabled provider state.
- Missing config is handled with public-safe defaults.
- Config example remains non-private.

Suggested branch:

- `task/usage-cli-config-registry`

Suggested commit:

- `feat: scaffold usage cli and provider registry`

### Task 2002: Implement Codex Limits Provider

Scope:

- Port the current Codex quota-fetching behavior from `agent-cli-tools`
  conceptually, not by copying private cache data.
- Discover Codex profiles from explicit config first, then from `~/.codex-*`
  homes containing `auth.json`, then fallback to `~/.codex`.
- Fetch and normalize Codex profile limits:
  - 5-hour remaining percentage
  - weekly remaining percentage
  - reset timestamps
  - source and cache age
- Add private local cache files under the user cache dir with restrictive
  permissions.
- Add `agent-tools usage limits` with table and JSON output.

Acceptance criteria:

- `agent-tools usage limits --provider codex --json` works with configured
  profiles.
- Auth/config errors are clear and do not print tokens.
- The README documents that this endpoint is Codex-only and may change.

Suggested branch:

- `task/codex-limits-provider`

Suggested commit:

- `feat: add codex limits provider`

### Task 2003: Implement Daily Usage And Session Grouping

Scope:

- Add a ccusage adapter that shells out to the configured `ccusage` command and
  consumes JSON output.
- Implement `agent-tools usage today`.
- Implement `agent-tools usage sessions`.
- Enrich Codex sessions by reading local `~/.codex/sessions/**/*.jsonl`.
- Classify sessions as:
  - `review`
  - `goal/resume`
  - `other`
- Group usage by provider, repo/cwd, type, active state, tokens, and estimated
  cost.
- Detect active Codex sessions from local process state where available.

Acceptance criteria:

- `agent-tools usage today --provider codex --json` returns totals and grouped
  repo/type usage.
- `agent-tools usage sessions --provider codex --json` lists biggest offender
  sessions with session id, repo/cwd, type, tokens, cost, active flag, and last
  prompt preview.
- Missing `ccusage` produces a clear doctor/action message.
- Tests cover ccusage JSON normalization and session classification.

Suggested branch:

- `task/codex-daily-usage-sessions`

Suggested commit:

- `feat: add codex daily usage and sessions`

### Task 2004: Build The Terminal Usage TUI

Scope:

- Add Bubble Tea TUI at `agent-tools usage tui`.
- Use an internal sidebar with pages:
  - `Limit`
  - `Usage`
  - `Sessions`
  - `Providers`
  - `Alerts`
- Use Bubbles/Lip Gloss for tables, viewports, and layout.
- Implement independent refresh intervals:
  - limits: default 300s
  - usage: default 60s
  - process status: default 15s
- Keep UI usable in narrow panes and mobile SSH terminals.

Acceptance criteria:

- TUI launches in a Herdr pane.
- Sidebar navigation works by keyboard.
- Long tables scroll.
- Refresh errors are displayed without crashing the app.
- `Limit`, `Usage`, `Sessions`, `Providers`, and `Alerts` pages all render
  useful initial content.

Suggested branch:

- `task/usage-tui`

Suggested commit:

- `feat: add usage tui`

### Task 2005: Add Herdr Publisher Compatibility

Scope:

- Add `agent-tools usage herdr-publisher`.
- Publish compact Codex labels into Herdr pane metadata equivalent to the
  existing `herdr-codex-usage-sidebar` script.
- Support view modes:
  - `limit`
  - `usage`
  - `auto`
- Keep this separate from the internal TUI sidebar.
- Add compatibility wrapper documentation for:
  - `codex-usage-all`
  - `codex-usage-api`
  - `herdr-codex-usage-sidebar`

Acceptance criteria:

- Existing Herdr compact limit behavior can be reproduced.
- Publisher errors do not kill the loop unless config is invalid.
- README clearly distinguishes Herdr sidebar metadata from the internal TUI
  sidebar.

Suggested branch:

- `task/herdr-publisher-compat`

Suggested commit:

- `feat: add herdr usage publisher`

### Task 2006: Documentation, Install, And Release Prep

Scope:

- Update README with:
  - install instructions
  - config guide
  - privacy/secrets boundaries
  - Codex limitations
  - ccusage dependency
  - examples for each command
- Add `docs/` pages as needed for config and provider design.
- Add release/build workflow scaffolding.
- Add GoReleaser config if practical, or document it as the next release step.

Acceptance criteria:

- A new user can clone, build, run doctor, and understand how to configure
  Codex profiles without seeing private local paths.
- Release prep does not require secrets committed to the repo.
- `go test ./...`, `go vet ./...`, and README command examples are consistent.

Suggested branch:

- `task/docs-install-release`

Suggested commit:

- `docs: document agent-tools usage workflow`

## Final Completion Criteria

The goal is complete only when all tasks above are merged to `main`, local
`main` is clean and up to date, the CLI builds, the documented smoke commands
work, and issue #1 has a closing summary with PR links and remaining follow-up
work, if any.

Do not mark the goal complete merely because the scaffold exists or the first
task is merged.
