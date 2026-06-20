# CronPlus

Local automation runner for Python task scripts. A single binary that schedules, executes, and delivers results — with a built-in web UI.

## What it does

- **Schedule** Python scripts using cron expressions
- **Deliver** results to Telegram (more channels coming)
- **Import and run** manifest-backed task packages from a web browser
- **Inspect health** with run history filters, dependency health, environment sizes, and storage summaries
- **Remote control** via Telegram commands (/status, /run, /list, etc.)

## Quick Start

```bash
# Build and run directly
make build
./cronplus
```

Open http://127.0.0.1:9876 in your browser. The auth token is at `~/.config/cronplus/auth-token`.

## Task Package

Create a directory with a script and manifest. The manifest is the source of truth; if it changes on disk, reload the import in CronPlus.

```
my-task/
├── script.py
├── my-task.cronplus.yaml
└── requirements.txt
```

### Manifest Example

```yaml
manifest_version: 1
script:
  path: ./script.py
  name: Price Watch
  description: Check product prices daily
runtime:
  environment:
    strategy: managed_venv
    requirements_file: ./requirements.txt
  timeout_seconds: 120
  env_file: ./.env
  isolated_run: true              # Default. Use per-run HOME/TMP/cache dirs
  resource_limits:
    graceful_kill_seconds: 5      # TERM before KILL for leaked children
    max_open_files: 1024          # Optional best-effort ulimit
schedule:
  expression: "0 */6 * * *"      # Every 6 hours
  timezone: America/Los_Angeles
  missed_run_policy: skip         # Missed runs are not backfilled
delivery:
  profiles: [my-telegram]
  send_on: [success]
```

### Local Environment Values

For personal local secrets or per-task settings, use a package-local dotenv file and/or environment references:

```yaml
runtime:
  env_file: ./.env
  env:
    API_TOKEN:
      type: secret
      value: env://MY_API_TOKEN
    MODE:
      type: plain
      value: daily
```

`env_file` is resolved relative to the manifest directory and supports simple `KEY=value` lines, optional `export KEY=value`, blank lines, and comments. Final script environment precedence is: daemon process environment, then `env_file`, then manifest `runtime.env`.

CronPlus also injects `CRONPLUS_TASK_ID`, `CRONPLUS_RUN_ID`, `CRONPLUS_TASK_DIR`, and, when run isolation is enabled, `CRONPLUS_RUN_DIR`. Browser tasks should use `CRONPLUS_BROWSER_USER_DATA_DIR` and `CRONPLUS_BROWSER_DOWNLOADS_DIR` for temporary profiles and downloads.

### Resource Cleanup

Each run starts in its own process group. When a script exits, times out, or the daemon shuts down, CronPlus attempts to terminate leftover child processes, scans for detached processes that still reference the run directory, and removes the per-run temp/profile/cache directory.

The runner uses bounded stdout/stderr buffers, so noisy scripts cannot grow daemon memory before output is truncated. Run details record root PID, process group, output bytes, discarded output, detached cleanup, and run-directory cleanup status.

### Recommended AI-Generated Package

```
my-task/
├── script.py
├── my-task.cronplus.yaml
├── requirements.txt
├── README.md
└── sample_output.json
```

`README.md` and `sample_output.json` are optional, but they give humans and AI agents useful context when debugging or regenerating a task.

For a complete AI-facing task creation prompt, see [AI_TASK_AUTHORING_GUIDE.md](./AI_TASK_AUTHORING_GUIDE.md).

### Script Output

Scripts can optionally output structured results:

```python
import json

result = {
    "status": "success",
    "summary": "Price dropped to $19.99",
    "deliverable": {
        "kind": "text",
        "body": "Price dropped to $19.99"
    }
}
print(f"CRONPLUS_RESULT={json.dumps(result)}")
```

Supported statuses are `success`, `failure`, `warning`, and `skipped`; `failed` is accepted as an alias for `failure`. When CronPlus can parse a structured result with a valid status, that status is authoritative for run state and delivery matching, even if the process exits non-zero. Unknown structured result statuses are treated as `failure`. If there is no parseable structured result, CronPlus falls back to the script exit code.

### Delivery Templates

`delivery.message_template` uses Go `text/template` syntax with standard actions and built-ins such as `if`, `with`, `range`, `index`, `len`, `printf`, `eq`, and `ne`. CronPlus does not add custom template functions, and missing fields are render errors.

The template root includes parsed result fields plus defaults such as `.task`, `.status`, `.summary`, `.body`, `.data`, `.stdout`, `.stderr`, `.exitcode`, and `.duration`. Simple field output can use shorthand like `{{summary}}` or `{{data.price}}`; control actions should use normal dotted syntax:

```gotemplate
{{with .body}}{{.}}{{else}}{{.summary}}{{end}}
```

### Task Dependencies

Tasks can declare prerequisite tasks in their manifest. CronPlus checks dependencies before marking the dependent task as running and before launching its script. The check uses the dependency task's latest completed imported-task run; an in-progress run does not count until it completes.

```yaml
dependencies:
  tasks:
    - slug: browser-manager
      require_status: success
      max_age_seconds: 3900
      on_unhealthy: skip
```

Each dependency uses exactly one of `slug` or `id`. `require_status` defaults to `success`; `max_age_seconds` is optional and disables freshness checks when omitted or set to `0`; `on_unhealthy` defaults to `skip` and can also be `fail`. When a dependency is unhealthy, CronPlus records a completed run with status `skipped` or `failure` without launching the dependent script, without publishing a `run_started` event, and without consuming an active-run slot.

Package checks do not satisfy dependencies. `cronplus check` and the web UI **Diagnostic Check** action validate a package, prepare its environment, and run its script as a diagnostic probe, but they do not create imported-task run history. Use **Run Now** on the imported dependency task to create the successful run record that downstream dependencies require.

The web UI task detail page shows dependency health for every configured prerequisite and lists downstream dependents. The API and MCP tools expose the same information through dependency-health and dependents endpoints/tools.

### Browser Automation Tasks

CronPlus has first-class support for scheduled visible-browser scripts through `runtime.browser`. This is intended for Playwright, Chromium, Chrome, or browser wrappers that cannot run headless because the target site uses bot checks.

```yaml
runtime:
  browser:
    enabled: true
    profile_mode: isolated        # isolated | copy_from | shared_external
    profile_source: ./profile     # required for copy_from/shared_external
    downloads_mode: isolated      # isolated | default
    cache_policy: isolated        # isolated | default | disabled
    cleanup_policy: keep_on_failure
    process_detection_hints: [Chromium, Chrome, cloakbrowser]
```

Supported patterns:

| Pattern | Use When |
|---|---|
| Per-run temporary profile: `profile_mode: isolated` | Each run can start clean and should leave no durable browser state. |
| Copied durable profile: `profile_mode: copy_from` | The task needs cookies/localStorage from a known profile but should mutate only a per-run copy. |
| Shared long-running browser manager: `profile_mode: shared_external` | A manager task owns the visible browser, and monitor tasks connect over CDP or a local debug port. |
| Dependency on browser freshness | Monitor tasks should run only after the browser manager has a recent successful run. |

Browser-enabled runs record the resolved profile, download, and cache paths; profile-copy status; cleanup policy; cleanup status; suspected leftover process count; and browser output bytes. When `profile_mode: copy_from` cannot copy the source profile, CronPlus fails the run before launching the script so the task does not navigate with an empty or partial profile. `downloads_mode: default` and `cache_policy: default` leave the corresponding browser path variables empty; `cache_policy: disabled` also leaves the cache path empty so the script can pass browser-specific no-cache flags.

`process_detection_hints` are recorded in diagnostics and exposed to the script as `CRONPLUS_BROWSER_PROCESS_HINTS`. CronPlus process cleanup still primarily identifies detached children by process group and references to the isolated run directory; shared external browser managers should own their own recycling logic.

The Health page includes a Browser Automation section with active browser runs, recent browser failures, retained run/profile directories, and browser storage usage.

Official templates are under `templates/browser`:

- `browser-manager`: starts or safely refreshes a visible browser session. By default it recycles only a process whose command line contains its configured `--user-data-dir`; if the debug port is owned by something else, it fails instead of killing an unrelated process. Set `BROWSER_RESTART_EXISTING=0` to only check/reuse an existing session.
- `managed-monitor`: connects to the existing browser manager and uses a dependency freshness gate.
- `one-shot-playwright`: opens a visible isolated Playwright Chromium context for one run.
- `profile-copy-helper`: verifies copied-profile behavior and reports copied profile size.

Copy-ready example packages are under `examples/browser`:

- `one-shot-browser`: a complete isolated visible Playwright browser task.
- `profile-copy-browser`: a complete copied-profile browser task for sites that need existing cookies or localStorage without mutating the source profile.

On macOS, long-lived visible Chromium sessions can accumulate GPU, compositor, extension, profile-cache, or WindowServer pressure. Restart browser-manager sessions periodically, keep monitor tasks short, prefer `copy_from` when a task needs cookies but not persistent mutation, and use `keep_on_failure` only while debugging. `--disable-gpu` can reduce GPU/WindowServer pressure for some sites, but it can also change rendering and fingerprint signals. Headless mode may fail on antibot-heavy sites because browser fingerprints, extension state, windowing APIs, and user-session behavior differ from a normal visible browser.

### Task Lifecycle

CronPlus does not create task packages and does not edit task files. AI agents or humans create package directories that follow the manifest contract; CronPlus imports those packages, validates them, schedules them, and records runs.

| Action | Meaning |
|---|---|
| **Import** | Register a task package by directory path. Returns immediately after manifest validation; `managed_venv` environment setup continues in the background. |
| **Diagnostic Check** | Validate a package, prepare its environment, and run its script once as a diagnostic probe. Does not create imported-task run history and does not satisfy task dependencies. |
| **Reload Manifest** | Re-read the package manifest from disk, preserving task ID and run history |
| **Run Now** | Execute the imported task immediately. Blocked while environment setup is `pending` or `failed` for `managed_venv` tasks. |
| **Remove Import** | Unregister the task from CronPlus without deleting package files |

Tasks expose environment details in the API and web UI, including strategy, resolved Python/requirements paths, setup state, and virtual-environment size. `managed_venv` tasks can be rebuilt from the web UI, API, or MCP; CronPlus removes the package-local `.cronplus-venv` and prepares it again. Custom `venv_path` environments report size but are not deleted or rebuilt by CronPlus because they may be shared.

## CLI

The binary also includes small commands that are useful for AI-generated task packages:

```bash
# Validate the manifest contract
cronplus validate /path/to/my-task

# Validate, prepare environment, run once, and verify structured output when required.
# This is a diagnostic check; it does not create imported-task run history.
cronplus check /path/to/my-task

# Print the embedded JSON Schema
cronplus schema

# Talk to the local daemon
cronplus status
cronplus list
cronplus import /path/to/my-task
cronplus reload <task-id>
cronplus run <task-id>

# Serve MCP over stdio for MCP-capable AI clients
cronplus mcp

# Download the latest GitHub release and replace the installed binary
cronplus update
cronplus update --dry-run

# Manage macOS autostart
cronplus autostart install
cronplus autostart status
cronplus autostart uninstall
```

The machine-readable schema is also available in `schemas/manifest.schema.json`.

Daemon API commands use `CRONPLUS_PORT` when it is set. Otherwise they read the active port from `~/.config/cronplus/daemon.lock`, then fall back to `9876`.

`cronplus update` fetches the latest release from GitHub, selects the asset for the current OS/architecture, extracts the real `cronplus` binary, rejects launcher scripts or unsafe archives, and replaces the installed binary. By default it updates the current stable executable path when possible, otherwise `/opt/homebrew/bin/cronplus` on Apple Silicon/Homebrew Macs or `/usr/local/bin/cronplus` elsewhere. Use `--path /absolute/path/to/cronplus` to override the install target, `--dry-run` to inspect the selected release without changing files, and `--force` to reinstall the same version. Private or rate-limited GitHub access can use `GITHUB_TOKEN`.

## MCP

CronPlus includes a local MCP server for AI clients that support the Model Context Protocol:

```json
{
  "mcpServers": {
    "cronplus": {
      "command": "/absolute/path/to/cronplus",
      "args": ["mcp"]
    }
  }
}
```

Run the CronPlus daemon first with `cronplus` or `./cronplus`. The MCP command is a separate stdio adapter process launched by the AI client; it does not start a second daemon, scheduler, or `core.Engine`.

```
AI client
  -> cronplus mcp
      -> local REST API + auth token
          -> running cronplus daemon
              -> core.Engine and scheduler
```

`cronplus mcp` resolves the daemon using the same rules as daemon CLI commands: `CRONPLUS_PORT`, then `~/.config/cronplus/daemon.lock`, then `127.0.0.1:9876`. It reads the bearer token from `~/.config/cronplus/auth-token`.

MCP tools include:

| Tool | Purpose |
|---|---|
| `cronplus.status` | Read daemon status |
| `cronplus.health` | Read health and maintenance summary, including active runs, storage usage, and environment sizes |
| `cronplus.tasks.list` / `cronplus.tasks.get` | Inspect imported tasks |
| `cronplus.task_package.validate` | Validate a task manifest without running code |
| `cronplus.task_package.check` | Validate, prepare the environment, and run the script once as a diagnostic probe. Does not create imported-task run history or satisfy dependencies |
| `cronplus.tasks.check` | Run the same diagnostic check for an imported task's current package. Does not create imported-task run history or satisfy dependencies |
| `cronplus.tasks.import` / `reload` / `set_enabled` / `remove` / `delivery_preview` | Manage imported tasks and preview latest-run delivery text |
| `cronplus.tasks.dependency_health` / `dependents` | Inspect upstream dependency health and downstream dependent tasks |
| `cronplus.tasks.environment` / `environment_rebuild` | Inspect environment paths/size and rebuild managed venvs |
| `cronplus.schedules.preview` | Preview upcoming run times for a task or cron expression, including disabled tasks |
| `cronplus.runs.start` / `list` / `get` / `wait` | Start manual runs and inspect imported-task run history. `list` supports status, trigger, aggregate delivery status (`success`, `failed`, `skipped`, `none`), search, and limit filters |
| `cronplus.runs.active` / `active_get` / `cancel` | Inspect active runs with PID, elapsed time, run directory, live output tails, and request cancellation |
| `cronplus.retention.get` / `update` / `cleanup` | Inspect and apply run-history retention settings |
| `cronplus.deliveries.list` / `create` / `update` / `set_commands_enabled` / `remove` / `test` | Manage delivery profiles and send delivery tests |
| `cronplus.commands.list` / `clear` | Inspect and clear inbound command log records |
| `cronplus.system.pick_directory` | Open the daemon host's native directory picker when supported |

MCP resources include `cronplus://status`, `cronplus://health`, `cronplus://runs/active`, `cronplus://retention`, `cronplus://tasks`, `cronplus://deliveries`, `cronplus://commands`, active-run and task/run/environment/dependency resource templates, the manifest schema, and the task-authoring guide. The SSE-only `/api/events` stream has no request/response MCP tool equivalent. There is no HTTP MCP endpoint yet; MCP support is stdio-only.

Delivery profile tools follow the daemon API contract. `cronplus.deliveries.list` returns redacted profile metadata only; bot tokens and chat IDs are never returned through MCP. `cronplus.deliveries.create` accepts `name`, `bot_token`, `chat_id`, optional `id`, `enabled`, `inbound_commands_enabled`, and `authorized_chat_ids`. `cronplus.deliveries.update` uses `profile_id` plus any fields to change; omitted secrets and omitted non-secret fields keep their existing values.

For imported tasks, use `cronplus.runs.start` to create real run history and `cronplus.runs.list` / `get` / `wait` to inspect it. `cronplus.tasks.check` and `cronplus.task_package.check` are diagnostic probes only; they execute code but do not create imported-task run history, trigger delivery, or satisfy dependencies.

## Features

| Feature | Description |
|---|---|
| **Web UI** | Dark-themed dashboard with live updates via SSE |
| **Run History** | Filterable task run history with diagnosis summaries, delivery state, active-run cancellation, and retention controls |
| **Task Dependencies** | Manifest dependencies with pre-run gating, UI health checks, and dependent-task discovery |
| **Health Page** | Active runs with live output tails, retention settings, daemon paths, storage usage, environment sizes, and attention items |
| **Scheduler** | 5-field cron expressions with timezone support; 30-second evaluation tick (runs may start up to ~30s after the scheduled minute) |
| **Python Envs** | System Python by default, managed venv per task, or custom venv |
| **Delivery** | Telegram (more drivers planned) |
| **Inbound Commands** | Control CronPlus via Telegram messages |
| **Contract Checks** | CLI validation and run checks for AI-authored packages |
| **MCP** | Local stdio MCP adapter for AI clients |
| **Autostart** | macOS LaunchAgent install/status/uninstall command |
| **Persistence** | SQLite state at `~/.config/cronplus/state.db` with automatic legacy JSON import |
| **Auth** | Token file, auto-auth for localhost |
| **Single Binary** | Web UI embedded via `go:embed` |

## API

Full REST API at `http://127.0.0.1:9876/api/`:

```bash
# Get status
curl -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  http://127.0.0.1:9876/api/status

# Import a task
curl -X POST -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  -H "Content-Type: application/json" \
  -d '{"path":"/path/to/my-task"}' \
  http://127.0.0.1:9876/api/tasks/import

# Trigger a run
curl -X POST -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  http://127.0.0.1:9876/api/tasks/{id}/run
# Response includes {"taskID":"...","runID":"...","status":"started"}

# Reload a task manifest after editing files on disk
curl -X POST -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  http://127.0.0.1:9876/api/tasks/{id}/reload

# Preview latest-run delivery message
curl -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  http://127.0.0.1:9876/api/tasks/{id}/delivery-preview

# Inspect health and maintenance data
curl -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  http://127.0.0.1:9876/api/health

# Inspect active runs and cancel one by run ID
curl -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  http://127.0.0.1:9876/api/runs/active
curl -X POST -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  -H "Content-Type: application/json" \
  -d '{"reason":"manual maintenance"}' \
  http://127.0.0.1:9876/api/runs/active/{runID}/cancel

# Bound run-history growth
curl -X PUT -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  -H "Content-Type: application/json" \
  -d '{"maxRunsPerTask":50,"maxRunAgeDays":30,"maxRunOutputKB":256}' \
  http://127.0.0.1:9876/api/retention

# Inspect task dependency and environment state
curl -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  http://127.0.0.1:9876/api/tasks/{id}/dependencies/health
curl -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  http://127.0.0.1:9876/api/tasks/{id}/environment

# Preview a schedule, even for a disabled task
curl -X POST -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  -H "Content-Type: application/json" \
  -d '{"taskID":"{id}","count":5}' \
  http://127.0.0.1:9876/api/schedules/preview

# Send a delivery profile test message
curl -X POST -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  -H "Content-Type: application/json" \
  -d '{"message":"CronPlus delivery test"}' \
  http://127.0.0.1:9876/api/deliveries/{id}/test
```

## Telegram Commands

Enable inbound commands on a Telegram delivery profile, then send:

CronPlus clears Telegram's command menu and only adds contextual inline buttons to related responses, such as task run/last actions under `/list` and task-specific replies.

| Command | Description |
|---|---|
| `/status` | App health summary |
| `/list` | All tasks |
| `/run <slug>` | Run a task now |
| `/last <slug>` | Last run result |
| `/enable <slug>` | Enable a task |
| `/disable <slug>` | Disable a task |
| `/help` | Command reference |

## Build from Source

```bash
git clone https://github.com/ran-su/cronplus.git
cd cronplus

# Development build
go build -o cronplus .
./cronplus

# Production build with version injection
make build
./cronplus

# Run tests
make test
```

Before cutting a release, run the focused verification path in [RELEASE_CHECKLIST.md](./RELEASE_CHECKLIST.md), including the browser UI smoke checks.

### Configuration Flags

```bash
# Custom port
./cronplus --port 8080

# Or via environment variable
CRONPLUS_PORT=8080 ./cronplus

# Limit aggregate task load
CRONPLUS_MAX_CONCURRENT_RUNS=1 ./cronplus
```

## Run as a Service

### macOS (launchd)

```bash
# Install the binary somewhere stable
make install
# Override when needed, for example:
# make install INSTALL_BINDIR=/usr/local/bin

# Confirm the shell will run the freshly installed binary
command -v cronplus
cronplus --help

# Later upgrades can be installed from GitHub releases
cronplus update

# Install and load the user LaunchAgent
cronplus autostart install

# If you run CronPlus on a custom port
cronplus autostart install --port 9887

# Install for the next login without starting it now
cronplus autostart install --no-start

# Check or remove it later
cronplus autostart status
cronplus autostart uninstall
```

The service keeps CronPlus running in the background, starts it at login, and restarts it automatically. The command writes `~/Library/LaunchAgents/com.cronplus.daemon.plist` and logs to `~/Library/Logs/cronplus.log`. If CronPlus is already running when you install autostart, the command installs the LaunchAgent without starting a second daemon.

On Apple Silicon Macs, `make install` defaults to `/opt/homebrew/bin` when that directory exists; otherwise it uses `/usr/local/bin`. This avoids leaving an older `cronplus` earlier in `PATH`. Autostart refuses temporary build paths and shell launcher wrappers because launchd needs the real CronPlus binary at a stable, executable path.

## Configuration

All data stored in `~/.config/cronplus/`:

| File | Purpose |
|---|---|
| `auth-token` | API authentication token (stable across upgrades) |
| `daemon.lock` | Single-daemon lock with current PID, port, and start time |
| `state.db` | SQLite state database for imported tasks, profiles, settings, commands, and run history |
| `state.json` | Legacy JSON state file, imported once into `state.db` when present |

## License

MIT. See [LICENSE](./LICENSE).
