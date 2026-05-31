# CronPlus Result Schema

## Overview

Scripts can optionally output a structured result by printing a JSON line with a known prefix to stdout.

## Format

```
CRONPLUS_RESULT={"status":"success","summary":"3 items found","deliverable":{"kind":"text","body":"3 items found"}}
```

## Rules

1. Normal log output can be printed freely to stdout/stderr.
2. CronPlus scans stdout for the **last** line starting with `CRONPLUS_RESULT=`.
3. The JSON after the prefix is parsed as structured output.
4. If missing, CronPlus records the run using exit code + raw logs only.
5. If the JSON is invalid, the structured result is ignored.
6. If `status` is present but unknown, CronPlus changes it to `failure` and adds an invalid-status diagnostic to the summary.

## Result Schema

```json
{
  "status": "success",
  "summary": "3 new items found",
  "deliverable": {
    "kind": "text",
    "title": "Price Alert",
    "body": "3 new items found",
    "format": "plain"
  },
  "data": {
    "count": 3
  }
}
```

### Fields

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | string | No | `"success"`, `"failure"`, `"warning"`, `"skipped"` (`"failed"` is accepted as a compatibility alias for `"failure"`). If omitted, run status falls back to exit code. |
| `summary` | string | No | One-line summary for UI and delivery |
| `deliverable` | object | No | Structured payload for delivery channels |
| `deliverable.kind` | string | No | `"text"` (future: `"image"`, `"file"`) |
| `deliverable.title` | string | No | Title for the delivery message |
| `deliverable.body` | string | No | Body content for delivery |
| `deliverable.format` | string | No | `"plain"` or `"markdown"` |
| `data` | object | No | Arbitrary data for programmatic use |

## Delivery Message Flow

1. Script finishes → CronPlus parses result
2. Check `delivery.send_on` conditions against `status`
3. If conditions match, render `message_template` with result data
4. Send to each configured delivery profile

## Message Template Data

Delivery templates are Go text templates. These keys are available:

| Key | Value |
|---|---|
| `.TaskName` / `.task` | Task display name |
| `.Status` / `.status` | Canonical run status |
| `.Summary` / `.summary` | Structured result summary |
| `.Body` / `.body` | `deliverable.body` |
| `.ExitCode` / `.exitcode` | Process exit code |
| `.Duration` / `.duration` | Duration in seconds |
| `.Stdout` / `.stdout` | First 500 bytes of stdout captured for delivery |
| `.Stderr` / `.stderr` | First 500 bytes of stderr captured for delivery |
| `.Data` / `.data` | Structured result data object |

Older template forms such as `{{status}}` and `{{data.price}}` are rewritten to Go-template field syntax when the key is supported.
