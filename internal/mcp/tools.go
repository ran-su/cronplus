package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ran-su/cronplus/internal/daemonclient"
)

type toolDefinition struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (s *Server) listTools() map[string]any {
	tools := []toolDefinition{
		{
			Name:         "cronplus.status",
			Title:        "CronPlus Status",
			Description:  "Read the local CronPlus daemon status, including task counts, next run, failures, and attention items.",
			InputSchema:  emptyObjectSchema(),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:         "cronplus.health",
			Title:        "CronPlus Health",
			Description:  "Read the local CronPlus health and maintenance summary, including active runs, storage usage, environment sizes, and attention items.",
			InputSchema:  emptyObjectSchema(),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:         "cronplus.tasks.list",
			Title:        "List Tasks",
			Description:  "List imported CronPlus tasks with schedule, manifest, running, timeline, and latest-run summary fields.",
			InputSchema:  emptyObjectSchema(),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.tasks.get",
			Title:       "Get Task",
			Description: "Read details for one imported CronPlus task.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
			}, "task_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.tasks.dependency_health",
			Title:       "Task Dependency Health",
			Description: "Read all declared dependency checks for an imported task, including target resolution, latest dependency run status, age, and unhealthy reason.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
			}, "task_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.tasks.dependents",
			Title:       "Task Dependents",
			Description: "List imported tasks that declare a dependency on this task by ID or slug.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
			}, "task_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.tasks.environment",
			Title:       "Task Environment",
			Description: "Read an imported task's Python environment details, setup state, resolved paths, and environment directory size.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
			}, "task_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.tasks.environment_rebuild",
			Title:       "Rebuild Task Environment",
			Description: "Rebuild an imported task's managed Python virtual environment. Only managed_venv environments are rebuildable; custom venv_path environments are reported but not deleted.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
			}, "task_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": false},
		},
		{
			Name:        "cronplus.schedules.preview",
			Title:       "Preview Schedule",
			Description: "Preview upcoming run times for a task or cron expression. Task previews work even when the task is disabled.",
			InputSchema: objectSchema(map[string]any{
				"task_id":    stringProperty("Optional imported CronPlus task ID. If provided, expression and timezone default to the task schedule."),
				"expression": stringProperty("Optional cron expression to preview."),
				"timezone":   stringProperty("Optional IANA timezone. Defaults to the task timezone or UTC."),
				"after":      stringProperty("Optional RFC3339 timestamp to preview after. Defaults to now."),
				"count":      map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "Number of upcoming runs to return. Defaults to 10."},
			}),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.task_package.validate",
			Title:       "Validate Task Package",
			Description: "Validate a CronPlus task package manifest without installing dependencies, importing it, or running its script.",
			InputSchema: objectSchema(map[string]any{
				"path": stringProperty("Absolute path to the CronPlus task package directory."),
			}, "path"),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.task_package.check",
			Title:       "Check Task Package",
			Description: "Validate a CronPlus task package, prepare its environment, and run the script once as a diagnostic probe. This can install dependencies and execute local code, but does not create imported-task run history or satisfy dependencies.",
			InputSchema: objectSchema(map[string]any{
				"path": stringProperty("Absolute path to the CronPlus task package directory."),
			}, "path"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
		},
		{
			Name:        "cronplus.tasks.check",
			Title:       "Check Imported Task",
			Description: "Validate an imported task's current package, prepare its environment, and run the script once as a diagnostic probe. This does not create imported-task run history, trigger delivery, or satisfy dependencies.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
			}, "task_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
		},
		{
			Name:        "cronplus.tasks.delivery_preview",
			Title:       "Preview Task Delivery",
			Description: "Render the delivery message that would be sent for an imported task's latest run without sending it.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
			}, "task_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.tasks.import",
			Title:       "Import Task",
			Description: "Import a validated CronPlus task package into the local daemon. This registers the task but does not delete or edit package files.",
			InputSchema: objectSchema(map[string]any{
				"path":    stringProperty("Absolute path to the CronPlus task package directory."),
				"enabled": map[string]any{"type": "boolean", "description": "Whether the task should be enabled after import. Defaults to true."},
			}, "path"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
		},
		{
			Name:        "cronplus.tasks.reload",
			Title:       "Reload Task",
			Description: "Reload an imported task's manifest from disk while preserving its task ID and run history.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
			}, "task_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
		},
		{
			Name:        "cronplus.tasks.set_enabled",
			Title:       "Set Task Enabled",
			Description: "Enable or disable an imported CronPlus task.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
				"enabled": map[string]any{"type": "boolean", "description": "True to enable the task, false to disable it."},
			}, "task_id", "enabled"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
		},
		{
			Name:        "cronplus.tasks.remove",
			Title:       "Remove Task Import",
			Description: "Remove a task import from CronPlus without deleting task package files.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
			}, "task_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": false},
		},
		{
			Name:        "cronplus.runs.start",
			Title:       "Start Run",
			Description: "Start a manual run for an imported task and return a run ID that can be polled.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
			}, "task_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
		},
		{
			Name:        "cronplus.runs.list",
			Title:       "List Runs",
			Description: "Read run history for an imported task, newest first, with optional status, trigger, delivery status, search, and limit filters. Diagnostic package checks are not included because they do not create imported-task run history.",
			InputSchema: objectSchema(map[string]any{
				"task_id":         stringProperty("Imported CronPlus task ID."),
				"status":          stringProperty("Optional run status filter: success, failure, warning, or skipped."),
				"trigger":         stringProperty("Optional trigger filter: schedule, manual, or command."),
				"delivery_status": stringProperty("Optional aggregate delivery status filter: success, failed, skipped, or none."),
				"q":               stringProperty("Optional case-insensitive search over run ID, trigger, diagnosis, structured summary, and delivery results."),
				"limit":           map[string]any{"type": "integer", "minimum": 1, "description": "Optional maximum number of runs to return."},
			}, "task_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.runs.get",
			Title:       "Get Run",
			Description: "Read a completed run record. If the task is still running and the record is not available yet, returns a running status.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
				"run_id":  stringProperty("CronPlus run ID."),
			}, "task_id", "run_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.runs.wait",
			Title:       "Wait For Run",
			Description: "Poll for a run record until it completes or timeout_ms elapses. Defaults to 60000 ms and caps at 600000 ms.",
			InputSchema: objectSchema(map[string]any{
				"task_id":    stringProperty("Imported CronPlus task ID."),
				"run_id":     stringProperty("CronPlus run ID."),
				"timeout_ms": map[string]any{"type": "integer", "minimum": 0, "maximum": 600000, "description": "Maximum time to wait in milliseconds. Defaults to 60000."},
			}, "task_id", "run_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:         "cronplus.runs.active",
			Title:        "List Active Runs",
			Description:  "Read currently active runs with PID, elapsed time, execution paths, run directory, and live stdout/stderr tails.",
			InputSchema:  emptyObjectSchema(),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.runs.active_get",
			Title:       "Get Active Run",
			Description: "Read one currently active run by run ID with PID, elapsed time, execution paths, run directory, and live stdout/stderr tails.",
			InputSchema: objectSchema(map[string]any{
				"run_id": stringProperty("CronPlus active run ID."),
			}, "run_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.runs.cancel",
			Title:       "Cancel Run",
			Description: "Request cancellation of a currently active run by run ID. The daemon records a canceled run with cleanup diagnostics when the runner exits.",
			InputSchema: objectSchema(map[string]any{
				"run_id": stringProperty("CronPlus active run ID."),
				"reason": stringProperty("Optional cancellation reason recorded in diagnostics."),
			}, "run_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
		},
		{
			Name:         "cronplus.retention.get",
			Title:        "Get Retention Policy",
			Description:  "Read run-history retention settings: max runs per task, max run age, and max retained stdout/stderr bytes per stream.",
			InputSchema:  emptyObjectSchema(),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:        "cronplus.retention.update",
			Title:       "Update Retention Policy",
			Description: "Update run-history retention settings and immediately prune history to match the new policy.",
			InputSchema: objectSchema(map[string]any{
				"max_runs_per_task": map[string]any{"type": "integer", "minimum": 0, "description": "Maximum completed runs kept per task. Zero uses the daemon default."},
				"max_run_age_days":  map[string]any{"type": "integer", "minimum": 0, "description": "Maximum run age in days. Zero disables age pruning."},
				"max_run_output_kb": map[string]any{"type": "integer", "minimum": 0, "description": "Maximum retained stdout and stderr KB per stream. Zero disables output pruning."},
			}),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
		},
		{
			Name:         "cronplus.retention.cleanup",
			Title:        "Run Retention Cleanup",
			Description:  "Apply the current run-history retention policy immediately and return cleanup counts.",
			InputSchema:  emptyObjectSchema(),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
		},
		{
			Name:        "cronplus.deliveries.test",
			Title:       "Test Delivery",
			Description: "Send a test message through an existing delivery profile. Delivery profile secrets are not exposed through MCP.",
			InputSchema: objectSchema(map[string]any{
				"profile_id": stringProperty("CronPlus delivery profile ID."),
				"message":    stringProperty("Test message to send. Defaults to CronPlus delivery test."),
			}, "profile_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
		},
		{
			Name:         "cronplus.deliveries.list",
			Title:        "List Deliveries",
			Description:  "List delivery profiles with sensitive configuration redacted by the daemon.",
			InputSchema:  emptyObjectSchema(),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:         "cronplus.deliveries.create",
			Title:        "Create Delivery",
			Description:  "Create a Telegram delivery profile. Secrets are sent to the local daemon but are not exposed by list/read responses.",
			InputSchema:  objectSchema(deliveryProfileInputProperties(false), "name", "bot_token", "chat_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
		},
		{
			Name:         "cronplus.deliveries.update",
			Title:        "Update Delivery",
			Description:  "Update a delivery profile. Omitted non-secret fields keep their current values; omitted bot_token or chat_id keep existing secrets.",
			InputSchema:  objectSchema(deliveryProfileInputProperties(true), "profile_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
		},
		{
			Name:        "cronplus.deliveries.set_commands_enabled",
			Title:       "Set Delivery Commands Enabled",
			Description: "Enable or disable inbound Telegram commands for a delivery profile.",
			InputSchema: objectSchema(map[string]any{
				"profile_id": stringProperty("CronPlus delivery profile ID."),
				"enabled":    map[string]any{"type": "boolean", "description": "True to enable inbound commands, false to disable them."},
			}, "profile_id", "enabled"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
		},
		{
			Name:        "cronplus.deliveries.remove",
			Title:       "Remove Delivery",
			Description: "Delete a delivery profile from CronPlus.",
			InputSchema: objectSchema(map[string]any{
				"profile_id": stringProperty("CronPlus delivery profile ID."),
			}, "profile_id"),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": false},
		},
		{
			Name:         "cronplus.commands.list",
			Title:        "List Commands",
			Description:  "List recent inbound command log records.",
			InputSchema:  emptyObjectSchema(),
			OutputSchema: objectOutputSchema(),
			Annotations:  readOnlyAnnotations(),
		},
		{
			Name:         "cronplus.commands.clear",
			Title:        "Clear Commands",
			Description:  "Clear the inbound command log.",
			InputSchema:  emptyObjectSchema(),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
		},
		{
			Name:         "cronplus.system.pick_directory",
			Title:        "Pick Directory",
			Description:  "Open the daemon host's native system directory picker and return the selected absolute path when supported.",
			InputSchema:  emptyObjectSchema(),
			OutputSchema: objectOutputSchema(),
			Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
		},
	}

	return map[string]any{
		"tools": tools,
	}
}

func (s *Server) callTool(params json.RawMessage) (any, *rpcError) {
	var call callToolParams
	if err := decodeParams(params, &call); err != nil {
		return nil, err
	}
	call.Name = strings.TrimSpace(call.Name)
	if call.Name == "" {
		return nil, invalidParams("tool name is required")
	}
	if len(call.Arguments) == 0 {
		call.Arguments = json.RawMessage("{}")
	}

	switch call.Name {
	case "cronplus.status":
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/status")
		}), nil
	case "cronplus.health":
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/health")
		}), nil
	case "cronplus.tasks.list":
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/tasks")
		}), nil
	case "cronplus.tasks.get":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/tasks/" + pathID(args.TaskID))
		}), nil
	case "cronplus.tasks.dependency_health":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/tasks/" + pathID(args.TaskID) + "/dependencies/health")
		}), nil
	case "cronplus.tasks.dependents":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/tasks/" + pathID(args.TaskID) + "/dependents")
		}), nil
	case "cronplus.tasks.environment":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/tasks/" + pathID(args.TaskID) + "/environment")
		}), nil
	case "cronplus.tasks.environment_rebuild":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/tasks/"+pathID(args.TaskID)+"/environment/rebuild", nil)
		}), nil
	case "cronplus.schedules.preview":
		var args schedulePreviewArgs
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" && strings.TrimSpace(args.Expression) == "" {
			return nil, invalidParams("task_id or expression is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/schedules/preview", schedulePreviewBody(args))
		}), nil
	case "cronplus.task_package.validate":
		var args struct {
			Path string `json:"path"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.Path) == "" {
			return nil, invalidParams("path is required")
		}
		return toolSuccess(validateTaskPackage(args.Path)), nil
	case "cronplus.task_package.check":
		var args struct {
			Path string `json:"path"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.Path) == "" {
			return nil, invalidParams("path is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/tasks/check", map[string]string{"path": args.Path})
		}), nil
	case "cronplus.tasks.check":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/tasks/"+pathID(args.TaskID)+"/check", nil)
		}), nil
	case "cronplus.tasks.delivery_preview":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/tasks/" + pathID(args.TaskID) + "/delivery-preview")
		}), nil
	case "cronplus.tasks.import":
		var args struct {
			Path    string `json:"path"`
			Enabled *bool  `json:"enabled,omitempty"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.Path) == "" {
			return nil, invalidParams("path is required")
		}
		body := map[string]any{"path": args.Path}
		if args.Enabled != nil {
			body["enabled"] = *args.Enabled
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/tasks/import", body)
		}), nil
	case "cronplus.tasks.reload":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/tasks/"+pathID(args.TaskID)+"/reload", nil)
		}), nil
	case "cronplus.tasks.set_enabled":
		var args struct {
			TaskID  string `json:"task_id"`
			Enabled *bool  `json:"enabled"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		if args.Enabled == nil {
			return nil, invalidParams("enabled is required")
		}
		action := "disable"
		if *args.Enabled {
			action = "enable"
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/tasks/"+pathID(args.TaskID)+"/"+action, nil)
		}), nil
	case "cronplus.tasks.remove":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Delete("/api/tasks/" + pathID(args.TaskID))
		}), nil
	case "cronplus.runs.start":
		var args struct {
			TaskID string `json:"task_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/tasks/"+pathID(args.TaskID)+"/run", nil)
		}), nil
	case "cronplus.runs.list":
		var args runListArgs
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.TaskID) == "" {
			return nil, invalidParams("task_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get(runListPath(args))
		}), nil
	case "cronplus.runs.get":
		var args runLookupArgs
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := args.validate(); err != nil {
			return nil, err
		}
		result, daemonErr := s.getRunOrRunning(args.TaskID, args.RunID)
		if daemonErr != nil {
			return toolExecutionError(daemonErr), nil
		}
		return toolSuccess(result), nil
	case "cronplus.runs.wait":
		var args struct {
			TaskID    string `json:"task_id"`
			RunID     string `json:"run_id"`
			TimeoutMs int    `json:"timeout_ms"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		lookup := runLookupArgs{TaskID: args.TaskID, RunID: args.RunID}
		if err := lookup.validate(); err != nil {
			return nil, err
		}
		return s.waitForRun(args.TaskID, args.RunID, args.TimeoutMs), nil
	case "cronplus.runs.active":
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/runs/active")
		}), nil
	case "cronplus.runs.active_get":
		var args struct {
			RunID string `json:"run_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.RunID) == "" {
			return nil, invalidParams("run_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/runs/active/" + pathID(args.RunID))
		}), nil
	case "cronplus.runs.cancel":
		var args struct {
			RunID  string `json:"run_id"`
			Reason string `json:"reason"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.RunID) == "" {
			return nil, invalidParams("run_id is required")
		}
		body := map[string]string{}
		if strings.TrimSpace(args.Reason) != "" {
			body["reason"] = strings.TrimSpace(args.Reason)
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/runs/active/"+pathID(args.RunID)+"/cancel", body)
		}), nil
	case "cronplus.retention.get":
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/retention")
		}), nil
	case "cronplus.retention.update":
		var args retentionUpdateArgs
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Put("/api/retention", retentionUpdateBody(args))
		}), nil
	case "cronplus.retention.cleanup":
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/retention/cleanup", nil)
		}), nil
	case "cronplus.deliveries.test":
		var args struct {
			ProfileID string `json:"profile_id"`
			Message   string `json:"message"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.ProfileID) == "" {
			return nil, invalidParams("profile_id is required")
		}
		body := map[string]string{}
		if args.Message != "" {
			body["message"] = args.Message
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/deliveries/"+pathID(args.ProfileID)+"/test", body)
		}), nil
	case "cronplus.deliveries.list":
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/deliveries")
		}), nil
	case "cronplus.deliveries.create":
		var args deliveryProfileArgs
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.Name) == "" {
			return nil, invalidParams("name is required")
		}
		if strings.TrimSpace(args.BotToken) == "" {
			return nil, invalidParams("bot_token is required")
		}
		if strings.TrimSpace(args.ChatID) == "" {
			return nil, invalidParams("chat_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/deliveries", createDeliveryProfileBody(args))
		}), nil
	case "cronplus.deliveries.update":
		var args deliveryProfileArgs
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.ProfileID) == "" {
			return nil, invalidParams("profile_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			body, err := updateDeliveryProfileBody(c, args)
			if err != nil {
				return nil, err
			}
			return c.Put("/api/deliveries/"+pathID(args.ProfileID), body)
		}), nil
	case "cronplus.deliveries.set_commands_enabled":
		var args struct {
			ProfileID string `json:"profile_id"`
			Enabled   *bool  `json:"enabled"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.ProfileID) == "" {
			return nil, invalidParams("profile_id is required")
		}
		if args.Enabled == nil {
			return nil, invalidParams("enabled is required")
		}
		action := "disable"
		if *args.Enabled {
			action = "enable"
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/deliveries/"+pathID(args.ProfileID)+"/commands/"+action, nil)
		}), nil
	case "cronplus.deliveries.remove":
		var args struct {
			ProfileID string `json:"profile_id"`
		}
		if err := bindArgs(call.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.ProfileID) == "" {
			return nil, invalidParams("profile_id is required")
		}
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Delete("/api/deliveries/" + pathID(args.ProfileID))
		}), nil
	case "cronplus.commands.list":
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Get("/api/commands")
		}), nil
	case "cronplus.commands.clear":
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Delete("/api/commands")
		}), nil
	case "cronplus.system.pick_directory":
		return s.daemonTool(func(c *daemonclient.Client) (any, error) {
			return c.Post("/api/system/pick-directory", nil)
		}), nil
	default:
		return nil, unknownTool(call.Name)
	}
}

type runLookupArgs struct {
	TaskID string `json:"task_id"`
	RunID  string `json:"run_id"`
}

type runListArgs struct {
	TaskID         string `json:"task_id"`
	Status         string `json:"status"`
	Trigger        string `json:"trigger"`
	DeliveryStatus string `json:"delivery_status"`
	Query          string `json:"q"`
	Limit          int    `json:"limit"`
}

type retentionUpdateArgs struct {
	MaxRunsPerTask *int `json:"max_runs_per_task"`
	MaxRunAgeDays  *int `json:"max_run_age_days"`
	MaxRunOutputKB *int `json:"max_run_output_kb"`
}

type schedulePreviewArgs struct {
	TaskID     string `json:"task_id"`
	Expression string `json:"expression"`
	Timezone   string `json:"timezone"`
	After      string `json:"after"`
	Count      int    `json:"count"`
}

type deliveryProfileArgs struct {
	ProfileID              string   `json:"profile_id"`
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	DriverType             string   `json:"driver_type"`
	Enabled                *bool    `json:"enabled"`
	BotToken               string   `json:"bot_token"`
	ChatID                 string   `json:"chat_id"`
	InboundCommandsEnabled *bool    `json:"inbound_commands_enabled"`
	AuthorizedChatIDs      []string `json:"authorized_chat_ids"`
}

func (a runLookupArgs) validate() *rpcError {
	if strings.TrimSpace(a.TaskID) == "" {
		return invalidParams("task_id is required")
	}
	if strings.TrimSpace(a.RunID) == "" {
		return invalidParams("run_id is required")
	}
	return nil
}

func runListPath(args runListArgs) string {
	path := "/api/tasks/" + pathID(args.TaskID) + "/runs"
	values := url.Values{}
	if strings.TrimSpace(args.Status) != "" {
		values.Set("status", strings.TrimSpace(args.Status))
	}
	if strings.TrimSpace(args.Trigger) != "" {
		values.Set("trigger", strings.TrimSpace(args.Trigger))
	}
	if strings.TrimSpace(args.DeliveryStatus) != "" {
		values.Set("delivery_status", strings.TrimSpace(args.DeliveryStatus))
	}
	if strings.TrimSpace(args.Query) != "" {
		values.Set("q", strings.TrimSpace(args.Query))
	}
	if args.Limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", args.Limit))
	}
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return path
}

func retentionUpdateBody(args retentionUpdateArgs) map[string]any {
	body := map[string]any{}
	if args.MaxRunsPerTask != nil {
		body["maxRunsPerTask"] = *args.MaxRunsPerTask
	}
	if args.MaxRunAgeDays != nil {
		body["maxRunAgeDays"] = *args.MaxRunAgeDays
	}
	if args.MaxRunOutputKB != nil {
		body["maxRunOutputKB"] = *args.MaxRunOutputKB
	}
	return body
}

func schedulePreviewBody(args schedulePreviewArgs) map[string]any {
	body := map[string]any{}
	if strings.TrimSpace(args.TaskID) != "" {
		body["task_id"] = strings.TrimSpace(args.TaskID)
	}
	if strings.TrimSpace(args.Expression) != "" {
		body["expression"] = strings.TrimSpace(args.Expression)
	}
	if strings.TrimSpace(args.Timezone) != "" {
		body["timezone"] = strings.TrimSpace(args.Timezone)
	}
	if strings.TrimSpace(args.After) != "" {
		body["after"] = strings.TrimSpace(args.After)
	}
	if args.Count > 0 {
		body["count"] = args.Count
	}
	return body
}

func createDeliveryProfileBody(args deliveryProfileArgs) map[string]any {
	driverType := strings.TrimSpace(args.DriverType)
	if driverType == "" {
		driverType = "telegram"
	}
	enabled := true
	if args.Enabled != nil {
		enabled = *args.Enabled
	}
	inboundCommandsEnabled := false
	if args.InboundCommandsEnabled != nil {
		inboundCommandsEnabled = *args.InboundCommandsEnabled
	}

	body := map[string]any{
		"name":                   strings.TrimSpace(args.Name),
		"driverType":             driverType,
		"enabled":                enabled,
		"inboundCommandsEnabled": inboundCommandsEnabled,
		"config": map[string]string{
			"bot_token": strings.TrimSpace(args.BotToken),
			"chat_id":   strings.TrimSpace(args.ChatID),
		},
	}
	if id := strings.TrimSpace(args.ID); id != "" {
		body["id"] = id
	}
	if args.AuthorizedChatIDs != nil {
		body["authorizedChatIDs"] = normalizedStringSlice(args.AuthorizedChatIDs)
	}
	return body
}

func updateDeliveryProfileBody(client *daemonclient.Client, args deliveryProfileArgs) (map[string]any, error) {
	profile, err := fetchDeliveryProfile(client, args.ProfileID)
	if err != nil {
		return nil, err
	}

	name := stringField(profile, "name")
	if strings.TrimSpace(args.Name) != "" {
		name = strings.TrimSpace(args.Name)
	}
	driverType := stringField(profile, "driverType")
	if driverType == "" {
		driverType = "telegram"
	}
	if strings.TrimSpace(args.DriverType) != "" {
		driverType = strings.TrimSpace(args.DriverType)
	}
	enabled := boolField(profile, "enabled")
	if args.Enabled != nil {
		enabled = *args.Enabled
	}
	inboundCommandsEnabled := boolField(profile, "inboundCommandsEnabled")
	if args.InboundCommandsEnabled != nil {
		inboundCommandsEnabled = *args.InboundCommandsEnabled
	}
	authorizedChatIDs := stringSliceField(profile, "authorizedChatIDs")
	if args.AuthorizedChatIDs != nil {
		authorizedChatIDs = normalizedStringSlice(args.AuthorizedChatIDs)
	}

	config := map[string]string{}
	if strings.TrimSpace(args.BotToken) != "" {
		config["bot_token"] = strings.TrimSpace(args.BotToken)
	}
	if strings.TrimSpace(args.ChatID) != "" {
		config["chat_id"] = strings.TrimSpace(args.ChatID)
	}

	return map[string]any{
		"name":                   name,
		"driverType":             driverType,
		"enabled":                enabled,
		"inboundCommandsEnabled": inboundCommandsEnabled,
		"authorizedChatIDs":      authorizedChatIDs,
		"config":                 config,
	}, nil
}

func fetchDeliveryProfile(client *daemonclient.Client, profileID string) (map[string]any, error) {
	result, err := client.Get("/api/deliveries")
	if err != nil {
		return nil, err
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		return nil, &daemonclient.Error{Code: "invalid_response", Message: "delivery profile list response was not an object"}
	}
	profiles, ok := resultMap["profiles"].([]any)
	if !ok {
		return nil, &daemonclient.Error{Code: "invalid_response", Message: "delivery profile list response did not include profiles"}
	}
	profileID = strings.TrimSpace(profileID)
	for _, raw := range profiles {
		profile, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringField(profile, "id") == profileID {
			return profile, nil
		}
	}
	return nil, &daemonclient.Error{Code: "profile_not_found", Message: "delivery profile not found: " + profileID}
}

func stringField(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return value
}

func boolField(m map[string]any, key string) bool {
	value, _ := m[key].(bool)
	return value
}

func stringSliceField(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if value, ok := item.(string); ok {
			out = append(out, value)
		}
	}
	return out
}

func normalizedStringSlice(in []string) []string {
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func (s *Server) daemonTool(fn func(*daemonclient.Client) (any, error)) any {
	client, err := s.daemon()
	if err != nil {
		return toolExecutionError(err)
	}
	result, err := fn(client)
	if err != nil {
		return toolExecutionError(err)
	}
	return toolSuccess(result)
}

func (s *Server) daemon() (*daemonclient.Client, error) {
	if s.client == nil {
		return nil, &daemonclient.Error{Code: "daemon_client_missing", Message: "CronPlus MCP server was not configured with a daemon client."}
	}
	return s.client, nil
}

func (s *Server) getRunOrRunning(taskID, runID string) (any, error) {
	client, err := s.daemon()
	if err != nil {
		return nil, err
	}
	result, err := client.Get("/api/tasks/" + pathID(taskID) + "/runs/" + pathID(runID))
	if err == nil {
		return result, nil
	}
	if daemonErr, ok := err.(*daemonclient.Error); !ok || daemonErr.Code != "run_not_found" {
		return nil, err
	}
	active, activeErr := client.Get("/api/runs/active/" + pathID(runID))
	if activeErr == nil {
		if activeMap, ok := active.(map[string]any); ok {
			activeMap["status"] = "running"
			if stringField(activeMap, "taskID") == "" {
				activeMap["taskID"] = taskID
			}
			return activeMap, nil
		}
		return active, nil
	}
	task, taskErr := client.Get("/api/tasks/" + pathID(taskID))
	if taskErr != nil {
		return nil, err
	}
	if taskMap, ok := task.(map[string]any); ok {
		if running, _ := taskMap["running"].(bool); running {
			return map[string]any{
				"taskID": taskID,
				"runID":  runID,
				"status": "running",
			}, nil
		}
	}
	return nil, err
}

func (s *Server) waitForRun(taskID, runID string, timeoutMs int) any {
	if timeoutMs == 0 {
		timeoutMs = 60000
	}
	if timeoutMs < 0 {
		timeoutMs = 0
	}
	if timeoutMs > 600000 {
		timeoutMs = 600000
	}

	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for {
		result, err := s.getRunOrRunning(taskID, runID)
		if err != nil {
			return toolExecutionError(err)
		}
		if status, _ := resultStatus(result); status != "running" {
			return toolSuccess(result)
		}
		if time.Now().After(deadline) {
			return toolSuccess(map[string]any{
				"taskID":    taskID,
				"runID":     runID,
				"status":    "running",
				"timedOut":  true,
				"timeoutMs": timeoutMs,
			})
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func resultStatus(value any) (string, bool) {
	m, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	status, ok := m["status"].(string)
	return status, ok
}

func bindArgs(args json.RawMessage, v any) *rpcError {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if err := json.Unmarshal(args, v); err != nil {
		return invalidParams(err.Error())
	}
	return nil
}

func toolSuccess(data any) map[string]any {
	return map[string]any{
		"content":           []map[string]string{textBlock(prettyJSON(data))},
		"structuredContent": data,
		"isError":           false,
	}
}

func toolExecutionError(err error) map[string]any {
	payload := map[string]any{
		"error":   "tool_execution_failed",
		"message": err.Error(),
	}
	if daemonErr, ok := err.(*daemonclient.Error); ok {
		payload["error"] = daemonErr.Code
		payload["message"] = daemonErr.Message
		if daemonErr.StatusCode != 0 {
			payload["statusCode"] = daemonErr.StatusCode
		}
	}
	return map[string]any{
		"content":           []map[string]string{textBlock(fmt.Sprintf("%s: %s", payload["error"], payload["message"]))},
		"structuredContent": payload,
		"isError":           true,
	}
}

func emptyObjectSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func objectOutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
	}
}

func stringProperty(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

func deliveryProfileInputProperties(includeProfileID bool) map[string]any {
	properties := map[string]any{
		"id":                       stringProperty("Optional delivery profile ID for creation. If omitted, CronPlus derives one from the name."),
		"name":                     stringProperty("Delivery profile name."),
		"driver_type":              stringProperty("Delivery driver type. Defaults to telegram; currently only telegram is supported."),
		"enabled":                  map[string]any{"type": "boolean", "description": "Whether this delivery profile is enabled. Defaults to true on create; omitted values are preserved on update."},
		"bot_token":                stringProperty("Telegram bot token. Required on create; omitted values are preserved on update."),
		"chat_id":                  stringProperty("Telegram chat ID. Required on create; omitted values are preserved on update."),
		"inbound_commands_enabled": map[string]any{"type": "boolean", "description": "Whether inbound Telegram commands are enabled for this profile."},
		"authorized_chat_ids":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional allow-list of chat IDs authorized to issue inbound commands."},
	}
	if includeProfileID {
		properties["profile_id"] = stringProperty("CronPlus delivery profile ID to update.")
		delete(properties, "id")
	}
	return properties
}

func readOnlyAnnotations() map[string]any {
	return map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
}

func pathID(id string) string {
	return url.PathEscape(strings.TrimSpace(id))
}
