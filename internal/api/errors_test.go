package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ran-su/cronplus/internal/core"
	"github.com/ran-su/cronplus/internal/store"
)

func TestRunTaskMapsTypedErrors(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte("import time\ntime.sleep(2)\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	manifest := `manifest_version: 1
script:
  path: ./script.py
  name: Typed Errors
runtime:
  environment:
    strategy: system
    python_base_interpreter: ` + python + `
  timeout_seconds: 5
  max_output_kb: 64
schedule:
  expression: "* * * * *"
`
	if err := os.WriteFile(filepath.Join(dir, "test.cronplus.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}

	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	start := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/run", nil)
	startRec := httptest.NewRecorder()
	mux.ServeHTTP(startRec, start)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("first run status = %d, want %d; body=%s", startRec.Code, http.StatusAccepted, startRec.Body.String())
	}

	conflict := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/run", nil)
	conflictRec := httptest.NewRecorder()
	mux.ServeHTTP(conflictRec, conflict)
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("second run status = %d, want %d; body=%s", conflictRec.Code, http.StatusConflict, conflictRec.Body.String())
	}
}

func TestRunTaskRejectsPendingEnvironmentSetup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte("print('ok')\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	manifest := `manifest_version: 1
script:
  path: ./script.py
  name: Pending Env
runtime:
  environment:
    strategy: managed_venv
    python_base_interpreter: /definitely/missing/python
  timeout_seconds: 5
  max_output_kb: 64
schedule:
  expression: "* * * * *"
`
	if err := os.WriteFile(filepath.Join(dir, "test.cronplus.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}

	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/run", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	runErr := engine.StartTaskRun(task.ID, "manual")
	if !errors.Is(runErr, core.ErrEnvironmentSetupPending) {
		t.Fatalf("StartTaskRun error = %v, want ErrEnvironmentSetupPending", runErr)
	}

	waitForEnvironmentState(t, engine, task.ID, "failed", 10*time.Second)

	req = httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/run", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("failed env status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func waitForEnvironmentState(t *testing.T, engine *core.Engine, taskID, wantState string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task := engine.Task(taskID)
		if task != nil && task.EnvironmentSetup.State == wantState {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	task := engine.Task(taskID)
	state := ""
	if task != nil {
		state = task.EnvironmentSetup.State
	}
	t.Fatalf("environment state = %q, want %q before timeout", state, wantState)
}
