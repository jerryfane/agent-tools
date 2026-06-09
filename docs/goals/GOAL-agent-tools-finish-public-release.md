# Goal: Finish Agent Tools Public Release Prep

Finish the remaining public-repo work for `agent-tools` task by task. Each
task must be reviewed, opened as its own pull request when it changes tracked
repo files, merged, and verified before moving on, unless a task is explicitly
non-code operational closeout.

The user-facing outcome is a shareable public MIT-licensed `agent-tools` repo
with the Codex usage CLI/TUI implementation documented, release scaffolding in
place, the tracking issue closed with a clear summary, and the user notified on
Telegram through Agentgram.

Primary tracker:

- GitHub issue: https://github.com/jerryfane/agent-tools/issues/1
- Target repo: `jerryfane/agent-tools`
- Target base branch: `main`

## Current State

- Task 2001 is merged in PR #2: CLI/config/provider registry.
- Task 2002 is merged in PR #3: Codex limits provider.
- Task 2003 is merged in PR #4: daily usage and session grouping.
- Task 2004 is merged in PR #5: terminal usage TUI.
- Task 2005 is merged in PR #6: Herdr publisher compatibility.
- Task 2006 has local work in progress on `task/docs-install-release`.

## Core Rules

- Work one task at a time in the listed order.
- Do not restart completed tasks 2001-2005 unless verification proves a
  regression from the current remaining task.
- Keep the repo public-safe. Do not commit auth files, token caches, local
  profile config, private session archives, local generated reports, or host
  specific secrets.
- Preserve existing behavior unless the current task explicitly changes it.
- Keep provider-specific logic behind provider boundaries.
- If implementation depends on external APIs, docs, CLIs, data formats,
  generated scripts, installers, service launchers, subprocess calls, env vars,
  config formats, or third-party libraries, verify the real contract with local
  commands and/or official sources before editing.

## Before Starting

1. Inspect repo state:
   - `git status --short --branch`
   - `git remote -v`
   - `git log --oneline --decorate -8`
2. Confirm GitHub tooling:
   - `gh auth status`
   - `gh repo view jerryfane/agent-tools`
3. Confirm Go tooling:
   - `go version`
4. Inspect issue #1 and the existing goal file:
   - `gh issue view 1 --comments`
   - `sed -n '1,260p' docs/goals/GOAL-agent-tools-usage-tui.md`

## Per-Task Workflow

1. Confirm the current task's scope.
2. Use the task branch named in that task.
3. Implement only that task.
4. Add focused tests/checks for changed behavior.
5. Run:
   - `gofmt -w` on touched Go files
   - `go test ./...`
   - `go vet ./...`
   - task-specific smoke commands listed below
6. Run `codex exec review --uncommitted` and preserve the exact raw output.
7. Fix review findings and rerun checks/review until clean or explicitly
   blocked.
8. Run `git diff --check`, inspect the final diff, commit, push, and open one
   PR for the task.
9. PR body must include:
   - WHAT
   - WHY
   - CHANGES
   - RESULTS
   - RISK
   - exact final raw `codex exec review --uncommitted` output
10. Merge the PR after checks/review pass, update local `main`, and verify the
    worktree is clean.

## Implementation Tasks

### Task 3001: Finish Docs, Doctor, CI, And Release Prep

Scope:

- Complete the existing `task/docs-install-release` branch.
- Finish `usage doctor` so it reports actionable local install/config checks
  without leaking token values.
- Finish README updates for install, configuration, commands, privacy
  boundaries, Codex-only limitations, `ccusage`, Herdr publisher behavior, and
  development checks.
- Add or finish docs for:
  - configuration
  - privacy/local data
  - provider design
- Add CI workflow scaffolding for formatting, tests, vet, and build.
- Add GoReleaser config for supported platforms only.

Acceptance criteria:

- A new user can clone, build, run `agent-tools usage doctor`, and understand
  how to configure Codex profiles without seeing private local paths in tracked
  docs.
- `usage doctor --json` emits valid JSON and reports optional Herdr absence as
  optional, not a hard failure.
- Auth validation checks JSON shape and required token/account fields without
  printing token values.
- Release config validates with GoReleaser.
- Windows release targets are excluded unless platform-specific file locking is
  implemented.

Checks:

- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `go build -o /tmp/agent-tools ./cmd/agent-tools`
- `/tmp/agent-tools usage doctor --json`
- `go run github.com/goreleaser/goreleaser/v2@latest check`
- `codex exec review --uncommitted`

Branch:

- `task/docs-install-release`

Suggested commit:

- `docs: add install and release prep`

### Task 3002: Merge Release Prep And Verify Main

Scope:

- Push `task/docs-install-release`.
- Open a PR against `main`.
- Include the required PR body sections and the exact final raw
  `codex exec review --uncommitted` output.
- Wait for CI if it exists and fix any failures.
- Merge with the repository's preferred method. If no preference is
  discoverable, use squash merge.
- Update local `main` after merge.

Acceptance criteria:

- The release-prep PR is merged.
- Local `main` matches `origin/main`.
- Local worktree is clean.
- The task branch is deleted remotely if the merge command safely supports it.

Checks after merge:

- `git status --short --branch`
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `go build -o /tmp/agent-tools ./cmd/agent-tools`
- `/tmp/agent-tools usage doctor --json`

Suggested commit:

- No direct commit. This task merges the Task 3001 PR.

### Task 3003: Close Issue And Send Telegram Handoff

Scope:

- Update issue #1 with a concise completion summary.
- Include the merged PR list:
  - PR #2 CLI/config/provider registry
  - PR #3 Codex limits provider
  - PR #4 daily usage and sessions
  - PR #5 terminal usage TUI
  - PR #6 Herdr publisher compatibility
  - release-prep PR from Task 3002
- Include final checks and any honest residual risks.
- Close issue #1.
- Run Agentgram doctor, then send the user a Telegram message with:
  - repo URL
  - issue URL
  - merged PR summary
  - final verification status

Acceptance criteria:

- Issue #1 is closed with a useful closeout comment.
- Telegram message is sent through Agentgram.
- No secrets, token values, auth JSON, private config, or session archives are
  included in the issue comment or Telegram message.

Checks:

- `gh issue view 1 --json state,url`
- `agentgram doctor`

Suggested commit:

- No commit. This is operational closeout.

## Final Completion Criteria

The goal is complete only when all remaining tasks above are done, issue #1 is
closed, the local `main` worktree is clean and verified, and the Agentgram
handoff message has been sent.

Final response must include:

- Completed tasks.
- PR URL for Task 3002.
- Final checks run.
- Whether issue #1 was closed.
- Whether Telegram was sent.
- Exact final raw `codex exec review --uncommitted` output for the last changed
  repo.

Do not claim interactive `/review` is clean. Say:
`codex exec review is clean; ready for manual /review.`
