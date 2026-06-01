# CronPlus Result Schema

## Overview

Scripts can optionally output a structured result by printing a JSON line with a known prefix to stdout.

## Format

```
CRONPLUS_RESULT={"status":"success","message":"3 items found","details":{"count":3}}
```

## Rules

1. Normal log output can be printed freely to stdout/stderr.
2. CronPlus scans stdout for the **last** line starting with `CRONPLUS_RESULT=`.
3. The JSON after the prefix is parsed as structured output.
4. If missing, CronPlus records the run using exit code + raw logs only.
5. If the JSON is invalid, the structured result is ignored.
6. If `status` is present but unknown, CronPlus changes it to `failure` and adds an invalid-status diagnostic to the summary.
7. Fields other than `status` are task-defined and are passed through for UI/API storage and delivery templates.

## Result Schema

```json
{
  "status": "success",
  "message": "3 new items found",
  "details": {
    "count": 3
  }
}
```

### Fields

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | string | Yes | `"success"`, `"failure"`, `"warning"`, `"skipped"` (`"failed"` is accepted as a compatibility alias for `"failure"`). CronPlus uses this field for run state and `delivery.send_on` matching. |
| any other field | any JSON value | No | Task-defined data. CronPlus stores it and makes it available to delivery templates without prescribing its shape. |

## Delivery Message Flow

1. Script finishes → CronPlus parses result
2. Check `delivery.send_on` conditions against `status`
3. If conditions match, render `message_template` against the parsed JSON object
4. Send to each configured delivery profile only when the rendered message is not empty

## Message Template Data

Delivery templates are Go text templates. The parsed result JSON object is the template root:

```yaml
delivery:
  message_template: |
    {{message}}
    Count: {{details.count}}
```

Short field forms such as `{{status}}`, `{{message}}`, and `{{details.count}}` are rewritten to Go-template field syntax.
