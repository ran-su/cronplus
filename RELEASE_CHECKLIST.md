# CronPlus Release Checklist

Use this checklist before tagging a release. It is intentionally focused on the user-visible paths that should keep working after upgrades.

## Code Verification

Run from the repository root:

```bash
git diff --check
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test -race ./internal/core ./internal/api ./internal/inbound ./internal/delivery ./internal/manifest
```

Confirm release builds still work without CGO:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/cronplus-linux-amd64 .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/cronplus-linux-arm64 .
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o /tmp/cronplus-darwin-arm64 .
```

## Upgrade Smoke

1. Start CronPlus with an existing config directory that contains imported tasks and delivery profiles.
2. Confirm `state.db` is created or opened successfully.
3. If a legacy `state.json` is present, confirm tasks, delivery profiles, app settings, and recent usable run history are visible after startup.
4. On macOS, run `make install`, then confirm `command -v cronplus` points at the installed stable binary and `cronplus --help` does not execute a shell wrapper or temporary installer path.
5. Run `cronplus update --dry-run` and confirm it selects the current OS/arch release asset and stable install path.
6. Run `cronplus autostart install --no-start` from the stable binary and confirm the generated LaunchAgent points at that binary.
7. Run negative autostart checks with `--path` values for a temporary build path, a shell launcher wrapper, and a non-executable file; confirm each is rejected.
8. Open the web UI and confirm Dashboard, Tasks, Delivery, Commands, Health, and Settings load without API errors.

## Task Lifecycle Smoke

Use a small package with `managed_venv`, structured output, and at least one delivery profile available.

1. Import the package through the web UI folder picker or pasted path.
2. Confirm import returns immediately and environment setup appears as pending, then ready.
3. Run **Diagnostic Check** and confirm the result appears in the check panel only.
4. Confirm the diagnostic check does not create imported-task run history.
5. Run **Run Now** and confirm a real run history row appears.
6. Open the run detail page and confirm status, trigger, duration, stdout/stderr, parsed result, diagnosis, cleanup, and delivery results are readable.
7. Start a long-running task, confirm it appears on Health with PID/process group, elapsed time, output tail, and run directory, then cancel it and confirm a canceled run record is saved.
8. Validate or import a `templates/browser` package and confirm browser policy validation succeeds.
9. Run or inspect a browser-enabled task and confirm Health shows Browser Automation counts, retained profile/run directory usage, recent failures, and active browser runs when present.
10. Reload the manifest and confirm task ID and run history are preserved.
11. Disable and re-enable the task.
12. Remove the import and confirm the package files remain on disk.

## Dependency Smoke

1. Import an upstream task and a downstream task that depends on it.
2. Confirm the downstream task detail page shows upstream dependency health and downstream dependents are shown on the upstream task.
3. Run the downstream task before the upstream task has a successful imported-task run.
4. Confirm CronPlus records a skipped or failed run according to `on_unhealthy` without starting the script.
5. Run **Run Now** on the upstream task.
6. Refresh dependency health and confirm the downstream task can run when the latest upstream run satisfies status and freshness requirements.

## Delivery Smoke

1. Create or edit a Telegram delivery profile.
2. Send a delivery test message.
3. Preview delivery for a task with a latest run.
4. Confirm template rendering errors are visible when a template references a missing field.
5. Confirm MCP delivery list/read responses do not expose bot tokens or chat IDs.

## API And MCP Smoke

With the daemon running:

1. Call `/api/status`, `/api/health`, `/api/tasks`, and `/api/tasks/{id}/runs`.
2. Confirm run history filters work for `status`, `trigger`, `deliveryStatus`, `q`, and `limit`.
3. Call `/api/runs/active`, `/api/runs/active/{runId}`, `/api/runs/active/{runId}/cancel`, `/api/retention`, and `/api/retention/cleanup`.
4. Start `cronplus mcp` from an MCP-capable client or local harness.
5. Confirm MCP tools can read status, list/get tasks, start a run, list/get/wait for runs, inspect/cancel active runs, inspect/update/cleanup retention, inspect dependency health, inspect dependents, inspect environment size, rebuild a managed venv, preview schedules, and manage delivery profiles.
6. Confirm MCP package/task checks remain diagnostic probes and do not create imported-task run history.

## Browser UI Smoke

Open the web UI in a browser and inspect desktop and narrow mobile widths:

1. Dashboard cards and next-run text do not overlap.
2. Task list action buttons remain readable.
3. Task detail action buttons, environment card, dependency card, manifest/runtime cards, and package directory card do not overflow.
4. Run history filters fit and filtered results update correctly.
5. Run detail stdout/stderr blocks scroll instead of stretching the layout.
6. Delivery profile forms and edit/test/delete buttons fit.
7. Import modal path picker, diagnostic check result, and import actions fit.
8. Health storage, daemon, browser automation, active-run, retention, and attention sections are readable.
9. Active-run cancel controls fit and retention inputs do not overlap at desktop or narrow widths.
10. Settings daemon paths wrap without forcing horizontal page scroll.
