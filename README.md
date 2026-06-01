# CronPlus

Local automation runner for Python task scripts. A single binary that schedules, executes, and delivers results — with a built-in web UI.

## What it does

- **Schedule** Python scripts using cron expressions
- **Deliver** results to Telegram (more channels coming)
- **Import and run** manifest-backed task packages from a web browser
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

Supported statuses are `success`, `failure`, `warning`, and `skipped`; `failed` is accepted as an alias for `failure`. Unknown structured result statuses are treated as `failure`. If no structured status is present, CronPlus falls back to the script exit code.

### Task Lifecycle

CronPlus does not create task packages and does not edit task files. AI agents or humans create package directories that follow the manifest contract; CronPlus imports those packages, validates them, schedules them, and records runs.

| Action | Meaning |
|---|---|
| **Import** | Register a task package by directory path |
| **Reload Manifest** | Re-read the package manifest from disk, preserving task ID and run history |
| **Run Now** | Execute the imported task immediately |
| **Remove Import** | Unregister the task from CronPlus without deleting package files |

## CLI

The binary also includes small commands that are useful for AI-generated task packages:

```bash
# Validate the manifest contract
cronplus validate /path/to/my-task

# Validate, prepare environment, run once, and verify structured output when required
cronplus check /path/to/my-task

# Print the embedded JSON Schema
cronplus schema

# Talk to the local daemon
cronplus status
cronplus list
cronplus import /path/to/my-task
cronplus reload <task-id>
cronplus run <task-id>

# Manage macOS autostart
cronplus autostart install
cronplus autostart status
cronplus autostart uninstall
```

The machine-readable schema is also available in `schemas/manifest.schema.json`.

Daemon API commands use `CRONPLUS_PORT` when it is set. Otherwise they read the active port from `~/.config/cronplus/daemon.lock`, then fall back to `9876`.

## Features

| Feature | Description |
|---|---|
| **Web UI** | Dark-themed dashboard with live updates via SSE |
| **Scheduler** | 5-field cron expressions with timezone support |
| **Python Envs** | System Python by default, managed venv per task, or custom venv |
| **Delivery** | Telegram (more drivers planned) |
| **Inbound Commands** | Control CronPlus via Telegram messages |
| **Contract Checks** | CLI validation and run checks for AI-authored packages |
| **Autostart** | macOS LaunchAgent install/status/uninstall command |
| **Persistence** | JSON file at `~/.config/cronplus/state.json` |
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

# Reload a task manifest after editing files on disk
curl -X POST -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  http://127.0.0.1:9876/api/tasks/{id}/reload

# Preview latest-run delivery message
curl -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  http://127.0.0.1:9876/api/tasks/{id}/delivery-preview

# Send a delivery profile test message
curl -X POST -H "Authorization: Bearer $(cat ~/.config/cronplus/auth-token)" \
  -H "Content-Type: application/json" \
  -d '{"message":"CronPlus delivery test"}' \
  http://127.0.0.1:9876/api/deliveries/{id}/test
```

## Telegram Commands

Enable inbound commands on a Telegram delivery profile, then send:

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

## Configuration

All data stored in `~/.config/cronplus/`:

| File | Purpose |
|---|---|
| `auth-token` | API authentication token (stable across upgrades) |
| `daemon.lock` | Single-daemon lock with current PID, port, and start time |
| `state.json` | Persisted tasks, profiles, run history |

## License

MIT. See [LICENSE](./LICENSE).
