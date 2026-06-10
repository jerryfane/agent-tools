# Goal: Usage Guard + Multi-Provider Usage/Sessions

Implement the plan task by task. Each task must be developed, reviewed, opened
as its own pull request, merged, and verified before moving on, unless a task is
explicitly marked parallel-safe.

Add a **usage guard** to `agent-tools` that warns, denies, or stops AI-agent
token burn when a configurable budget or quota floor is crossed — per
project/session and globally, across Codex and Claude — via the agents' native
**PreToolUse hooks**. Plus an independent first task: a **provider column on the
Usage and Sessions surfaces** (finishing what PR #13 did for Limits).

Tracking issue: https://github.com/jerryfane/agent-tools/issues/15
Target repo: `jerryfane/agent-tools`. Target base branch: `main`.

User-facing outcome: `agent-tools guard install-hooks` arms a cache-first,
observe-then-enforce guard that the running Codex/Claude sessions consult before
every tool call; budgets and quota floors are configured in TOML; over-budget
sessions are warned, then denied, then stopped, with a manual `guard release`
escape hatch and Telegram alerts. Explicitly excluded from this goal (deferred
follow-up): the herdr watchdog for non-hooked panes, herdr guard state-labels,
per-profile budgets, weekly/rolling windows, and macOS.

## Core Rules

- Work one task at a time in the listed order by default. Task 1 is
  parallel-safe (independent of the guard); Tasks 2-6 are sequential.
- Do not start dependent work until the prerequisite task has passed checks,
  passed `/code-review`, been pushed, opened as a PR, merged, and verified on
  `main`.
- Do not commit generated data, caches, logs, secrets, credentials, the guard
  state file, session archives, cloned helper repos, or large outputs. The only
  intended tracked artifacts are source, tests, docs, and this goal file.
- The guard must **never** print token values or credentials in logs, state,
  errors, doctor output, or Telegram messages — only paths, counts, and dollars.
- The guard must access quota/usage **only** through `internal/providers`
  (`LimitsFor` with `force=false`, `UsageFor`, `SessionsFor`); it must never
  call the quota endpoint or ccusage directly, and must never bypass the
  existing 5-min TTL / sticky 429 cooldown in `internal/claude/limits.go`.
- Preserve existing behavior unless the current task explicitly changes it.
  Keep `internal/codex` and `internal/claude` untouched except where a task
  names them; build the guard as a new sibling package `internal/guard`,
  copying the small cache/flock/atomic-0600 helpers from
  `internal/claude/limits.go` per repo convention.
- Avoid code duplication; reuse `internal/providers` dispatch and the existing
  `loadLimits` aggregation pattern in `internal/tui/model.go`.
- Verify external contracts (Claude Code PreToolUse + UserPromptSubmit hook JSON,
  Codex PreToolUse hook JSON, `settings.json`/`hooks.json` shapes, `agentgram`
  CLI) against the official docs and/or local commands before editing.
- Dry-run-first: the guard ships with `enabled = false` and `mode = "observe"`;
  it must be fully runnable in observe-only mode before any enforcement lands.

## Before Starting

1. Inspect repo state: `git status --short`, current branch, `git remote -v`.
2. If the base branch is unclear, the remote looks wrong, or the worktree has
   unrelated changes, stop and ask.
3. Confirm the base branch is `main`.
4. Verify tooling: `gh auth status`, `go version`, and that the `/code-review`
   skill, `ccusage`, and `agentgram` are available.
5. Read the tracking issue (#15) and `docs/goals/GOAL-agent-tools-claude-provider.md`
   for the established patterns this goal builds on.

## Per-Task Branch Workflow

1. Confirm the current task's scope.
2. Create the task branch (named in the task) from the latest `main`.
3. Implement only that task.
4. Add focused tests/checks for the changed behavior.
5. Run, in the repo:
   - `gofmt -w` on touched Go files
   - `go test ./...`
   - `go vet ./...`
   - `git diff --check`
   - the task-specific smoke commands listed in the task.
6. Run `/code-review` on the uncommitted changes and preserve the exact raw
   output.
7. Apply the Review-Fix Loop until `/code-review` is clean or a finding is
   verified incorrect.
8. Inspect the final diff, commit with the task's suggested message, and push.

## Review-Fix Loop

1. If `/code-review` finds issues, do not only patch the literal line.
2. Identify the underlying invariant/class of bug.
3. Audit nearby and sibling paths for the same issue.
4. Plan the smallest safe fix, verifying external assumptions with local commands
   and/or official sources; preserve repo patterns and avoid unnecessary
   refactors.
5. Execute the fix, then re-run focused tests/checks and `/code-review`.
6. Repeat until the final `/code-review` output contains no findings, or stop if
   blocked or if a finding is incorrect after verification.

## Commit Gate

1. Run `git diff --check` and inspect the final diff.
2. Commit only the current task's intended tracked changes (never the guard
   state file or other generated output).
3. Use the task's suggested commit message.
4. Push the task branch; verify the worktree is clean after push.

## Pull Request Gate

1. Open one PR for the current task against `main`.
2. The PR body must include: WHAT, WHY, CHANGES, RESULTS, RISK, and the exact
   raw final `/code-review` output.
3. Wait for CI (the `test` workflow) and fix failures before merge.
4. Merge with squash; update local `main`; verify the worktree is clean.
5. Record the PR number, URL, branch, and merged commit hash. Delete the task
   branch after merge.

## Final Response After All Tasks

- List completed tasks with branch, PR URL, merge status, and merged commit.
- List tests/checks run.
- Include the exact final raw `/code-review` output for the last task.
- Note skipped checks, blockers, or residual risk.
- State plainly whether `/code-review` is clean and the guard is ready to arm
  (move from `observe` to `enforce`) after a soak.

## Implementation Tasks

### Task 1: Multi-Provider Usage And Sessions

Branch `task/multi-provider-usage`. Parallel-safe; independent of the guard.

Scope:
- `internal/providers/providers.go`: add `UsageAll(ctx, cfg, date)` and
  `SessionsAll(ctx, cfg, date, limit)` that loop `Enabled(cfg)` and aggregate
  across providers, mirroring the `loadLimits` "a failing provider becomes a
  row, never blanks the others" behavior.
- Merge helper (new `internal/usage/merge.go` or a function in
  `internal/usage`): merge `UsageSummary`s as `Provider: "all"`, totals summed,
  `Groups` concatenated unmodified (each `UsageGroup` already carries
  `Provider`, which is the disambiguator) and re-sorted by tokens desc. Sessions:
  concatenate, re-sort by tokens, apply limit after merge.
- `internal/tui/model.go`: `loadUsage`/`loadSessions` use the aggregators; add a
  `PROVIDER` column to `usageContent()` and `sessionsContent()`; de-hardcode the
  "No Codex sessions" / "Loading" strings.
- `internal/cli/root.go`: `usage today` and `usage sessions` accept
  `--provider all`; keep `codex` as the default flag value for compatibility;
  render the PROVIDER column when aggregating.

Acceptance:
- `usage today --provider all --json` totals equal the sum of per-provider runs;
  TUI Usage/Sessions show codex and claude rows with a PROVIDER column; one
  provider erroring does not blank the other; single-provider behavior is
  unchanged.

Checks: `go test ./...`, `go vet ./...`, `git diff --check`,
`/tmp/agent-tools usage today --provider all`, `... usage sessions --provider all`.

Suggested commit: `feat: aggregate usage and sessions across providers`.

### Task 2: Guard Core (Config, State, Policy, Status)

Branch `task/guard-core`. No enforcement, no hooks.

Scope:
- `internal/config/config.go`: `GuardConfig` structs under `[usage.guard]` with
  defaults `enabled=false`, `mode="observe"`, `default_action="deny"`,
  `warn_at_percent=80`, `refresh_seconds=60`, `stale_after_seconds=600`,
  `fail_mode="open"`, `deny_escalation_count=5`; `[usage.guard.quota]`
  (`action`, `min_5h_percent`, `min_week_percent`); and a
  `map[string]GuardBudget` for `[usage.guard.budgets."<cwd>"]` (same koanf
  quoted-key pattern as `Profiles`). Round-trip through koanf, including quoted
  path keys.
- New `internal/guard/{state.go,policy.go,refresh.go}` (copy cache/flock/atomic
  helpers from `internal/claude/limits.go`): `refresh` calls `providers.*`
  (`LimitsFor` force=false), evaluates policy (longest-prefix cwd match;
  zero-valued budget = unlimited; per-session spend tracked by session id), and
  writes `~/.cache/agent-tools/guard/state.json` (flock + atomic 0600). State
  schema: `evaluated_at`, quota verdicts, per-scope `{budget, spent, verdict,
  sessions[]}`, `overrides[]`, `deny_counts`, `last_warned` — no tokens/secrets.
- `internal/cli/guard.go`: new `guard` cobra group with `refresh` and
  `status [--json]` (table of scopes/quota/verdicts; flag never-matched budget
  scopes to surface config typos).

Acceptance:
- `guard refresh` writes a valid 0600 state file using only `providers.*` calls
  with `force=false`; longest-prefix cwd matching, zero=unlimited, and koanf
  round-trip are unit-tested; `guard status` renders verdicts.

Checks: `go test ./...`, `go vet ./...`, `git diff --check`,
`/tmp/agent-tools guard refresh` then `guard status`.

Suggested commit: `feat: add usage guard core (config, state, policy)`.

### Task 3: Guard Check (Fast Hook Path, Observe-Only)

Branch `task/guard-check`. Depends on Task 2.

Scope:
- `internal/guard/{check.go,hook.go}`: parse PreToolUse stdin for both providers
  (`session_id`, `cwd`, `tool_name`); output encoders for the Claude deny JSON
  (`hookSpecificOutput.permissionDecision="deny"`), `{"continue":false}`,
  `systemMessage` warning, and the Codex deny-only fallback. Match the hook's
  `cwd`/`session_id` to a state verdict. Stale-while-revalidate: serve the
  current verdict and, if stale, spawn a detached `guard refresh` (non-blocking
  `refresh.lock` + a `refresh_attempted_at` stamp to prevent stampedes).
  Fail-open on missing/corrupt/stale state (except sticky breach verdicts);
  `fail_mode="closed"` opt-in. Observe mode always allows but records the
  would-be verdict.
- `internal/cli/guard.go`: `guard check --provider codex|claude` reading stdin,
  writing the hook response to stdout, exit codes per contract.

Acceptance:
- `echo '<payload>' | agent-tools guard check --provider claude` completes in
  < 50ms with warm state (latency assertion in tests); observe mode always
  allows but records; stale state triggers exactly one background refresh
  (verified via the stamp); the full failure-mode table is covered by golden
  tests; breach stickiness is tested.

Checks: `go test ./...`, `go vet ./...`, `git diff --check`, the piped-payload
smoke above for both providers.

Suggested commit: `feat: add guard check fast path and hook I/O`.

### Task 4: Guard Install-Hooks And Doctor

Branch `task/guard-install-hooks`. Depends on Task 3.

Scope:
- `internal/guard/install.go` + `internal/cli/guard.go`: `guard install-hooks
  [--print|--project|--remove]`. Marker-based idempotent merge (entries whose
  command contains `agent-tools guard check`) into `~/.claude/settings.json`
  (PreToolUse matcher `*` + UserPromptSubmit) and `~/.codex/hooks.json`
  (PreToolUse). Use the absolute `os.Executable()` path with `--provider` baked
  in. Back up to `<file>.agent-tools.bak`, write atomically, and **abort on
  invalid existing JSON — never clobber**. `--print` shows the resulting JSON
  without writing; `--project` targets `.claude/settings.json` in cwd.
- `internal/cli/doctor.go`: guard checks — hooks installed, hook binary path
  matches the current executable (stale-after-upgrade trap), state freshness,
  and a Codex one-time-trust reminder.

Acceptance:
- Install is idempotent (run twice = identical file); unrelated user hooks are
  preserved byte-for-byte in tests; invalid existing JSON aborts with guidance;
  a backup is written; `--print` writes nothing; doctor flags a moved binary.

Checks: `go test ./...`, `go vet ./...`, `git diff --check`,
`/tmp/agent-tools guard install-hooks --print`.

Suggested commit: `feat: add guard install-hooks and doctor checks`.

### Task 5: Guard Enforce (Arm Warn/Deny/Stop)

Branch `task/guard-enforce`. Depends on Task 4.

Scope:
- `internal/guard/{check.go,policy.go}`: implement the actions — `warn` at
  `warn_at_percent` (allow + throttled `systemMessage`, once per scope per
  10 min, recorded in `last_warned`), `deny` at 100% (`permissionDecision=deny`
  with a reason instructing the model to stop and summarize), and `stop`
  (`continue:false` on Claude; deny fallback on Codex). Deny-count escalation:
  after `deny_escalation_count` consecutive denies per session, Claude escalates
  to `stop`. Quota-floor breaches default to `stop` (Claude) / deny (Codex).
- `guard release [--scope <path>|--all] [--for 2h]`: write a temporary override
  (default 1h) into state under flock; `check` honors unexpired overrides
  (allow + systemMessage noting the snooze). Never edits config.

Acceptance:
- With `mode=enforce`, over-budget tool calls are denied with a model-visible
  reason; warn fires once per throttle window at 80%; 5 consecutive denies
  escalate to `continue:false` on Claude and stay deny on Codex; `guard release
  --for 1h` unblocks immediately and expires; a quota-floor breach stops on
  Claude / denies on Codex; observe-mode behavior is unchanged.

Checks: `go test ./...`, `go vet ./...`, `git diff --check`, and an enforcement
smoke on a **scratch project** (`/tmp/guard-test`) with a tiny budget — never on
live work sessions.

Suggested commit: `feat: arm usage guard enforcement actions`.

### Task 6: Guard Watch Daemon, Telegram Alerts, And TUI

Branch `task/guard-watch-alerts`. Depends on Task 5.

Scope:
- `internal/guard/watch.go` + `internal/cli/guard.go`: `guard watch
  [--interval|--once|--dry-run]`, a loop modeled on `Publisher.Run` in
  `internal/herdr/publisher.go`, that keeps state fresh and diffs verdict
  **transitions** (ok→warn, warn→breach, breach→ok). On each transition, shell
  out to `agentgram send` (throttled; message includes scope/spend/action, never
  token values); `--dry-run` prints instead of sending.
- `internal/tui/model.go`: `alertsContent()` additionally reads the guard state
  file (read-only, no evaluation) and appends breach/warn lines.
- `[usage.guard.alerts]` config (agentgram command, throttle).

Acceptance:
- `watch` keeps state fresher than `refresh_seconds`; verdict transitions (not
  steady states) produce exactly one `agentgram send` each; `--dry-run` prints
  instead; the TUI Alerts page shows guard breaches; no token values appear in
  any message.

Checks: `go test ./...`, `go vet ./...`, `git diff --check`,
`/tmp/agent-tools guard watch --once --dry-run`.

Suggested commit: `feat: add guard watch daemon, telegram alerts, and TUI`.

## Final Completion Criteria

All six tasks merged, `main` clean and verified, the tracking issue (#15)
updated with the merged-PR list and honest residual risks and closed, and a
Telegram handoff sent via Agentgram. The guard ships in `observe` mode; arming
it (`mode=enforce`) is a deliberate post-soak step, not part of this goal.
