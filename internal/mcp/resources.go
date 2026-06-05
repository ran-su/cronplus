package mcp

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type resourceDefinition struct {
	URI         string         `json:"uri"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	MimeType    string         `json:"mimeType,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

type resourceTemplateDefinition struct {
	URITemplate string         `json:"uriTemplate"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	MimeType    string         `json:"mimeType,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

func (s *Server) listResources() map[string]any {
	resources := []resourceDefinition{
		{
			URI:         "cronplus://status",
			Name:        "status",
			Title:       "Daemon Status",
			Description: "CronPlus daemon status, task counts, next run, failures, and attention items.",
			MimeType:    "application/json",
		},
		{
			URI:         "cronplus://tasks",
			Name:        "tasks",
			Title:       "Imported Tasks",
			Description: "All imported CronPlus tasks.",
			MimeType:    "application/json",
		},
		{
			URI:         "cronplus://deliveries",
			Name:        "deliveries",
			Title:       "Delivery Profiles",
			Description: "Delivery profiles with sensitive config values redacted by the daemon.",
			MimeType:    "application/json",
		},
		{
			URI:         "cronplus://commands",
			Name:        "commands",
			Title:       "Inbound Commands",
			Description: "Recent inbound command log records.",
			MimeType:    "application/json",
		},
		{
			URI:         "cronplus://manifest/schema",
			Name:        "manifest_schema",
			Title:       "Manifest JSON Schema",
			Description: "CronPlus task manifest JSON Schema.",
			MimeType:    "application/schema+json",
		},
		{
			URI:         "cronplus://guides/task-authoring",
			Name:        "task_authoring_guide",
			Title:       "Task Authoring Guide",
			Description: "AI-facing guidance for authoring CronPlus task packages.",
			MimeType:    "text/markdown",
		},
	}
	return map[string]any{
		"resources": resources,
	}
}

func (s *Server) listResourceTemplates() map[string]any {
	templates := []resourceTemplateDefinition{
		{
			URITemplate: "cronplus://tasks/{task_id}",
			Name:        "task",
			Title:       "Task Detail",
			Description: "Details for one imported CronPlus task.",
			MimeType:    "application/json",
		},
		{
			URITemplate: "cronplus://tasks/{task_id}/runs",
			Name:        "task_runs",
			Title:       "Task Runs",
			Description: "Run history for one imported CronPlus task.",
			MimeType:    "application/json",
		},
		{
			URITemplate: "cronplus://tasks/{task_id}/runs/{run_id}",
			Name:        "task_run",
			Title:       "Run Detail",
			Description: "One completed CronPlus run record with diagnosis.",
			MimeType:    "application/json",
		},
	}
	return map[string]any{
		"resourceTemplates": templates,
	}
}

func (s *Server) readResource(params json.RawMessage) (any, *rpcError) {
	var args struct {
		URI string `json:"uri"`
	}
	if err := decodeParams(params, &args); err != nil {
		return nil, err
	}
	uri := strings.TrimSpace(args.URI)
	if uri == "" {
		return nil, invalidParams("uri is required")
	}

	switch uri {
	case "cronplus://status":
		return s.daemonResource(uri, "application/json", "/api/status")
	case "cronplus://tasks":
		return s.daemonResource(uri, "application/json", "/api/tasks")
	case "cronplus://deliveries":
		return s.daemonResource(uri, "application/json", "/api/deliveries")
	case "cronplus://commands":
		return s.daemonResource(uri, "application/json", "/api/commands")
	case "cronplus://manifest/schema":
		text, err := readManifestSchema()
		if err != nil {
			return nil, &rpcError{Code: -32603, Message: "Could not read manifest schema: " + err.Error()}
		}
		return resourceText(uri, "application/schema+json", text), nil
	case "cronplus://guides/task-authoring":
		text, err := readTaskAuthoringGuide()
		if err != nil {
			return resourceText(uri, "text/markdown", fallbackTaskAuthoringGuide()), nil
		}
		return resourceText(uri, "text/markdown", text), nil
	}

	parts, ok := cronplusURIParts(uri)
	if !ok || len(parts) == 0 || parts[0] != "tasks" {
		return nil, &rpcError{Code: -32602, Message: "Unknown resource URI: " + uri}
	}
	if len(parts) == 2 {
		return s.daemonResource(uri, "application/json", "/api/tasks/"+pathID(parts[1]))
	}
	if len(parts) == 3 && parts[2] == "runs" {
		return s.daemonResource(uri, "application/json", "/api/tasks/"+pathID(parts[1])+"/runs")
	}
	if len(parts) == 4 && parts[2] == "runs" {
		return s.daemonResource(uri, "application/json", "/api/tasks/"+pathID(parts[1])+"/runs/"+pathID(parts[3]))
	}
	return nil, &rpcError{Code: -32602, Message: "Unknown resource URI: " + uri}
}

func (s *Server) daemonResource(uri, mimeType, path string) (any, *rpcError) {
	client, err := s.daemon()
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	result, err := client.Get(path)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return resourceText(uri, mimeType, prettyJSON(result)), nil
}

func resourceText(uri, mimeType, text string) map[string]any {
	return map[string]any{
		"contents": []map[string]string{
			{
				"uri":      uri,
				"mimeType": mimeType,
				"text":     text,
			},
		},
	}
}

func cronplusURIParts(uri string) ([]string, bool) {
	const prefix = "cronplus://"
	if !strings.HasPrefix(uri, prefix) {
		return nil, false
	}
	rest := strings.TrimPrefix(uri, prefix)
	rawParts := strings.Split(rest, "/")
	parts := make([]string, 0, len(rawParts))
	for _, raw := range rawParts {
		if raw == "" {
			continue
		}
		decoded, err := url.PathUnescape(raw)
		if err != nil {
			return nil, false
		}
		parts = append(parts, decoded)
	}
	return parts, true
}

func readManifestSchema() (string, error) {
	if text, err := readRepoTextFile("schemas/manifest.schema.json"); err == nil {
		return text, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	out, err := exec.Command(exe, "schema").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func readTaskAuthoringGuide() (string, error) {
	return readRepoTextFile("AI_TASK_AUTHORING_GUIDE.md")
}

func readRepoTextFile(rel string) (string, error) {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	seen := map[string]bool{}
	for _, start := range candidates {
		dir := start
		for i := 0; i < 8 && dir != ""; i++ {
			if seen[dir] {
				break
			}
			seen[dir] = true
			path := filepath.Join(dir, rel)
			data, err := os.ReadFile(path)
			if err == nil {
				return string(data), nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", errors.New("file not found: " + rel)
}

func fallbackTaskAuthoringGuide() string {
	return `# CronPlus Task Authoring

Create a task package directory with script.py, a .cronplus.yaml manifest, optional requirements.txt, README.md, and sample_output.json.

Use cronplus.task_package.validate for manifest-only checks. Use cronplus.task_package.check only when you are ready to prepare the environment and run the script once.

Scripts should print CRONPLUS_RESULT=<json> when structured output is expected. Supported result statuses are success, failure, warning, and skipped.
`
}
