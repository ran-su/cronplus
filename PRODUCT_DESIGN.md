# CronPlus V2 — Product Design

## Goal

CronPlus is a local automation runner for Python task scripts. It runs as a background daemon, manages scheduled script execution, and delivers results to channels like Telegram. The primary interface is an embedded web UI served from a single binary.

## Product Positioning

CronPlus makes it easy for humans or LLMs to create small single-purpose Python scripts, drop them into the system, configure schedule + delivery, and let it handle the rest.

The task package manifest is the source of truth. The web UI imports, reloads, runs, enables/disables, and removes task imports; it does not create task packages or maintain a separate editable task configuration.

The CLI provides the contract surface for AI task authors: `validate` checks a package manifest, `check` prepares the environment and runs the task once, and `schema` prints the embedded JSON Schema.

Local secret handling stays file/env based for convenience: package-local dotenv files and `env://NAME` references are supported, while OS credential stores are intentionally out of scope for this personal-use product.

## Core Concepts

### Task Package
A directory containing a Python script and a `.cronplus.yaml` manifest:
```
my-task/
├── script.py
├── my-task.cronplus.yaml
└── requirements.txt (optional)
```

### Script Contract
- CronPlus executes `python <script_path>`
- Script writes logs to stdout/stderr
- Optional structured result: `CRONPLUS_RESULT=<json>` on the last matching line
```json
{"status":"success","summary":"3 items found","deliverable":{"kind":"text","body":"3 items found"}}
```

### Manifest
YAML configuration file (see MANIFEST_SCHEMA.md for full spec):
```yaml
manifest_version: 1
script:
  path: ./script.py
  name: My Task
schedule:
  expression: "*/5 * * * *"
delivery:
  profiles: [my-telegram]
  send_on: [success]
```

Manifest edits happen on disk. CronPlus reloads an imported task to re-read the manifest while preserving the task ID and run history.

### Recommended Package Shape

```
my-task/
├── script.py
├── my-task.cronplus.yaml
├── requirements.txt
├── README.md
└── sample_output.json
```

`README.md` and `sample_output.json` are optional but recommended for AI regeneration, debugging, and human review.

### Delivery Profile
A configured destination (e.g. Telegram bot). Defined via web UI or inline in manifests.

### Run
One execution of a script. Records exit code, stdout, stderr, parsed result, and delivery outcomes.

## Architecture

```
cronplus (single Go binary)
├── Core Engine — task registry, state
├── Scheduler — 30s tick, cron evaluation
├── Runner — os/exec with timeout
├── Delivery — pluggable drivers (Telegram)
├── Inbound — Telegram polling for commands
├── Persistence — JSON file (~/.config/cronplus/state.json)
├── REST API — net/http
├── SSE — real-time push to web UI
└── Web UI — embedded via go:embed
```

## Key Behaviors

1. **Scheduling**: 30-second tick evaluates cron expressions. Skip-if-running policy prevents overlap, and a daemon-level concurrent-run cap limits aggregate load.
2. **Environment**: System Python by default, with optional managed venv per task or custom venv path.
3. **Delivery**: After a run, evaluate send_on conditions, render message template, send via driver.
4. **Inbound Commands**: Telegram long-polling for /status, /list, /run, /help, etc.
5. **Persistence**: JSON file with atomic writes. State restored on daemon restart.
6. **Auth**: Auto-generated token at `~/.config/cronplus/auth-token`. Stable across upgrades.
7. **Task Lifecycle**: Import registers a package, reload re-reads its manifest, remove import unregisters it without deleting package files.
8. **Missed Runs**: Missed scheduled times are skipped; CronPlus does not backfill runs after downtime.
9. **Resource Cleanup**: Each run uses its own process group and per-run temp/profile/cache directory. CronPlus kills leftover process-group members, scans for detached processes referencing the run directory, and removes run artifacts.
10. **Diagnostics**: Runs record Python executable, script path, working directory, timeout, process IDs, output bytes/discards, run directory, cleanup results, and structured-result detection.
11. **Contract Checks**: CLI validation, schema output, and one-shot run checks help AI agents produce valid task packages before import.

## Distribution

- Single binary: `go build -o cronplus .`
- Local install: `make install`
- Package-manager distribution is future work.
- Opens web UI at http://127.0.0.1:9876
