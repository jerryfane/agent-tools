# Privacy And Local Data

`agent-tools` is designed for local terminal workflows. It can read sensitive
local files to report useful usage status. Treat command output as local
diagnostic data unless you have reviewed it.

## Files Read

Codex limits:

- `~/.codex*/auth.json`

Codex usage and session enrichment:

- `~/.codex/sessions/**/*.jsonl`

Process and Herdr mapping:

- `/proc`
- `HERDR_PANE_ID`
- `HERDR_CODEX_PROFILE`
- `CODEX_HOME`

Config:

- `~/.config/agent-tools/config.toml`

## Files Written

Codex limit cache and refreshed access-token cache:

- OS user cache dir under `agent-tools/codex`, unless `cache_dir` is configured.

Build and release output:

- `bin/`
- `dist/`

The repository `.gitignore` excludes local build output, local config, cache
directories, and logs.

## Network Use

`usage limits` calls the ChatGPT/Codex quota endpoint using the selected Codex
profile's local auth data. It does not send prompts to a model.

`usage today` and `usage sessions` call the configured `ccusage` command. If
that command is `npx --yes ccusage`, `npx` may download or update packages
according to your Node/npm setup.

`usage herdr-publisher` talks to the local Herdr CLI/socket.

## Output Cautions

Review output before sharing:

- `usage doctor` can include local profile paths.
- `usage sessions` can include cwd/repo names and last prompt previews.
- JSON output can include more detail than table output.

Do not commit:

- Codex auth files.
- `agent-tools` cache files.
- Local config containing private paths or account labels.
- Session logs or generated private reports.

