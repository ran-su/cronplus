package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ran-su/cronplus/internal/daemonclient"
)

func TestInitializeReturnsCapabilities(t *testing.T) {
	server := NewServer(daemonclient.New("http://127.0.0.1:1", "token"), "test-version")

	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	result := responseResult(t, response)

	if result["protocolVersion"] == "" {
		t.Fatalf("protocolVersion missing in result: %+v", result)
	}
	info, ok := result["serverInfo"].(map[string]any)
	if !ok || info["name"] != "cronplus" || info["version"] != "test-version" {
		t.Fatalf("serverInfo = %+v, want cronplus test-version", result["serverInfo"])
	}
	if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatalf("tools capability missing: %+v", result["capabilities"])
	}
}

func TestNotificationProducesNoResponse(t *testing.T) {
	server := NewServer(nil, "test")
	if data, ok := server.HandleMessage([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); ok || len(data) != 0 {
		t.Fatalf("notification response = %q, %t; want none", string(data), ok)
	}
}

func TestStatusToolCallsDaemonWithBearerToken(t *testing.T) {
	client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/status" {
			t.Fatalf("path = %s, want /api/status", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		return jsonHTTPResponse(http.StatusOK, `{"version":"test","tasks":{"total":0}}`), nil
	})

	server := NewServer(client, "test")
	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":"call-1","method":"tools/call","params":{"name":"cronplus.status","arguments":{}}}`)
	result := responseResult(t, response)

	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; result=%+v", result["isError"], result)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %#v, want object", result["structuredContent"])
	}
	if structured["version"] != "test" {
		t.Fatalf("version = %v, want test", structured["version"])
	}
}

func TestValidateTaskPackageDoesNotRequireDaemon(t *testing.T) {
	dir := writeMCPTaskPackage(t)
	server := NewServer(nil, "test")

	request := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"cronplus.task_package.validate","arguments":{"path":` + quoteJSON(dir) + `}}}`
	response := handleTestMessage(t, server, request)
	result := responseResult(t, response)

	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; result=%+v", result["isError"], result)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %#v, want object", result["structuredContent"])
	}
	if structured["status"] != "success" {
		t.Fatalf("status = %v, want success; structured=%+v", structured["status"], structured)
	}
	if !strings.Contains(structured["summary"].(string), "No environment setup") {
		t.Fatalf("summary = %q, want manifest-only validation wording", structured["summary"])
	}
}

func TestCheckImportedTaskToolCallsDaemonEndpoint(t *testing.T) {
	client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/tasks/task-1/check" {
			t.Fatalf("path = %s, want imported task check endpoint", r.URL.Path)
		}
		return jsonHTTPResponse(http.StatusOK, `{"status":"success","summary":"ready"}`), nil
	})

	server := NewServer(client, "test")
	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"cronplus.tasks.check","arguments":{"task_id":"task-1"}}}`)
	result := responseResult(t, response)

	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; result=%+v", result["isError"], result)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %#v, want object", result["structuredContent"])
	}
	if structured["status"] != "success" {
		t.Fatalf("status = %v, want success; structured=%+v", structured["status"], structured)
	}
}

func TestRunsListToolCallsDaemonEndpoint(t *testing.T) {
	client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/tasks/task-1/runs" {
			t.Fatalf("path = %s, want run history endpoint", r.URL.Path)
		}
		return jsonHTTPResponse(http.StatusOK, `{"runs":[{"id":"run-1","taskID":"task-1"}]}`), nil
	})

	server := NewServer(client, "test")
	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"cronplus.runs.list","arguments":{"task_id":"task-1"}}}`)
	result := responseResult(t, response)

	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; result=%+v", result["isError"], result)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %#v, want object", result["structuredContent"])
	}
	runs, ok := structured["runs"].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs = %#v, want one run", structured["runs"])
	}
}

func TestTaskDeliveryPreviewToolCallsDaemonEndpoint(t *testing.T) {
	client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/tasks/task-1/delivery-preview" {
			t.Fatalf("path = %s, want delivery preview endpoint", r.URL.Path)
		}
		return jsonHTTPResponse(http.StatusOK, `{"taskID":"task-1","runID":"run-1","message":"ready"}`), nil
	})

	server := NewServer(client, "test")
	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"cronplus.tasks.delivery_preview","arguments":{"task_id":"task-1"}}}`)
	result := responseResult(t, response)

	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; result=%+v", result["isError"], result)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["message"] != "ready" {
		t.Fatalf("message = %v, want ready", structured["message"])
	}
}

func TestDeliveryCreateToolCallsDaemonEndpoint(t *testing.T) {
	client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/deliveries" {
			t.Fatalf("path = %s, want create delivery endpoint", r.URL.Path)
		}
		body := requestBodyMap(t, r)
		if body["name"] != "Telegram" || body["driverType"] != "telegram" || body["enabled"] != true {
			t.Fatalf("body = %+v, want normalized telegram profile", body)
		}
		config := body["config"].(map[string]any)
		if config["bot_token"] != "token" || config["chat_id"] != "chat" {
			t.Fatalf("config = %+v, want token and chat", config)
		}
		return jsonHTTPResponse(http.StatusCreated, `{"id":"telegram"}`), nil
	})

	server := NewServer(client, "test")
	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"cronplus.deliveries.create","arguments":{"name":"Telegram","bot_token":"token","chat_id":"chat"}}}`)
	result := responseResult(t, response)

	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; result=%+v", result["isError"], result)
	}
}

func TestDeliveryUpdateToolPreservesOmittedProfileState(t *testing.T) {
	requestCount := 0
	client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
		requestCount++
		switch requestCount {
		case 1:
			if r.Method != http.MethodGet || r.URL.Path != "/api/deliveries" {
				t.Fatalf("request 1 = %s %s, want GET /api/deliveries", r.Method, r.URL.Path)
			}
			return jsonHTTPResponse(http.StatusOK, `{"profiles":[{"id":"telegram","name":"Old","driverType":"telegram","enabled":true,"inboundCommandsEnabled":true,"authorizedChatIDs":["1"]}]}`), nil
		case 2:
			if r.Method != http.MethodPut || r.URL.Path != "/api/deliveries/telegram" {
				t.Fatalf("request 2 = %s %s, want PUT /api/deliveries/telegram", r.Method, r.URL.Path)
			}
			body := requestBodyMap(t, r)
			if body["name"] != "New" || body["enabled"] != true || body["inboundCommandsEnabled"] != true {
				t.Fatalf("body = %+v, want renamed profile with booleans preserved", body)
			}
			authorized := body["authorizedChatIDs"].([]any)
			if len(authorized) != 1 || authorized[0] != "1" {
				t.Fatalf("authorizedChatIDs = %+v, want preserved chat ID", authorized)
			}
			config := body["config"].(map[string]any)
			if _, ok := config["bot_token"]; ok {
				t.Fatalf("config = %+v, want omitted bot token preserved by daemon", config)
			}
			if config["chat_id"] != "new-chat" {
				t.Fatalf("config = %+v, want updated chat ID", config)
			}
			return jsonHTTPResponse(http.StatusOK, `{"ok":true}`), nil
		default:
			t.Fatalf("unexpected request %d: %s %s", requestCount, r.Method, r.URL.Path)
			return nil, nil
		}
	})

	server := NewServer(client, "test")
	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"cronplus.deliveries.update","arguments":{"profile_id":"telegram","name":"New","chat_id":"new-chat"}}}`)
	result := responseResult(t, response)

	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; result=%+v", result["isError"], result)
	}
	if requestCount != 2 {
		t.Fatalf("request count = %d, want 2", requestCount)
	}
}

func TestSimpleParityToolsCallDaemonEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		tool       string
		arguments  string
		wantMethod string
		wantPath   string
		response   string
	}{
		{
			name:       "list deliveries",
			tool:       "cronplus.deliveries.list",
			arguments:  `{}`,
			wantMethod: http.MethodGet,
			wantPath:   "/api/deliveries",
			response:   `{"profiles":[]}`,
		},
		{
			name:       "disable delivery commands",
			tool:       "cronplus.deliveries.set_commands_enabled",
			arguments:  `{"profile_id":"telegram","enabled":false}`,
			wantMethod: http.MethodPost,
			wantPath:   "/api/deliveries/telegram/commands/disable",
			response:   `{"ok":true}`,
		},
		{
			name:       "remove delivery",
			tool:       "cronplus.deliveries.remove",
			arguments:  `{"profile_id":"telegram"}`,
			wantMethod: http.MethodDelete,
			wantPath:   "/api/deliveries/telegram",
			response:   `{"ok":true}`,
		},
		{
			name:       "list commands",
			tool:       "cronplus.commands.list",
			arguments:  `{}`,
			wantMethod: http.MethodGet,
			wantPath:   "/api/commands",
			response:   `{"commands":[]}`,
		},
		{
			name:       "clear commands",
			tool:       "cronplus.commands.clear",
			arguments:  `{}`,
			wantMethod: http.MethodDelete,
			wantPath:   "/api/commands",
			response:   `{"ok":true}`,
		},
		{
			name:       "pick directory",
			tool:       "cronplus.system.pick_directory",
			arguments:  `{}`,
			wantMethod: http.MethodPost,
			wantPath:   "/api/system/pick-directory",
			response:   `{"path":"/tmp/task"}`,
		},
		{
			name:       "health",
			tool:       "cronplus.health",
			arguments:  `{}`,
			wantMethod: http.MethodGet,
			wantPath:   "/api/health",
			response:   `{"status":"healthy"}`,
		},
		{
			name:       "active runs",
			tool:       "cronplus.runs.active",
			arguments:  `{}`,
			wantMethod: http.MethodGet,
			wantPath:   "/api/runs/active",
			response:   `{"activeRuns":[]}`,
		},
		{
			name:       "active run get",
			tool:       "cronplus.runs.active_get",
			arguments:  `{"run_id":"run-1"}`,
			wantMethod: http.MethodGet,
			wantPath:   "/api/runs/active/run-1",
			response:   `{"runID":"run-1","status":"running"}`,
		},
		{
			name:       "cancel run",
			tool:       "cronplus.runs.cancel",
			arguments:  `{"run_id":"run-1","reason":"test"}`,
			wantMethod: http.MethodPost,
			wantPath:   "/api/runs/active/run-1/cancel",
			response:   `{"ok":true}`,
		},
		{
			name:       "retention get",
			tool:       "cronplus.retention.get",
			arguments:  `{}`,
			wantMethod: http.MethodGet,
			wantPath:   "/api/retention",
			response:   `{"maxRunsPerTask":50}`,
		},
		{
			name:       "retention cleanup",
			tool:       "cronplus.retention.cleanup",
			arguments:  `{}`,
			wantMethod: http.MethodPost,
			wantPath:   "/api/retention/cleanup",
			response:   `{"runsDeleted":0}`,
		},
		{
			name:       "dependency health",
			tool:       "cronplus.tasks.dependency_health",
			arguments:  `{"task_id":"task-1"}`,
			wantMethod: http.MethodGet,
			wantPath:   "/api/tasks/task-1/dependencies/health",
			response:   `{"status":"healthy","dependencies":[]}`,
		},
		{
			name:       "dependents",
			tool:       "cronplus.tasks.dependents",
			arguments:  `{"task_id":"task-1"}`,
			wantMethod: http.MethodGet,
			wantPath:   "/api/tasks/task-1/dependents",
			response:   `{"dependents":[]}`,
		},
		{
			name:       "environment",
			tool:       "cronplus.tasks.environment",
			arguments:  `{"task_id":"task-1"}`,
			wantMethod: http.MethodGet,
			wantPath:   "/api/tasks/task-1/environment",
			response:   `{"strategy":"system","usage":{"bytes":0}}`,
		},
		{
			name:       "environment rebuild",
			tool:       "cronplus.tasks.environment_rebuild",
			arguments:  `{"task_id":"task-1"}`,
			wantMethod: http.MethodPost,
			wantPath:   "/api/tasks/task-1/environment/rebuild",
			response:   `{"strategy":"managed_venv","setup":{"state":"pending"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
				if r.Method != tt.wantMethod {
					t.Fatalf("method = %s, want %s", r.Method, tt.wantMethod)
				}
				if r.URL.Path != tt.wantPath {
					t.Fatalf("path = %s, want %s", r.URL.Path, tt.wantPath)
				}
				return jsonHTTPResponse(http.StatusOK, tt.response), nil
			})

			server := NewServer(client, "test")
			response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"`+tt.tool+`","arguments":`+tt.arguments+`}}`)
			result := responseResult(t, response)
			if result["isError"] != false {
				t.Fatalf("isError = %v, want false; result=%+v", result["isError"], result)
			}
		})
	}
}

func TestSchedulePreviewToolPostsDaemonBody(t *testing.T) {
	client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/schedules/preview" {
			t.Fatalf("request = %s %s, want POST /api/schedules/preview", r.Method, r.URL.Path)
		}
		body := requestBodyMap(t, r)
		if body["task_id"] != "task-1" || body["count"].(float64) != 3 {
			t.Fatalf("body = %+v, want task_id and count", body)
		}
		return jsonHTTPResponse(http.StatusOK, `{"valid":true,"runs":[]}`), nil
	})

	server := NewServer(client, "test")
	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"cronplus.schedules.preview","arguments":{"task_id":"task-1","count":3}}}`)
	result := responseResult(t, response)
	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; result=%+v", result["isError"], result)
	}
}

func TestRunsListToolPassesFilters(t *testing.T) {
	client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/tasks/task-1/runs" {
			t.Fatalf("request = %s %s, want GET /api/tasks/task-1/runs", r.Method, r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("status") != "success" || query.Get("trigger") != "manual" || query.Get("delivery_status") != "failed" || query.Get("q") != "ready" || query.Get("limit") != "5" {
			t.Fatalf("query = %s, want run filters", r.URL.RawQuery)
		}
		return jsonHTTPResponse(http.StatusOK, `{"runs":[]}`), nil
	})

	server := NewServer(client, "test")
	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"cronplus.runs.list","arguments":{"task_id":"task-1","status":"success","trigger":"manual","delivery_status":"failed","q":"ready","limit":5}}}`)
	result := responseResult(t, response)
	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; result=%+v", result["isError"], result)
	}
}

func TestRetentionUpdateToolPostsDaemonBody(t *testing.T) {
	client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/retention" {
			t.Fatalf("request = %s %s, want PUT /api/retention", r.Method, r.URL.Path)
		}
		body := requestBodyMap(t, r)
		if body["maxRunsPerTask"].(float64) != 10 || body["maxRunAgeDays"].(float64) != 7 || body["maxRunOutputKB"].(float64) != 64 {
			t.Fatalf("body = %+v, want retention policy", body)
		}
		return jsonHTTPResponse(http.StatusOK, `{"runsDeleted":0,"policy":{"maxRunsPerTask":10}}`), nil
	})

	server := NewServer(client, "test")
	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"cronplus.retention.update","arguments":{"max_runs_per_task":10,"max_run_age_days":7,"max_run_output_kb":64}}}`)
	result := responseResult(t, response)
	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; result=%+v", result["isError"], result)
	}
}

func TestRunsGetReturnsActiveRunDetail(t *testing.T) {
	requestCount := 0
	client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
		requestCount++
		switch requestCount {
		case 1:
			if r.Method != http.MethodGet || r.URL.Path != "/api/tasks/task-1/runs/run-1" {
				t.Fatalf("request 1 = %s %s, want completed run lookup", r.Method, r.URL.Path)
			}
			return jsonHTTPResponse(http.StatusNotFound, `{"error":"run_not_found","message":"missing"}`), nil
		case 2:
			if r.Method != http.MethodGet || r.URL.Path != "/api/runs/active/run-1" {
				t.Fatalf("request 2 = %s %s, want active run lookup", r.Method, r.URL.Path)
			}
			return jsonHTTPResponse(http.StatusOK, `{"taskID":"task-1","runID":"run-1","stdoutTail":"ready"}`), nil
		default:
			t.Fatalf("unexpected request %d: %s %s", requestCount, r.Method, r.URL.Path)
			return nil, nil
		}
	})

	server := NewServer(client, "test")
	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"cronplus.runs.get","arguments":{"task_id":"task-1","run_id":"run-1"}}}`)
	result := responseResult(t, response)
	structured := result["structuredContent"].(map[string]any)
	if structured["status"] != "running" || structured["stdoutTail"] != "ready" {
		t.Fatalf("structured = %+v, want active running detail", structured)
	}
}

func TestReadTaskRunResource(t *testing.T) {
	client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/tasks/task-1/runs/run-1" {
			t.Fatalf("path = %s, want run resource path", r.URL.Path)
		}
		return jsonHTTPResponse(http.StatusOK, `{"id":"run-1","taskID":"task-1"}`), nil
	})

	server := NewServer(client, "test")
	response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"cronplus://tasks/task-1/runs/run-1"}}`)
	result := responseResult(t, response)
	contents, ok := result["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("contents = %#v, want one content item", result["contents"])
	}
	content, ok := contents[0].(map[string]any)
	if !ok {
		t.Fatalf("content = %#v, want object", contents[0])
	}
	if !strings.Contains(content["text"].(string), `"run-1"`) {
		t.Fatalf("content text = %q, want run id", content["text"])
	}
}

func TestReadNewManagementResources(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantPath string
		response string
	}{
		{
			name:     "health",
			uri:      "cronplus://health",
			wantPath: "/api/health",
			response: `{"status":"healthy"}`,
		},
		{
			name:     "active runs",
			uri:      "cronplus://runs/active",
			wantPath: "/api/runs/active",
			response: `{"activeRuns":[]}`,
		},
		{
			name:     "active run detail",
			uri:      "cronplus://runs/active/run-1",
			wantPath: "/api/runs/active/run-1",
			response: `{"runID":"run-1"}`,
		},
		{
			name:     "retention",
			uri:      "cronplus://retention",
			wantPath: "/api/retention",
			response: `{"maxRunsPerTask":50}`,
		},
		{
			name:     "task environment",
			uri:      "cronplus://tasks/task-1/environment",
			wantPath: "/api/tasks/task-1/environment",
			response: `{"strategy":"system"}`,
		},
		{
			name:     "task dependency health",
			uri:      "cronplus://tasks/task-1/dependencies/health",
			wantPath: "/api/tasks/task-1/dependencies/health",
			response: `{"status":"healthy"}`,
		},
		{
			name:     "task dependents",
			uri:      "cronplus://tasks/task-1/dependents",
			wantPath: "/api/tasks/task-1/dependents",
			response: `{"dependents":[]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := testDaemonClient(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != tt.wantPath {
					t.Fatalf("path = %s, want %s", r.URL.Path, tt.wantPath)
				}
				return jsonHTTPResponse(http.StatusOK, tt.response), nil
			})
			server := NewServer(client, "test")
			response := handleTestMessage(t, server, `{"jsonrpc":"2.0","id":11,"method":"resources/read","params":{"uri":"`+tt.uri+`"}}`)
			result := responseResult(t, response)
			contents, ok := result["contents"].([]any)
			if !ok || len(contents) != 1 {
				t.Fatalf("contents = %#v, want one content item", result["contents"])
			}
		})
	}
}

func handleTestMessage(t *testing.T, server *Server, request string) map[string]any {
	t.Helper()
	data, ok := server.HandleMessage([]byte(request))
	if !ok {
		t.Fatal("HandleMessage returned no response")
	}
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("decode response %s: %v", string(data), err)
	}
	if response["error"] != nil {
		t.Fatalf("unexpected rpc error: %+v", response["error"])
	}
	return response
}

func responseResult(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want object", response["result"])
	}
	return result
}

func writeMCPTaskPackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte("print('ok')\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	manifest := `manifest_version: 1
script:
  path: ./script.py
  name: MCP Task
runtime:
  environment:
    strategy: system
  timeout_seconds: 5
schedule:
  expression: "*/5 * * * *"
`
	if err := os.WriteFile(filepath.Join(dir, "task.cronplus.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func quoteJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func requestBodyMap(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode request body %q: %v", string(data), err)
	}
	return body
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func testDaemonClient(fn roundTripFunc) *daemonclient.Client {
	client := daemonclient.New("http://cronplus.test", "secret")
	client.HTTPClient = &http.Client{Transport: fn}
	return client
}

func jsonHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
