# CronPlus AI Task Authoring Guide

Use this instruction when asking an AI agent to create a task package for CronPlus.

## Objective

Create a self-contained Python task package that CronPlus can import, validate, schedule, run, and optionally deliver to Telegram. CronPlus does not create or edit task packages itself. The manifest is the source of truth.

## Required Output

Return a directory containing:

```text
my-task/
  script.py
  my-task.cronplus.yaml
  requirements.txt        # only when third-party packages are needed
  README.md               # recommended
  sample_output.json      # recommended when structured output is meaningful
```

Use exactly one manifest file ending in `.cronplus.yaml` or `.cronplus.yml`. Multiple manifests in one directory are rejected.

## Manifest Rules

Use this shape unless the user gives different requirements:

```yaml
manifest_version: 1
script:
  path: ./script.py
  name: Human Readable Task Name
  description: Short description of what the task checks or does.
runtime:
  environment:
    strategy: managed_venv
    requirements_file: ./requirements.txt
  working_directory: .
  timeout_seconds: 120
  max_output_kb: 512
  isolated_run: true
  resource_limits:
    graceful_kill_seconds: 5
schedule:
  type: cron
  expression: "0 * * * *"
  timezone: UTC
  missed_run_policy: skip
delivery:
  profiles: []
  send_on: [success, failure]
dependencies:
  tasks: []
result_contract:
  version: 1
  expect_structured_result: true
  result_prefix: "CRONPLUS_RESULT="
```

Field guidance:

- `script.path` must point to an existing Python file.
- `script.name` is optional but should be set; if empty, CronPlus falls back to the package directory name.
- Default runtime strategy is `system`, but use `managed_venv` when the script needs third-party packages.
- `managed_venv` creates `.cronplus-venv` inside the package and installs `requirements_file` if present.
- `venv_path` uses an existing virtual environment and requires `runtime.environment.venv_path`.
- `runtime.env_file` must exist if specified. Omit it if the task has no local env file.
- `runtime.env` supports `plain` values and `secret` values using `env://NAME`.
- `schedule.expression` is standard 5-field numeric cron: `minute hour day-of-month month day-of-week`.
- Cron field forms supported: `*`, `*/N`, single numbers, ranges, and comma lists. Month/day names are not supported.
- Sunday can be `0` or `7`.
- If day-of-month and day-of-week are both restricted, a run matches when either day field matches.
- `missed_run_policy` must be `skip`; CronPlus does not backfill missed runs.
- `delivery.send_on` supports `success`, `failure`, `warning`, and `skipped`; `failed` is accepted as an alias for `failure`.
- `dependencies.tasks` is optional. Each dependency must use exactly one of `slug` or `id`; `require_status` defaults to `success`; `max_age_seconds` is optional; `on_unhealthy` defaults to `skip` and can be `fail`.
- Dependencies are checked against imported-task run history. `cronplus check` is a diagnostic probe and does not create run history or satisfy dependencies; use **Run Now** on the imported dependency task for that. After import, the web UI and MCP `cronplus.tasks.dependency_health` tool can inspect target resolution, latest upstream status, and freshness.

## Python Script Contract

The script must be runnable as:

```bash
python3 script.py
```

Write ordinary logs to stdout/stderr. At the end, print at most one final structured result line:

```python
print("CRONPLUS_RESULT=" + json.dumps(result, separators=(",", ":")))
```

CronPlus scans stdout for the last line beginning with the configured prefix. If CronPlus can parse the structured result and its `status` is valid, that status is authoritative for run state and delivery matching, even if the process exits non-zero. Invalid JSON is ignored. Unknown result statuses become `failure`.

Use `status` for CronPlus run state. Add any other JSON fields the task needs for UI/API output or delivery templates:

```python
result = {
    "status": "success",
    "message": "Short human-readable message",
    "details": {
        "count": 3
    }
}
```

Status rules:

- Use `success` when the task completed and the desired condition is normal.
- Use `failure` when the task could not complete or detected a critical problem.
- Use `warning` when the task completed but found a non-critical issue.
- Use `skipped` when the task intentionally did no work.
- Prefer exit code `0` for `success`, `warning`, and intentional `skipped`.
- Prefer non-zero exit code for true execution failures.
- If the final structured result is missing or invalid JSON, CronPlus uses the exit code as the run state.

Keep fields concise enough for local storage and delivery. Avoid putting huge payloads in the structured result.

## Resource-Safe Script Requirements

CronPlus protects itself, but the task script should still be careful:

- Set timeouts on all network calls.
- Close files, HTTP sessions, database connections, subprocesses, and browser instances.
- Wrap cleanup in `try/finally` or context managers.
- Do not create background daemons or infinite loops.
- Do not write large unbounded logs; CronPlus truncates output, but concise logs are easier to debug.
- For browser automation, always use CronPlus run directories:
  - user data/profile: `CRONPLUS_BROWSER_USER_DATA_DIR`
  - downloads: `CRONPLUS_BROWSER_DOWNLOADS_DIR`
  - cache: `CRONPLUS_BROWSER_CACHE_DIR`
- For temporary files, use `CRONPLUS_RUN_DIR` or the standard temp directory after CronPlus has set `TMPDIR`.
- Do not store durable task state in the isolated run directory; it is removed after the run. Store intentional durable state in the package directory or an explicitly configured path.

CronPlus injects these environment variables during runs:

| Variable | Meaning |
|---|---|
| `CRONPLUS_TASK_ID` | Imported task ID |
| `CRONPLUS_RUN_ID` | Run record ID |
| `CRONPLUS_TASK_DIR` | Package directory |
| `CRONPLUS_RUN_DIR` | Per-run directory when isolation is enabled |
| `CRONPLUS_BROWSER_USER_DATA_DIR` | Browser profile directory |
| `CRONPLUS_BROWSER_DOWNLOADS_DIR` | Browser download directory |
| `CRONPLUS_BROWSER_CACHE_DIR` | Browser cache directory |

## Delivery Template Guidance

If the manifest includes `delivery.message_template`, use Go `text/template` syntax:

```yaml
delivery:
  profiles: [my-telegram]
  send_on: [failure, warning]
  message_template: |
    {{with .body}}{{.}}{{else}}{{.summary}}{{end}}
```

Standard template actions and built-ins are available, including `if`, `else`, `with`, `range`, `index`, `len`, `printf`, and comparisons such as `eq` and `ne`. CronPlus does not register custom template functions. Missing fields are errors, so reference optional fields with `with` or make sure the script always emits them.

The parsed result JSON object contributes fields to the template root, and CronPlus also adds defaults such as `.task`, `.status`, `.summary`, `.body`, `.data`, `.stdout`, `.stderr`, `.exitcode`, and `.duration`. PascalCase aliases such as `.TaskName`, `.Status`, `.Summary`, `.Body`, and `.Data` are also available.

Simple field outputs can use shorthand. If the script emits `{"message":"ok","details":{"count":3}}`, the manifest can use `{{message}}` and `{{details.count}}`; CronPlus rewrites those to dotted paths. Template actions should use normal Go template syntax, such as `{{with .data}}...{{end}}`. CronPlus sends only when the rendered template is not empty.

Only use inline delivery profiles when the user explicitly wants the package to define them. Telegram config keys are `bot_token` and `chat_id`. Inbound commands are enabled only on daemon delivery profiles, not inline manifest profiles.

## Validation Workflow

After generating the package:

1. Check the manifest file exists and is the only `.cronplus.yaml` or `.cronplus.yml` file in the directory.
2. If dependencies are needed, create `requirements.txt` and use `runtime.environment.strategy: managed_venv`.
3. Run:

```bash
cronplus validate /path/to/my-task
cronplus check /path/to/my-task
```

`cronplus check` prepares the environment and executes the package once, but it is not a scheduled/imported task run. It does not create run history, trigger delivery, or satisfy manifest dependencies.

For imported tasks, use the web UI schedule preview or MCP `cronplus.schedules.preview` to confirm upcoming run times, especially for disabled tasks where the normal next-run field is intentionally empty. Use the task environment view or MCP `cronplus.tasks.environment` to inspect managed/custom venv paths and environment size.

If running from the CronPlus source tree without an installed binary, use:

```bash
go run . validate /path/to/my-task
go run . check /path/to/my-task
```

Fix all validation errors before considering the task complete. Warnings should be resolved when practical, especially missing `script.name`.

For MCP clients, use `cronplus.task_package.validate` before import. After import, use `cronplus.tasks.check` only for a diagnostic probe of the current package, and use `cronplus.runs.start` followed by `cronplus.runs.list`, `cronplus.runs.get`, or `cronplus.runs.wait` when you need real imported-task run history. MCP delivery tools manage daemon delivery profiles; profile list responses redact secrets, and `cronplus.deliveries.update` preserves omitted secrets and existing non-secret fields.

## Minimal Example

`script.py`:

```python
import json
import os


def main() -> int:
    task_dir = os.environ.get("CRONPLUS_TASK_DIR", ".")
    result = {
        "status": "success",
        "summary": f"Task directory is {task_dir}",
        "data": {"task_dir": task_dir},
    }
    print("CRONPLUS_RESULT=" + json.dumps(result, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        result = {
            "status": "failure",
            "summary": str(exc),
            "data": {"error_type": type(exc).__name__},
        }
        print("CRONPLUS_RESULT=" + json.dumps(result, separators=(",", ":")))
        raise SystemExit(1)
```

`my-task.cronplus.yaml`:

```yaml
manifest_version: 1
script:
  path: ./script.py
  name: Example Task
  description: Minimal CronPlus task example.
runtime:
  environment:
    strategy: system
  timeout_seconds: 60
  max_output_kb: 128
  isolated_run: true
  resource_limits:
    graceful_kill_seconds: 5
schedule:
  type: cron
  expression: "0 * * * *"
  timezone: UTC
  missed_run_policy: skip
delivery:
  profiles: []
  send_on: [success, failure]
result_contract:
  version: 1
  expect_structured_result: true
  result_prefix: "CRONPLUS_RESULT="
```

## Final Checklist For The AI Agent

- The package has exactly one manifest.
- The manifest references existing files.
- The selected Python environment strategy matches the dependencies.
- Network/browser code has explicit timeouts and cleanup.
- Browser profiles/downloads/cache use CronPlus-provided run directories.
- The script prints a valid final `CRONPLUS_RESULT=` JSON line.
- The result status is one of `success`, `failure`, `warning`, or `skipped`.
- The task passes `cronplus validate`.
- The task passes `cronplus check` when it is safe and useful to execute the package as a diagnostic probe.
