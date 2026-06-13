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
