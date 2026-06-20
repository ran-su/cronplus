# CronPlus V2 — Product Design

## Goal

CronPlus is a local automation runner for Python task scripts. It runs as a background daemon, manages scheduled script execution, and delivers results to channels like Telegram. The primary interface is an embedded web UI served from a single binary.

## Product Positioning

CronPlus makes it easy for humans or LLMs to create small single-purpose Python scripts, drop them into the system, configure schedule + delivery, and let it handle the rest.

The task package manifest is the source of truth. The web UI imports, reloads, runs, enables/disables, and removes task imports; it does not create task packages or maintain a separate editable task configuration.

The CLI provides the contract surface for AI task authors: `validate` checks a package manifest, `check` prepares the environment and runs the task once as a diagnostic probe, and `schema` prints the embedded JSON Schema. Checks do not create imported-task run history and do not satisfy task dependencies.

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

### Dependency
A manifest-declared prerequisite task. CronPlus gates dependent runs before the script starts, and exposes dependency health so users can see target resolution, latest prerequisite run status, freshness, and skip/fail policy.

### Environment
The Python execution environment for a task. CronPlus supports system Python, package-local managed venvs, and custom venv paths. The UI and API expose resolved paths, setup state, and venv directory size; only managed venvs are rebuildable by CronPlus.

## Architecture

```
cronplus (single Go binary)
├── Core Engine — task registry, state
├── Scheduler — 30s tick, cron evaluation
├── Runner — os/exec with timeout
├── Delivery — pluggable drivers (Telegram)
├── Inbound — Telegram polling for commands
├── Persistence — SQLite database (~/.config/cronplus/state.db)
├── REST API — net/http
├── SSE — real-time push to web UI
├── MCP stdio adapter — `cronplus mcp`, calls the running daemon over REST
└── Web UI — embedded via go:embed
```

## Key Behaviors

1. **Scheduling**: 30-second tick evaluates cron expressions, so minute-level schedules may start up to ~30s late. Skip-if-running policy prevents overlap, and a daemon-level concurrent-run cap limits aggregate load.
2. **Environment**: System Python by default, with optional managed venv per task or custom venv path. Import and reload return after manifest validation; managed-venv creation and `pip install` run in the background and block runs until `environmentSetup.state` is `ready`. Environment management reports venv size, resolved Python/requirements paths, and setup timestamps. Managed venv rebuild removes the package-local `.cronplus-venv` and prepares it again; custom `venv_path` directories are inspected but not deleted.
3. **Delivery**: After a run, evaluate send_on conditions, render message template, send via driver.
4. **Inbound Commands**: Telegram long-polling for /status, /list, /run, /help, etc.
5. **Persistence**: SQLite database at `~/.config/cronplus/state.db`. State restored on daemon restart. CronPlus v2.0 and later do not read legacy `state.json` files. Users upgrading from JSON-state releases must run the latest v1.x once to create `state.db` before upgrading to v2.0 or later.
6. **Auth**: Auto-generated token at `~/.config/cronplus/auth-token`. Stable across upgrades.
7. **Task Lifecycle**: Import registers a package, reload re-reads its manifest, remove import unregisters it without deleting package files.
8. **Missed Runs**: Missed scheduled times are skipped; CronPlus does not backfill runs after downtime.
9. **Resource Cleanup**: Each run uses its own process group and per-run temp/profile/cache directory. CronPlus kills leftover process-group members, scans for detached processes referencing the run directory, and removes run artifacts according to the task's cleanup policy.
10. **Diagnostics**: Runs record Python executable, script path, working directory, timeout, process IDs, output bytes/discards, run directory, cleanup results, browser paths/profile-copy status when enabled, and structured-result detection.
11. **Contract Checks**: CLI validation, schema output, and one-shot run checks help AI agents produce valid task packages before import. One-shot checks are diagnostic only; they do not write imported-task run history.
12. **Task Dependencies**: Dependencies are checked against imported-task run history before a dependent task is marked running. Unhealthy dependencies create a normal completed run record with status `skipped` or `failure`, without launching the script or consuming an active-run slot. The API/UI/MCP can report all dependency checks and downstream dependents.
13. **Schedule Preview**: Users can preview upcoming run times for a task or raw cron expression. Preview uses the manifest schedule helper and works for disabled tasks.
14. **Browser Automation**: Browser-enabled tasks declare profile mode, downloads mode, cache policy, cleanup policy, and process-detection hints in the manifest. CronPlus surfaces active browser runs, retained browser run directories, recent browser failures, and browser storage usage.
15. **Health And Maintenance**: The health surface summarizes daemon metadata, task/run counts, active runs, browser automation health, recent failures, attention items, storage usage, and environment sizes.
16. **MCP Integration**: MCP clients launch `cronplus mcp` as a long-lived stdio subprocess. That adapter does not own scheduler state; it resolves the local daemon, authenticates with the token file, and uses the REST API to reach the single `core.Engine`. MCP tools and resources mirror the request/response REST API surface where practical; the SSE-only event stream remains a web/API feature rather than an MCP tool.

## Distribution

- Single binary: `go build -o cronplus .`
- Local install: `make install`
- Package-manager distribution is future work.
- Opens web UI at http://127.0.0.1:9876
