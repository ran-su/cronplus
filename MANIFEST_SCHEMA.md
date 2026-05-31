# CronPlus Manifest Schema (V2)

## Overview

Each task package contains a `.cronplus.yaml` manifest file that describes the script, its runtime, schedule, and delivery configuration.

## File Naming

- `<name>.cronplus.yaml` or `<name>.cronplus.yml`
- One manifest per directory

## Full Schema

```yaml
# Optional. Schema version for forward compatibility. Defaults to 1.
manifest_version: 1

# Required. Script configuration.
script:
  path: ./script.py              # Required. Relative or absolute path to script.
  name: My Task                  # Optional. Display name; package directory is used if empty.
  description: Does something.   # Optional. Shown in UI.

# Optional. Runtime configuration.
runtime:
  environment:
    strategy: managed_venv       # "system" | "managed_venv" | "venv_path"
    python_base_interpreter: /usr/bin/python3  # managed_venv base or system interpreter
    requirements_file: ./requirements.txt       # Installed into managed venv
    venv_path: ./my-venv         # For strategy: venv_path
  working_directory: .           # Script CWD. Relative to manifest dir.
  timeout_seconds: 120           # Kill script after this. Default: 120
  max_output_kb: 512             # Truncate stdout/stderr. Default: 512
  env_file: ./.env               # Optional dotenv file, relative to manifest dir.
  isolated_run: true             # Default. Per-run HOME/TMP/cache/profile dirs.
  resource_limits:
    graceful_kill_seconds: 5     # Default. TERM grace before KILL.
    max_open_files: 1024         # Optional best-effort ulimit.
    max_processes: 0             # Optional best-effort ulimit; 0 means unset.
    max_cpu_seconds: 0           # Optional best-effort CPU ulimit; 0 means unset.
    max_memory_mb: 0             # Optional best-effort memory ulimit; 0 means unset.
  env:                           # Environment variables
    MY_VAR:
      type: plain
      value: hello
    SECRET_KEY:
      type: secret
      value: env://MY_SECRET      # Read from daemon process environment

# Required. Cron schedule.
schedule:
  type: cron                     # Optional. Only "cron" supported. Defaults to cron.
  expression: "*/5 * * * *"     # Standard 5-field cron
  timezone: America/Los_Angeles  # IANA timezone. Default: UTC
  missed_run_policy: skip        # Only skip is supported; no backfill

# Optional. Delivery configuration.
delivery:
  profiles:                      # Profile IDs to send to
    - my-telegram
  send_on:                       # Conditions: "success", "failure", "warning", "skipped". Default: success + failure
    - success
    - failure
  message_template: |            # Go-style template. Optional.
    [{{.TaskName}}] {{.Status}}
    {{.Summary}}
  inline_profiles:               # Profiles defined directly in manifest
    - id: my-telegram
      name: My Telegram
      driver: telegram
      config:
        bot_token: "123456:ABC-DEF..."
        chat_id: "-100123456789"

# Optional. UI hints.
ui:
  category: Shopping
  tags: [alerts, prices]

# Optional. Result parsing contract.
result_contract:
  version: 1
  expect_structured_result: true
  result_prefix: "CRONPLUS_RESULT="    # Default
```

## Validation Rules

| Field | Rule |
|---|---|
| `manifest_version` | Defaults to `1`; if present, must be >= 1 |
| `script.path` | Must resolve to an existing file |
| `script.name` | Warning if empty; imported display name falls back to the package directory name |
| `schedule.type` | Defaults to `cron`; only `cron` is supported |
| `schedule.expression` | Must be valid 5-field cron |
| `schedule.timezone` | Must be a valid IANA timezone |
| `runtime.environment.strategy` | Must be `system`, `managed_venv`, or `venv_path` |
| `runtime.environment.venv_path` | Required when strategy is `venv_path` |
| `runtime.timeout_seconds` | Must be greater than 0 |
| `runtime.max_output_kb` | Must be greater than 0 |
| `runtime.env_file` | If present, must resolve to an existing file |
| `runtime.env.*.type` | Must be `plain` or `secret`; `secret` currently supports `env://NAME` |
| `runtime.resource_limits.graceful_kill_seconds` | Must be greater than 0 |
| `runtime.resource_limits.*` | Optional hard limits must be greater than or equal to 0 |
| `schedule.missed_run_policy` | Must be `skip` |
| `delivery.inline_profiles[].id` | Required, non-empty |
| `delivery.inline_profiles[].driver` | Required, non-empty |

## Defaults

| Field | Default |
|---|---|
| `manifest_version` | `1` |
| `runtime.environment.strategy` | `system` |
| `runtime.timeout_seconds` | `120` |
| `runtime.max_output_kb` | `512` |
| `runtime.isolated_run` | `true` |
| `runtime.resource_limits.graceful_kill_seconds` | `5` |
| `schedule.type` | `cron` |
| `schedule.timezone` | `UTC` |
| `schedule.missed_run_policy` | `skip` |
| `delivery.send_on` | `["success", "failure"]`; `"failed"` is accepted as a compatibility alias for `"failure"` |
| `result_contract.result_prefix` | `CRONPLUS_RESULT=` |

## JSON Schema

The machine-readable schema lives at `schemas/manifest.schema.json` and is embedded in the binary:

```bash
cronplus schema
```

The JSON Schema is a machine-readable authoring aid. Runtime validation also checks filesystem paths, timezones, cron field ranges, and duplicate manifests.

## Cron Semantics

CronPlus supports standard 5-field numeric cron expressions:

```
minute hour day-of-month month day-of-week
```

Supported field forms are `*`, `*/N`, single numbers, ranges such as `1-5`, and comma-separated lists such as `0,15,30,45`. Names such as `MON` or `JAN` are not supported. Sunday can be `0` or `7`.

When both day-of-month and day-of-week are restricted, CronPlus follows standard cron behavior: a time matches when either day field matches.

## Local Environment Values

`runtime.env_file` loads a simple dotenv file relative to the manifest directory. Supported lines are `KEY=value` and `export KEY=value`; blank lines and `#` comments are ignored.

`runtime.env` can then define explicit values. `plain` injects the value directly. `secret` supports `env://NAME`, which reads `NAME` from the CronPlus daemon process environment and injects it into the script under the manifest key.

Precedence is daemon process environment, then `env_file`, then manifest `runtime.env`.

## Run Isolation and Cleanup

By default each execution gets a per-run directory under the system temp directory. CronPlus sets `HOME`, `TMPDIR`, `XDG_CACHE_HOME`, `XDG_CONFIG_HOME`, and `XDG_DATA_HOME` into that directory, then removes it after the run.

CronPlus also injects:

| Variable | Purpose |
|---|---|
| `CRONPLUS_TASK_ID` | Imported task ID |
| `CRONPLUS_RUN_ID` | Run record ID |
| `CRONPLUS_TASK_DIR` | Manifest/package directory |
| `CRONPLUS_RUN_DIR` | Per-run temp directory |
| `CRONPLUS_BROWSER_USER_DATA_DIR` | Recommended browser profile directory |
| `CRONPLUS_BROWSER_DOWNLOADS_DIR` | Recommended browser downloads directory |
| `CRONPLUS_BROWSER_CACHE_DIR` | Recommended browser cache directory |

CronPlus starts each task in its own process group, kills remaining group members after the script exits or times out, scans for detached processes that still reference the run directory, and removes the run directory. Resource-limit fields are best-effort platform limits; process-tree cleanup is the primary protection.

## Inline Delivery Profiles

Inline profiles are merged into the daemon's profile list on task import. If a profile with the same ID already exists, the inline version is skipped (existing profile wins).

This allows task packages to be fully self-contained while respecting user-edited credentials.
