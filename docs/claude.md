# Claude Provider

`agent-tools` reports Claude Code (Pro/Max) subscription quota and token usage
alongside Codex. The provider is enabled by default.

## What It Reads

- **Quota / limits** come from Claude Code's authenticated usage endpoint
  (`/api/oauth/usage`), using the OAuth access token from your Claude Code
  credentials. It reports the 5-hour and weekly (7-day) windows as
  *remaining* percentages plus their reset times.
- **Token usage / sessions** come from [`ccusage`](https://github.com/ryoppippi/ccusage)
  (`ccusage claude daily|session`), which reads local Claude Code transcripts.
  No network and no auth are involved.

## Credentials

On Linux, Claude Code stores OAuth credentials at:

```text
~/.claude/.credentials.json
```

agent-tools reads (never writes) this file. When the access token is rejected,
it refreshes it against Claude's OAuth token endpoint and caches the rotated
token privately under the agent-tools cache dir — it does **not** write back to
`.credentials.json`.

**macOS is not yet supported**: there, Claude Code keeps the same credentials in
the Keychain rather than a file. `agent-tools usage doctor` reports this clearly.

## Multiple Subscriptions

Like Codex's `~/.codex-<name>` convention, additional Claude subscriptions are
discovered from `~/.claude-<name>` directories that contain a
`.credentials.json`. Each becomes a profile (named `<name>`). With no such
directories, the single `~/.claude` profile (`default`) is used.

You can also configure profiles explicitly:

```toml
[usage.providers.claude.profiles.work]
home = "~/.claude-work"
label = "work"
```

## The Rate-Limit Cooldown (important)

Claude's usage endpoint rate-limits aggressively: polling it every 30–60s
returns `429`, and that state is sticky for tens of minutes with no
`Retry-After`. agent-tools is therefore deliberately cache-first:

- Limits refresh at most once every 5 minutes (the floor, even if
  `refresh_limits_seconds` is lower).
- On a `429`, the provider records a 30-minute cooldown in its cache, serves the
  last-known numbers with source `cache (rate-limited)`, and makes **no** calls
  to the endpoint until the cooldown expires.
- `--force-refresh` does **not** bypass an active cooldown — retrying a sticky
  429 only extends the lockout.

This is why the TUI and Herdr publisher read the cache and never force a fetch.

## Limitations (v1)

- Sessions are not correlated to running processes (Claude Code does not put the
  session ID in its process arguments), so the `ACTIVE` column is always `no`.
- `usage doctor` validates the credentials file shape (and flags an expired
  token) without ever printing token values.
