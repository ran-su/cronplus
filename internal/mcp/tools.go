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
			Description: "Read run history for an imported task, newest first. Diagnostic package checks are not included because they do not create imported-task run history.",
			InputSchema: objectSchema(map[string]any{
				"task_id": stringProperty("Imported CronPlus task ID."),
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
			return c.Get("/api/tasks/" + pathID(args.TaskID) + "/runs")
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
	default:
		return nil, unknownTool(call.Name)
	}
}

type runLookupArgs struct {
	TaskID string `json:"task_id"`
	RunID  string `json:"run_id"`
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

func readOnlyAnnotations() map[string]any {
	return map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
}

func pathID(id string) string {
	return url.PathEscape(strings.TrimSpace(id))
}
