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

func TestActiveRunEndpointsAndCancel(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := writeAPITaskPackage(t, "import time\nprint('started', flush=True)\ntime.sleep(10)\n", python, "")
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	startRec := httptest.NewRecorder()
	startReq := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/run", nil)
	mux.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, want %d; body=%s", startRec.Code, http.StatusAccepted, startRec.Body.String())
	}
	var start struct {
		RunID string `json:"runID"`
	}
	if err := json.Unmarshal(startRec.Body.Bytes(), &start); err != nil {
		t.Fatalf("decode start: %v", err)
	}

	waitForAPIActiveRun(t, engine, start.RunID)
	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/runs/active", nil)
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body=%s", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var list struct {
		ActiveRuns []models.ActiveRunInfo `json:"activeRuns"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode active runs: %v", err)
	}
	if len(list.ActiveRuns) != 1 || list.ActiveRuns[0].RunID != start.RunID || list.ActiveRuns[0].StdoutTail == "" {
		t.Fatalf("active runs = %+v, want live run with output", list.ActiveRuns)
	}

	invalidCancelRec := httptest.NewRecorder()
	invalidCancelReq := httptest.NewRequest(http.MethodPost, "/api/runs/active/"+start.RunID+"/cancel", bytes.NewReader([]byte(`{`)))
	mux.ServeHTTP(invalidCancelRec, invalidCancelReq)
	if invalidCancelRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid cancel status = %d, want %d; body=%s", invalidCancelRec.Code, http.StatusBadRequest, invalidCancelRec.Body.String())
	}
	if !engine.IsRunning(task.ID) {
		t.Fatal("task stopped after invalid cancel request")
	}

	cancelRec := httptest.NewRecorder()
	cancelReq := httptest.NewRequest(http.MethodPost, "/api/runs/active/"+start.RunID+"/cancel", bytes.NewReader([]byte(`{"reason":"api test"}`)))
	mux.ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d, want %d; body=%s", cancelRec.Code, http.StatusAccepted, cancelRec.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	for engine.IsRunning(task.ID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if engine.IsRunning(task.ID) {
		t.Fatal("task still running after cancellation")
	}
	history := engine.RunHistory(task.ID)
	if len(history) != 1 || !history[0].Outcome.Diagnostics.Canceled {
		t.Fatalf("history = %+v, want canceled run", history)
	}
}

func TestRetentionEndpoints(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := writeAPITaskPackage(t, "print('x' * 2048)\nprint('CRONPLUS_RESULT={\"status\":\"success\",\"summary\":\"ready\"}')\n", python, "")
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	if _, err := engine.RunTask(task.ID, "manual"); err != nil {
		t.Fatalf("RunTask 1: %v", err)
	}
	if _, err := engine.RunTask(task.ID, "manual"); err != nil {
		t.Fatalf("RunTask 2: %v", err)
	}
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	body := []byte(`{"maxRunsPerTask":1,"maxRunOutputKB":1}`)
	updateRec := httptest.NewRecorder()
	updateReq := httptest.NewRequest(http.MethodPut, "/api/retention", bytes.NewReader(body))
	mux.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d; body=%s", updateRec.Code, http.StatusOK, updateRec.Body.String())
	}
	var report models.RetentionCleanupReport
	if err := json.Unmarshal(updateRec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode retention report: %v", err)
	}
	if report.RunsDeleted != 1 || report.OutputBytesPruned == 0 || report.Policy.MaxRunOutputKB != 1 {
		t.Fatalf("report = %+v, want deleted run and pruned output", report)
	}

	getRec := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/retention", nil)
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body=%s", getRec.Code, http.StatusOK, getRec.Body.String())
	}
	var policy models.RetentionPolicy
	if err := json.Unmarshal(getRec.Body.Bytes(), &policy); err != nil {
		t.Fatalf("decode retention policy: %v", err)
	}
	if policy.MaxRunsPerTask != 1 || !policy.OutputPruningEnabled {
		t.Fatalf("policy = %+v, want updated retention policy", policy)
	}

	cleanupRec := httptest.NewRecorder()
	cleanupReq := httptest.NewRequest(http.MethodPost, "/api/retention/cleanup", nil)
	mux.ServeHTTP(cleanupRec, cleanupReq)
	if cleanupRec.Code != http.StatusOK {
		t.Fatalf("cleanup status = %d, want %d; body=%s", cleanupRec.Code, http.StatusOK, cleanupRec.Body.String())
	}
}

func TestHealthIncludesBrowserSummary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte("print('CRONPLUS_RESULT={\"status\":\"failure\",\"summary\":\"browser failed\"}')\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	manifest := `manifest_version: 1
script:
  path: ./script.py
  name: Browser Health Task
runtime:
  environment:
    strategy: system
  timeout_seconds: 5
  max_output_kb: 64
  browser:
    enabled: true
    cleanup_policy: keep_on_failure
schedule:
  expression: "*/5 * * * *"
`
	if err := os.WriteFile(filepath.Join(dir, "test.cronplus.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	if _, err := engine.RunTask(task.ID, "manual"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	defer func() {
		for _, run := range engine.RunHistory(task.ID) {
			if run.Outcome.Diagnostics.RunDirectory != "" {
				_ = os.RemoveAll(run.Outcome.Diagnostics.RunDirectory)
			}
		}
	}()

	mux := http.NewServeMux()
	Routes(mux, engine, "test")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response models.HealthReport
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if response.Browser.Tasks != 1 || response.Browser.RecentFailures != 1 || response.Browser.StaleRunDirectories == 0 {
		t.Fatalf("browser health = %+v, want task, failure, and retained directory", response.Browser)
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

func TestDependencyHealthAndDependentsEndpoints(t *testing.T) {
	upstreamDir := writeNamedAPITaskPackage(t, "Upstream Task", "print('upstream')\n", "")
	dependentDir := writeNamedAPITaskPackage(t, "Dependent Task", "print('dependent')\n", `dependencies:
  tasks:
    - slug: upstream-task
      require_status: success
      max_age_seconds: 60
      on_unhealthy: fail
`)
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	upstream, err := engine.ImportTask(upstreamDir, true)
	if err != nil {
		t.Fatalf("ImportTask upstream: %v", err)
	}
	dependent, err := engine.ImportTask(dependentDir, true)
	if err != nil {
		t.Fatalf("ImportTask dependent: %v", err)
	}
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	healthRec := httptest.NewRecorder()
	healthReq := httptest.NewRequest(http.MethodGet, "/api/tasks/"+dependent.ID+"/dependencies/health", nil)
	mux.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("dependency health status = %d, want %d; body=%s", healthRec.Code, http.StatusOK, healthRec.Body.String())
	}
	var health models.DependencyHealthReport
	if err := json.Unmarshal(healthRec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode dependency health: %v", err)
	}
	if health.Status != "unhealthy" || len(health.Dependencies) != 1 {
		t.Fatalf("health = %+v, want one unhealthy dependency", health)
	}
	if health.Dependencies[0].TargetID != upstream.ID || health.Dependencies[0].OnUnhealthy != "fail" {
		t.Fatalf("dependency = %+v, want upstream target and fail policy", health.Dependencies[0])
	}

	dependentsRec := httptest.NewRecorder()
	dependentsReq := httptest.NewRequest(http.MethodGet, "/api/tasks/"+upstream.ID+"/dependents", nil)
	mux.ServeHTTP(dependentsRec, dependentsReq)
	if dependentsRec.Code != http.StatusOK {
		t.Fatalf("dependents status = %d, want %d; body=%s", dependentsRec.Code, http.StatusOK, dependentsRec.Body.String())
	}
	var dependents models.TaskDependentsReport
	if err := json.Unmarshal(dependentsRec.Body.Bytes(), &dependents); err != nil {
		t.Fatalf("decode dependents: %v", err)
	}
	if len(dependents.Dependents) != 1 || dependents.Dependents[0].TaskID != dependent.ID {
		t.Fatalf("dependents = %+v, want dependent task", dependents)
	}
}

func TestTaskEnvironmentEndpointIncludesSize(t *testing.T) {
	dir := writeManagedAPITaskPackage(t)
	venvDir := filepath.Join(dir, ".cronplus-venv")
	if err := os.MkdirAll(filepath.Join(venvDir, "cache"), 0700); err != nil {
		t.Fatalf("mkdir venv: %v", err)
	}
	if err := os.WriteFile(filepath.Join(venvDir, "cache", "data.bin"), []byte("1234567890"), 0600); err != nil {
		t.Fatalf("write venv data: %v", err)
	}
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	waitForAPIEnvironmentStateNotPending(t, engine, task.ID)
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/environment", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var detail models.TaskEnvironmentDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode environment: %v", err)
	}
	if detail.Strategy != "managed_venv" || !detail.CanRebuild {
		t.Fatalf("detail = %+v, want rebuildable managed venv", detail)
	}
	canonicalVenvDir, err := filepath.EvalSymlinks(venvDir)
	if err != nil {
		t.Fatalf("canonical venv dir: %v", err)
	}
	if detail.Usage.Path != canonicalVenvDir || detail.Usage.Bytes < 10 || !detail.Usage.Exists {
		t.Fatalf("usage = %+v, want env size for %s", detail.Usage, canonicalVenvDir)
	}
}

func TestSchedulePreviewEndpointWorksForDisabledTask(t *testing.T) {
	dir := writeNamedAPITaskPackage(t, "Preview Task", "print('ok')\n", "")
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, false)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	body := []byte(fmt.Sprintf(`{"taskID":%q,"count":2}`, task.ID))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/schedules/preview", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var preview models.SchedulePreview
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if !preview.Valid || len(preview.Runs) != 2 {
		t.Fatalf("preview = %+v, want two upcoming runs for disabled task", preview)
	}
}

func TestGetTaskRunsIncludesDiagnosisAndFilters(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := writeAPITaskPackage(t, "print('CRONPLUS_RESULT={\"status\":\"success\",\"summary\":\"ready\"}')\n", python, "")
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	if _, err := engine.RunTask(task.ID, "manual"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/runs?status=success&q=ready", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response struct {
		Runs []struct {
			ID        string            `json:"id"`
			Diagnosis core.RunDiagnosis `json:"diagnosis"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if len(response.Runs) != 1 || response.Runs[0].Diagnosis.Status != "success" {
		t.Fatalf("runs = %+v, want one successful diagnosed run", response.Runs)
	}

	emptyRec := httptest.NewRecorder()
	emptyReq := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/runs?status=failure", nil)
	mux.ServeHTTP(emptyRec, emptyReq)
	if emptyRec.Code != http.StatusOK {
		t.Fatalf("empty status = %d, want %d; body=%s", emptyRec.Code, http.StatusOK, emptyRec.Body.String())
	}
	var emptyResponse struct {
		Runs []any `json:"runs"`
	}
	if err := json.Unmarshal(emptyRec.Body.Bytes(), &emptyResponse); err != nil {
		t.Fatalf("decode empty runs: %v", err)
	}
	if len(emptyResponse.Runs) != 0 {
		t.Fatalf("failure filter returned %+v, want none", emptyResponse.Runs)
	}
}

func TestRunHasDeliveryStatusUsesAggregateStatus(t *testing.T) {
	noDelivery := models.RunRecord{}
	if !runHasDeliveryStatus(noDelivery, "none") {
		t.Fatal("no delivery results should match delivery_status=none")
	}
	if runHasDeliveryStatus(noDelivery, "success") {
		t.Fatal("no delivery results should not match delivery_status=success")
	}

	partialFailure := models.RunRecord{
		DeliveryResults: []models.DeliveryResult{
			{Status: "success"},
			{Status: "failed"},
		},
	}
	if !runHasDeliveryStatus(partialFailure, "failed") {
		t.Fatal("partial delivery failure should match delivery_status=failed")
	}
	if runHasDeliveryStatus(partialFailure, "success") {
		t.Fatal("partial delivery failure should not match delivery_status=success")
	}

	failureAlias := models.RunRecord{
		DeliveryResults: []models.DeliveryResult{{Status: "failure"}},
	}
	if !runHasDeliveryStatus(failureAlias, "failed") {
		t.Fatal("delivery status failure should be treated as failed")
	}

	sentWithSkipped := models.RunRecord{
		DeliveryResults: []models.DeliveryResult{
			{Status: "success"},
			{Status: "skipped"},
		},
	}
	if !runHasDeliveryStatus(sentWithSkipped, "success") {
		t.Fatal("successful delivery with skipped profiles should match delivery_status=success")
	}
	if runHasDeliveryStatus(sentWithSkipped, "skipped") {
		t.Fatal("successful delivery with skipped profiles should not aggregate to skipped")
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

func writeNamedAPITaskPackage(t *testing.T, name, script, extraManifest string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	manifest := fmt.Sprintf(`manifest_version: 1
script:
  path: ./script.py
  name: %s
runtime:
  environment:
    strategy: system
  timeout_seconds: 5
  max_output_kb: 64
schedule:
  expression: "*/5 * * * *"
%s`, name, extraManifest)
	if err := os.WriteFile(filepath.Join(dir, "test.cronplus.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func writeManagedAPITaskPackage(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte("print('ok')\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	manifest := `manifest_version: 1
script:
  path: ./script.py
  name: Managed API Task
runtime:
  environment:
    strategy: managed_venv
    python_base_interpreter: /definitely/missing/python
  timeout_seconds: 5
  max_output_kb: 64
schedule:
  expression: "*/5 * * * *"
`
	if err := os.WriteFile(filepath.Join(dir, "test.cronplus.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func waitForAPIEnvironmentStateNotPending(t *testing.T, engine *core.Engine, taskID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task := engine.Task(taskID)
		if task != nil && task.EnvironmentSetup.State != "pending" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	task := engine.Task(taskID)
	state := ""
	if task != nil {
		state = task.EnvironmentSetup.State
	}
	t.Fatalf("environment state remained %q before timeout", state)
}

func waitForAPIActiveRun(t *testing.T, engine *core.Engine, runID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, err := engine.ActiveRun(runID)
		if err == nil && info.StdoutTail != "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("active run %s did not report live output before timeout", runID)
}
