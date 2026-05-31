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
| Task Detail | `/tasks/:id` | Manifest status, timeline, schedule, run history table, run/reload/remove-import/preview actions |
| Run Detail | `/tasks/:id/runs/:runId` | stdout, stderr, parsed result, run diagnostics, resource cleanup, delivery outcomes |
| Delivery | `/delivery` | Profile list, create/test/delete |
| Commands | `/commands` | Inbound command log |
| Settings | `/settings` | Token display, version info |

## REST API

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/status` | Dashboard data |
| `GET` | `/api/tasks` | List tasks |
| `GET` | `/api/tasks/:id` | Task detail |
| `POST` | `/api/tasks/import` | Import task `{"path":"..."}` |
| `DELETE` | `/api/tasks/:id` | Remove import without deleting package files |
| `POST` | `/api/tasks/:id/reload` | Re-read manifest from disk |
| `POST` | `/api/tasks/:id/run` | Trigger run |
| `GET` | `/api/tasks/:id/delivery-preview` | Preview latest-run delivery message |
| `POST` | `/api/tasks/:id/enable` | Enable task |
| `POST` | `/api/tasks/:id/disable` | Disable task |
| `GET` | `/api/tasks/:id/runs` | Run history |
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
- Responsive (works on mobile)
