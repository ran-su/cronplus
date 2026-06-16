# CronPlus Web Interface Design

## Overview

The web interface is the primary management surface for CronPlus V2. It is a single-page application embedded in the Go binary via `go:embed` and served at `http://127.0.0.1:9876`.

## Technology

- Vanilla HTML + CSS + JavaScript (no build step)
- Dark theme, responsive layout
- SSE (Server-Sent Events) for real-time updates
- Embedded in Go binary via `embed.FS`

## Authentication

Three-step flow:
1. **localStorage** — Reuse previously-entered token
2. **Auto-auth** — `GET /api/auth/check` returns token for localhost connections
3. **Login page** — Manual token entry, stored in localStorage

## Pages

| Page | Route | Content |
|---|---|---|
| Login screen | n/a | Token input shown when auto-auth/localStorage token fails |
| Dashboard | `/` | Task counts, next run, recent failures, task cards |
| Tasks | `/tasks` | Task list with status, enable/disable, run buttons |
| Task Detail | `/tasks/:id` | Manifest status, environment details and size, upstream dependency health, downstream dependents, timeline, schedule preview, filterable run history, diagnostic-check/run/reload/remove-import/preview actions |
| Run Detail | `/tasks/:id/runs/:runId` | stdout, stderr, parsed result, run diagnostics, resource cleanup, delivery outcomes |
| Delivery | `/delivery` | Profile list, create/test/delete |
| Commands | `/commands` | Inbound command log |
| Health | `/health` | Daemon health, active runs, storage usage, environment sizes, attention items |
| Settings | `/settings` | Token display, version info |

## REST API

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/status` | Dashboard data |
| `GET` | `/api/health` | Health and maintenance data |
| `POST` | `/api/schedules/preview` | Preview upcoming runs for a task or cron expression |
| `POST` | `/api/system/pick-directory` | Open the daemon host's native directory picker when supported |
| `GET` | `/api/tasks` | List tasks |
| `GET` | `/api/tasks/:id` | Task detail |
| `POST` | `/api/tasks/check` | Run a diagnostic package check without importing it or creating imported-task run history |
| `POST` | `/api/tasks/import` | Import task `{"path":"..."}` |
| `DELETE` | `/api/tasks/:id` | Remove import without deleting package files |
| `POST` | `/api/tasks/:id/reload` | Re-read manifest from disk |
| `POST` | `/api/tasks/:id/check` | Run a diagnostic package check for an imported task without creating run history |
| `POST` | `/api/tasks/:id/run` | Trigger a real imported-task run that creates run history |
| `GET` | `/api/tasks/:id/delivery-preview` | Preview latest-run delivery message |
| `GET` | `/api/tasks/:id/dependencies/health` | Dependency health for all declared dependencies |
| `GET` | `/api/tasks/:id/dependents` | Tasks that depend on this task |
| `GET` | `/api/tasks/:id/environment` | Environment strategy, paths, setup state, and size |
| `POST` | `/api/tasks/:id/environment/rebuild` | Rebuild a managed venv |
| `POST` | `/api/tasks/:id/enable` | Enable task |
| `POST` | `/api/tasks/:id/disable` | Disable task |
| `GET` | `/api/tasks/:id/runs` | Run history with optional status, trigger, aggregate delivery status (`success`, `failed`, `skipped`, `none`), search, and limit filters |
| `GET` | `/api/tasks/:id/runs/:runId` | Run detail |
| `GET` | `/api/deliveries` | List profiles |
| `POST` | `/api/deliveries` | Create profile |
| `PUT` | `/api/deliveries/:id` | Update profile |
| `POST` | `/api/deliveries/:id/test` | Send test message |
| `DELETE` | `/api/deliveries/:id` | Delete profile |
| `GET` | `/api/commands` | Command log |
| `DELETE` | `/api/commands` | Clear log |
| `GET` | `/api/events` | SSE stream |
| `GET` | `/api/auth/check` | Localhost auto-auth |

## SSE Events

| Event | Trigger |
|---|---|
| `task_updated` | Task imported/modified/removed |
| `run_started` | Script execution begins |
| `run_completed` | Script execution finishes |
| `delivery_sent` | Delivery attempt completed |

The web UI also refreshes API state on a 30-second interval when no modal or input field is active.

## Design

- Dark theme with system UI and monospace fonts
- Color-coded status (green/amber/red)
- Live updates via SSE
- Native system directory picker for task import when supported, with paste-path fallback
- Run history filters for status, trigger, delivery state, and text search
- Task environment card always shows strategy and reports managed/custom venv size when a venv path exists
- Managed venv rebuild is available from task detail; custom venv paths are inspected but not deleted
- Health page summarizes active runs, storage usage, environment sizes, and attention items
- Responsive (works on mobile)
