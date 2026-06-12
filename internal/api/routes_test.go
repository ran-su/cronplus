package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ran-su/cronplus/internal/core"
	"github.com/ran-su/cronplus/internal/models"
	"github.com/ran-su/cronplus/internal/store"
)

func TestGetTaskRunsUnknownTaskReturns404(t *testing.T) {
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/missing/runs", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestCreateDeliveryReturns500WhenPersistFails(t *testing.T) {
	dir := t.TempDir()
	blockedParent := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blockedParent, []byte("blocked"), 0600); err != nil {
		t.Fatalf("write blocked parent: %v", err)
	}

	engine := core.NewEngine(store.New(filepath.Join(blockedParent, "state.json")), nil)
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	body := []byte(`{"name":"Telegram","driverType":"telegram","enabled":true,"config":{"bot_token":"token","chat_id":"1"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/deliveries", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}

	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["error"] != "persist_failed" {
		t.Fatalf("error = %q, want persist_failed", response["error"])
	}
}

func TestCreateDeliveryReturnsNameSlugID(t *testing.T) {
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	body := []byte(`{"name":"My Telegram","driverType":"telegram","enabled":true,"config":{"bot_token":"token","chat_id":"1"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/deliveries", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["id"] != "my-telegram" {
		t.Fatalf("id = %q, want my-telegram", response["id"])
	}
}

func TestCreateDeliveryRejectsUnsupportedDriver(t *testing.T) {
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	body := []byte(`{"name":"Email","driverType":"email","enabled":true,"config":{"bot_token":"token","chat_id":"1"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/deliveries", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if len(engine.DeliveryProfiles()) != 0 {
		t.Fatalf("profiles = %+v, want none persisted", engine.DeliveryProfiles())
	}
}

func TestCreateDeliveryRejectsMissingTelegramConfig(t *testing.T) {
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	body := []byte(`{"name":"Telegram","driverType":"telegram","enabled":true,"config":{"bot_token":"token"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/deliveries", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if len(engine.DeliveryProfiles()) != 0 {
		t.Fatalf("profiles = %+v, want none persisted", engine.DeliveryProfiles())
	}
}

func TestUpdateDeliveryValidationUsesPreservedSecrets(t *testing.T) {
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	id := engine.AddDeliveryProfile(models.DeliveryProfile{
		Name:       "Telegram",
		DriverType: "telegram",
		Enabled:    true,
		Config:     map[string]string{"bot_token": "token", "chat_id": "1"},
	})
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	body := []byte(`{"name":"Telegram Updated","driverType":"telegram","enabled":true,"config":{}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/deliveries/"+id, bytes.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	profiles := engine.DeliveryProfiles()
	if profiles[0].Config["bot_token"] != "token" || profiles[0].Config["chat_id"] != "1" {
		t.Fatalf("profile config = %+v, want preserved token and chat", profiles[0].Config)
	}
}

func TestSetDeliveryCommandsEndpoint(t *testing.T) {
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	id := engine.AddDeliveryProfile(models.DeliveryProfile{
		ID:         "telegram",
		Name:       "Telegram",
		DriverType: "telegram",
		Enabled:    true,
	})
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/deliveries/"+id+"/commands/enable", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	profiles := engine.DeliveryProfiles()
	if len(profiles) != 1 || !profiles[0].InboundCommandsEnabled {
		t.Fatalf("profile commands enabled = %+v, want true", profiles)
	}
}

func TestCheckTaskPackageEndpoint(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := writeAPITaskPackage(t, "print('CRONPLUS_RESULT={\"status\":\"success\",\"summary\":\"ready\"}')\n", python, "")

	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	body := []byte(fmt.Sprintf(`{"path":%q}`, dir))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/check", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response struct {
		Status   string   `json:"status"`
		NextRuns []string `json:"nextRuns"`
		Run      struct {
			Status string `json:"status"`
		} `json:"run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "success" || response.Run.Status != "success" {
		t.Fatalf("response = %+v, want successful package check", response)
	}
	if len(response.NextRuns) == 0 {
		t.Fatalf("nextRuns empty in response: %+v", response)
	}
}

func TestPickDirectoryEndpointReturnsSelectedPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/system/pick-directory", handlePickDirectory(func(_ context.Context) (directoryPickerResult, error) {
		return directoryPickerResult{Path: " /tmp/task-package \n"}, nil
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/system/pick-directory", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response directoryPickerResult
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Path != "/tmp/task-package" || response.Canceled {
		t.Fatalf("response = %+v, want selected path", response)
	}
}

func TestPickDirectoryEndpointReportsCanceled(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/system/pick-directory", handlePickDirectory(func(_ context.Context) (directoryPickerResult, error) {
		return directoryPickerResult{Canceled: true}, nil
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/system/pick-directory", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response directoryPickerResult
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Canceled || response.Path != "" {
		t.Fatalf("response = %+v, want canceled", response)
	}
}

func TestPickDirectoryEndpointReportsUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/system/pick-directory", handlePickDirectory(func(_ context.Context) (directoryPickerResult, error) {
		return directoryPickerResult{}, errDirectoryPickerUnavailable
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/system/pick-directory", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
}

func TestRunTaskEndpointReturnsRunID(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := writeAPITaskPackage(t, "print('CRONPLUS_RESULT={\"status\":\"success\",\"summary\":\"ran\"}')\n", python, "")
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/run", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var response struct {
		TaskID string `json:"taskID"`
		RunID  string `json:"runID"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.TaskID != task.ID || response.RunID == "" || response.Status != "started" {
		t.Fatalf("response = %+v, want task id, run id, started", response)
	}

	deadline := time.Now().Add(3 * time.Second)
	for engine.IsRunning(task.ID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if engine.IsRunning(task.ID) {
		t.Fatal("task still running after deadline")
	}
}

func TestGetDeliveriesIncludesUsedByTasks(t *testing.T) {
	dir := writeAPITaskPackage(t, "print('ok')\n", "", "delivery:\n  profiles: [telegram]\n")
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	engine.AddDeliveryProfile(models.DeliveryProfile{
		ID:         "telegram",
		Name:       "Telegram",
		DriverType: "telegram",
		Enabled:    true,
		Config:     map[string]string{"bot_token": "token", "chat_id": "1"},
	})
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}

	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/deliveries", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response struct {
		Profiles []struct {
			ID          string              `json:"id"`
			UsedByTasks []map[string]string `json:"usedByTasks"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Profiles) != 1 || len(response.Profiles[0].UsedByTasks) != 1 {
		t.Fatalf("profiles = %+v, want one usedBy task", response.Profiles)
	}
	if response.Profiles[0].UsedByTasks[0]["id"] != task.ID {
		t.Fatalf("usedBy task = %+v, want task id %s", response.Profiles[0].UsedByTasks[0], task.ID)
	}
}

func writeAPITaskPackage(t *testing.T, script, python, extraManifest string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	pythonLine := ""
	if python != "" {
		pythonLine = fmt.Sprintf("    python_base_interpreter: %q\n", python)
	}
	manifest := fmt.Sprintf(`manifest_version: 1
script:
  path: ./script.py
  name: API Task
runtime:
  environment:
    strategy: system
%s  timeout_seconds: 5
  max_output_kb: 64
schedule:
  expression: "*/5 * * * *"
%s`, pythonLine, extraManifest)
	if err := os.WriteFile(filepath.Join(dir, "test.cronplus.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	return dir
}
