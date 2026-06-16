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
4. Open the web UI and confirm Dashboard, Tasks, Delivery, Commands, Health, and Settings load without API errors.

## Task Lifecycle Smoke

Use a small package with `managed_venv`, structured output, and at least one delivery profile available.

1. Import the package through the web UI folder picker or pasted path.
2. Confirm import returns immediately and environment setup appears as pending, then ready.
3. Run **Diagnostic Check** and confirm the result appears in the check panel only.
4. Confirm the diagnostic check does not create imported-task run history.
5. Run **Run Now** and confirm a real run history row appears.
6. Open the run detail page and confirm status, trigger, duration, stdout/stderr, parsed result, diagnosis, cleanup, and delivery results are readable.
7. Reload the manifest and confirm task ID and run history are preserved.
8. Disable and re-enable the task.
9. Remove the import and confirm the package files remain on disk.

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
3. Start `cronplus mcp` from an MCP-capable client or local harness.
4. Confirm MCP tools can read status, list/get tasks, start a run, list/get/wait for runs, inspect dependency health, inspect dependents, inspect environment size, rebuild a managed venv, preview schedules, and manage delivery profiles.
5. Confirm MCP package/task checks remain diagnostic probes and do not create imported-task run history.

## Browser UI Smoke

Open the web UI in a browser and inspect desktop and narrow mobile widths:

1. Dashboard cards and next-run text do not overlap.
2. Task list action buttons remain readable.
3. Task detail action buttons, environment card, dependency card, manifest/runtime cards, and package directory card do not overflow.
4. Run history filters fit and filtered results update correctly.
5. Run detail stdout/stderr blocks scroll instead of stretching the layout.
6. Delivery profile forms and edit/test/delete buttons fit.
7. Import modal path picker, diagnostic check result, and import actions fit.
8. Health storage, daemon, active-run, and attention sections are readable.
9. Settings daemon paths wrap without forcing horizontal page scroll.
