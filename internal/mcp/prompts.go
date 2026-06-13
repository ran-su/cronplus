package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

type promptDefinition struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Arguments   []promptArgument `json:"arguments,omitempty"`
}

type promptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

func (s *Server) listPrompts() map[string]any {
	prompts := []promptDefinition{
		{
			Name:        "cronplus_author_task",
			Title:       "Author CronPlus Task",
			Description: "Guide an agent through creating a CronPlus task package.",
			Arguments: []promptArgument{
				{Name: "goal", Description: "What the task should accomplish.", Required: true},
				{Name: "package_dir", Description: "Target package directory.", Required: true},
				{Name: "schedule", Description: "Optional cron schedule expression."},
				{Name: "delivery_profile", Description: "Optional delivery profile name or ID."},
			},
		},
		{
			Name:        "cronplus_debug_failed_run",
			Title:       "Debug Failed Run",
			Description: "Guide an agent through inspecting and repairing a failed CronPlus run.",
			Arguments: []promptArgument{
				{Name: "task_id", Description: "Imported task ID.", Required: true},
				{Name: "run_id", Description: "Run ID. If omitted, inspect the latest run."},
			},
		},
		{
			Name:        "cronplus_repair_manifest",
			Title:       "Repair Manifest",
			Description: "Guide an agent through fixing a CronPlus manifest without running task code.",
			Arguments: []promptArgument{
				{Name: "package_dir", Description: "Task package directory.", Required: true},
			},
		},
	}
	return map[string]any{
		"prompts": prompts,
	}
}

func (s *Server) getPrompt(params json.RawMessage) (any, *rpcError) {
	var args struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments,omitempty"`
	}
	if err := decodeParams(params, &args); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, invalidParams("prompt name is required")
	}
	if args.Arguments == nil {
		args.Arguments = map[string]string{}
	}

	switch name {
	case "cronplus_author_task":
		return promptResult(name, authorTaskPrompt(args.Arguments)), nil
	case "cronplus_debug_failed_run":
		return promptResult(name, debugRunPrompt(args.Arguments)), nil
	case "cronplus_repair_manifest":
		return promptResult(name, repairManifestPrompt(args.Arguments)), nil
	default:
		return nil, &rpcError{Code: -32602, Message: "Unknown prompt: " + name}
	}
}

func promptResult(description, text string) map[string]any {
	return map[string]any{
		"description": description,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": map[string]string{
					"type": "text",
					"text": text,
				},
			},
		},
	}
}

func authorTaskPrompt(args map[string]string) string {
	goal := strings.TrimSpace(args["goal"])
	packageDir := strings.TrimSpace(args["package_dir"])
	schedule := strings.TrimSpace(args["schedule"])
	deliveryProfile := strings.TrimSpace(args["delivery_profile"])
	if schedule == "" {
		schedule = "choose an appropriate 5-field cron expression and timezone"
	}
	if deliveryProfile == "" {
		deliveryProfile = "leave delivery profiles empty unless the user names one"
	}
	return fmt.Sprintf(`Create a CronPlus task package.

Goal: %s
Package directory: %s
Schedule: %s
Delivery: %s

Use this package shape:
- script.py
- <name>.cronplus.yaml
- requirements.txt when dependencies are needed
- README.md
- sample_output.json when structured output is useful

The manifest is the source of truth. Do not import the task until the package passes cronplus.task_package.validate. Use cronplus.task_package.check only after the user is ready to run the script once, because check prepares the environment and executes local code. Package checks are diagnostic probes; they do not create imported-task run history, trigger delivery, or satisfy dependencies.

When structured results are expected, make the script print CRONPLUS_RESULT=<json> with status, summary, and optional deliverable/data fields. Supported statuses are success, failure, warning, and skipped.`, goal, packageDir, schedule, deliveryProfile)
}

func debugRunPrompt(args map[string]string) string {
	taskID := strings.TrimSpace(args["task_id"])
	runID := strings.TrimSpace(args["run_id"])
	runInstruction := "Call cronplus.runs.list for task " + taskID + " and inspect the latest run."
	if runID != "" {
		runInstruction = "Call cronplus.runs.get for task " + taskID + " run " + runID + "."
	}
	return fmt.Sprintf(`Debug a CronPlus task run.

Task ID: %s
Run ID: %s

%s Review the diagnosis, parsed result, stderr tail, stdout tail, timeout, environment strategy, and cleanup diagnostics. Identify whether the failure is caused by manifest configuration, Python dependencies, script logic, missing secrets, output contract mismatch, timeout, or delivery failure.

Prefer cronplus.task_package.validate after manifest edits. Use cronplus.tasks.check for an imported task diagnostic probe, or cronplus.task_package.check for an arbitrary package path. Checks do not create imported-task run history or satisfy dependencies. Reload the imported task with cronplus.tasks.reload after package files change.`, taskID, runID, runInstruction)
}

func repairManifestPrompt(args map[string]string) string {
	packageDir := strings.TrimSpace(args["package_dir"])
	return fmt.Sprintf(`Repair the CronPlus manifest in this package without running task code.

Package directory: %s

Read cronplus://manifest/schema and inspect the package manifest. Fix schema, schedule, runtime, result_contract, and delivery references as needed. Validate with cronplus.task_package.validate. Do not use cronplus.task_package.check unless the user explicitly wants to prepare the environment and run the script once as a diagnostic probe. Checks do not create imported-task run history or satisfy dependencies.`, packageDir)
}
