package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ran-su/cronplus/internal/models"
	"github.com/ran-su/cronplus/internal/store"
)

func TestRestoreStatePreservesTaskIDAndHistory(t *testing.T) {
	dir := writeTaskPackage(t, "print('ok')\n", "")
	statePath := filepath.Join(t.TempDir(), "state.json")
	st := store.New(statePath)

	err := st.Save(&store.State{
		Tasks: []store.PersistedTask{
			{ID: "task-old", PackageDir: dir, Enabled: true},
		},
		RunHistory: map[string][]models.RunRecord{
			"task-old": {
				{ID: "run-old", TaskID: "task-old", FinishedAt: time.Now()},
			},
		},
	})
	if err != nil {
		t.Fatalf("Save state: %v", err)
	}

	engine := NewEngine(st, nil)
	if err := engine.RestoreState(); err != nil {
		t.Fatalf("RestoreState: %v", err)
	}

	tasks := engine.Tasks()
	if len(tasks) != 1 {
		t.Fatalf("tasks len = %d, want 1", len(tasks))
	}
	if tasks[0].ID != "task-old" {
		t.Fatalf("restored task ID = %q, want task-old", tasks[0].ID)
	}
	if history := engine.RunHistory("task-old"); len(history) != 1 || history[0].ID != "run-old" {
		t.Fatalf("history = %+v, want restored run under original task ID", history)
	}
}

func TestReloadTaskPreservesIDAndHistory(t *testing.T) {
	dir := writeTaskPackage(t, "print('ok')\n", "")
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	engine.runHistory[task.ID] = []models.RunRecord{
		{ID: "run-before", TaskID: task.ID, FinishedAt: time.Now()},
	}

	updatedManifest := `manifest_version: 1
script:
  path: ./script.py
  name: Updated Task
runtime:
  environment:
    strategy: system
  timeout_seconds: 5
  max_output_kb: 64
schedule:
  expression: "*/5 * * * *"
`
	if err := os.WriteFile(filepath.Join(dir, "test.cronplus.yaml"), []byte(updatedManifest), 0644); err != nil {
		t.Fatalf("update manifest: %v", err)
	}

	reloaded, err := engine.ReloadTask(task.ID)
	if err != nil {
		t.Fatalf("ReloadTask: %v", err)
	}
	if reloaded.ID != task.ID {
		t.Fatalf("reloaded ID = %q, want %q", reloaded.ID, task.ID)
	}
	if reloaded.DisplayName != "Updated Task" {
		t.Fatalf("DisplayName = %q, want Updated Task", reloaded.DisplayName)
	}
	if got := reloaded.Manifest.Schedule.Expression; got != "*/5 * * * *" {
		t.Fatalf("schedule = %q, want */5 * * * *", got)
	}
	if history := engine.RunHistory(task.ID); len(history) != 1 || history[0].ID != "run-before" {
		t.Fatalf("history = %+v, want preserved run history", history)
	}
}

func TestImportTaskCanonicalizesPackageDirAndDeduplicates(t *testing.T) {
	dir := writeTaskPackage(t, "print('ok')\n", "")
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("canonical task dir: %v", err)
	}
	parent := filepath.Dir(dir)
	relativeDir, err := filepath.Rel(parent, dir)
	if err != nil {
		t.Fatalf("relative task dir: %v", err)
	}

	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	t.Chdir(parent)

	task, err := engine.ImportTask(relativeDir, true)
	if err != nil {
		t.Fatalf("ImportTask relative path: %v", err)
	}
	if task.PackageDir != canonicalDir {
		t.Fatalf("PackageDir = %q, want %q", task.PackageDir, canonicalDir)
	}

	updated, err := engine.ImportTask(dir, false)
	if err != nil {
		t.Fatalf("ImportTask absolute path: %v", err)
	}
	if updated.ID != task.ID {
		t.Fatalf("duplicate import ID = %q, want %q", updated.ID, task.ID)
	}
	if updated.Enabled {
		t.Fatal("duplicate import should update the existing task")
	}
	if tasks := engine.Tasks(); len(tasks) != 1 {
		t.Fatalf("tasks len = %d, want 1", len(tasks))
	}

	linkDir := filepath.Join(t.TempDir(), "task-link")
	if err := os.Symlink(dir, linkDir); err != nil {
		t.Logf("skipping symlink import assertion: %v", err)
		return
	}
	linked, err := engine.ImportTask(linkDir, true)
	if err != nil {
		t.Fatalf("ImportTask symlink path: %v", err)
	}
	if linked.ID != task.ID {
		t.Fatalf("symlink import ID = %q, want %q", linked.ID, task.ID)
	}
	if tasks := engine.Tasks(); len(tasks) != 1 {
		t.Fatalf("tasks len after symlink import = %d, want 1", len(tasks))
	}
}

func TestImportTaskSchedulesEnvironmentSetupInBackground(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte("print('ok')\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	manifest := `manifest_version: 1
script:
  path: ./script.py
  name: Broken Env
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

	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask should return immediately: %v", err)
	}
	if task.EnvironmentSetup.State != "pending" {
		t.Fatalf("initial environment state = %q, want pending", task.EnvironmentSetup.State)
	}

	waitForEnvironmentState(t, engine, task.ID, "failed", 10*time.Second)

	if err := engine.StartTaskRun(task.ID, "manual"); !errors.Is(err, ErrEnvironmentSetupFailed) {
		t.Fatalf("StartTaskRun error = %v, want ErrEnvironmentSetupFailed", err)
	}
}

func TestReloadManagedVenvTaskWhileRunActiveRecordsRun(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	script := "import time\nprint('starting')\ntime.sleep(1)\nprint('done')\n"
	dir := writeTaskPackage(t, script, python)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	engine.environmentSetupFunc = func(*models.ScriptManifest, string) error { return nil }

	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	if err := engine.StartTaskRun(task.ID, "manual"); err != nil {
		t.Fatalf("StartTaskRun: %v", err)
	}
	waitForActiveRunDetail(t, engine, task.ID)

	manifest := `manifest_version: 1
script:
  path: ./script.py
  name: Test Task
runtime:
  environment:
    strategy: managed_venv
    python_base_interpreter: ` + python + `
  timeout_seconds: 5
  max_output_kb: 64
schedule:
  expression: "* * * * *"
`
	if err := os.WriteFile(filepath.Join(dir, "test.cronplus.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write managed manifest: %v", err)
	}
	if _, err := engine.ReloadTask(task.ID); err != nil {
		t.Fatalf("ReloadTask: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for engine.IsRunning(task.ID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if engine.IsRunning(task.ID) {
		t.Fatal("task still running after deadline")
	}

	history := engine.RunHistory(task.ID)
	if len(history) != 1 {
		t.Fatalf("run history len = %d, want 1", len(history))
	}
	if history[0].TaskID != task.ID {
		t.Fatalf("history task ID = %q, want %q", history[0].TaskID, task.ID)
	}
}

func TestEnvironmentSetupSerializesAndSkipsStaleQueuedReload(t *testing.T) {
	dir := writeManagedVenvTaskPackage(t)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)

	var mu sync.Mutex
	activeSetups := 0
	maxActiveSetups := 0
	setupCalls := 0
	firstSetupStarted := make(chan struct{})
	releaseFirstSetup := make(chan struct{})
	engine.environmentSetupFunc = func(*models.ScriptManifest, string) error {
		mu.Lock()
		setupCalls++
		callNumber := setupCalls
		activeSetups++
		if activeSetups > maxActiveSetups {
			maxActiveSetups = activeSetups
		}
		mu.Unlock()

		if callNumber == 1 {
			close(firstSetupStarted)
			<-releaseFirstSetup
		}

		mu.Lock()
		activeSetups--
		mu.Unlock()
		return nil
	}

	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	waitForSignal(t, firstSetupStarted, 2*time.Second, "first environment setup did not start")

	if _, err := engine.ReloadTask(task.ID); err != nil {
		t.Fatalf("first ReloadTask: %v", err)
	}
	if _, err := engine.ReloadTask(task.ID); err != nil {
		t.Fatalf("second ReloadTask: %v", err)
	}
	close(releaseFirstSetup)
	waitForEnvironmentState(t, engine, task.ID, "ready", 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if maxActiveSetups != 1 {
		t.Fatalf("max active setup calls = %d, want 1", maxActiveSetups)
	}
	if setupCalls != 2 {
		t.Fatalf("setup calls = %d, want 2 latest generations to run and stale queued reload to skip", setupCalls)
	}
}

func TestStartTaskRunRejectsAlreadyRunning(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	script := "import time\nprint('starting')\ntime.sleep(1)\nprint('done')\n"
	dir := writeTaskPackage(t, script, python)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}

	if err := engine.StartTaskRun(task.ID, "manual"); err != nil {
		t.Fatalf("StartTaskRun first call: %v", err)
	}
	if err := engine.StartTaskRun(task.ID, "manual"); !errors.Is(err, ErrTaskAlreadyRunning) {
		t.Fatalf("StartTaskRun second call error = %v, want ErrTaskAlreadyRunning", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for engine.IsRunning(task.ID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if engine.IsRunning(task.ID) {
		t.Fatal("task still running after deadline")
	}
}

func TestRunTaskSkipsWhenDependencyMissing(t *testing.T) {
	dir := writeNamedTaskPackage(t, "Dependent Task", "raise SystemExit('should not run')\n", "", `dependencies:
  tasks:
    - id: missing-task
`)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}

	record, err := engine.RunTask(task.ID, "manual")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if got := models.RunStatusFromOutcome(record.Outcome); got != "skipped" {
		t.Fatalf("status = %q, want skipped; outcome = %+v", got, record.Outcome)
	}
	if record.Outcome.ParsedResult == nil || !strings.Contains(record.Outcome.ParsedResult.Summary, "was not found") {
		t.Fatalf("summary = %+v, want missing dependency reason", record.Outcome.ParsedResult)
	}
	if history := engine.RunHistory(task.ID); len(history) != 1 || history[0].ID != record.ID {
		t.Fatalf("history = %+v, want skipped run recorded", history)
	}
	if engine.IsRunning(task.ID) {
		t.Fatal("task should not remain active after dependency skip")
	}
}

func TestRunTaskDependencySkipDoesNotStartRun(t *testing.T) {
	dir := writeNamedTaskPackage(t, "Dependent Task", "raise SystemExit('should not run')\n", "", `dependencies:
  tasks:
    - id: missing-task
`)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	events := engine.Broker.Subscribe()
	defer engine.Broker.Unsubscribe(events)

	record, err := engine.RunTask(task.ID, "manual")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if got := models.RunStatusFromOutcome(record.Outcome); got != "skipped" {
		t.Fatalf("status = %q, want skipped", got)
	}
	if engine.IsRunning(task.ID) {
		t.Fatal("dependency skip should not mark task as running")
	}

	var eventTypes []string
	for {
		select {
		case event := <-events:
			eventTypes = append(eventTypes, event.Type)
		default:
			if containsString(eventTypes, "run_started") {
				t.Fatalf("events = %v, dependency skip should not publish run_started", eventTypes)
			}
			if !containsString(eventTypes, "run_completed") {
				t.Fatalf("events = %v, dependency skip should publish run_completed", eventTypes)
			}
			return
		}
	}
}

func TestRunTaskDependencySkipDoesNotConsumeGlobalConcurrency(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	activeDir := writeNamedTaskPackage(t, "Active Task", "import time\ntime.sleep(1)\n", python, "")
	skipDir := writeNamedTaskPackage(t, "Skipped Task", "raise SystemExit('should not run')\n", "", `dependencies:
  tasks:
    - id: missing-task
`)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	engine.maxConcurrentRuns = 1
	activeTask, err := engine.ImportTask(activeDir, true)
	if err != nil {
		t.Fatalf("ImportTask active: %v", err)
	}
	skippedTask, err := engine.ImportTask(skipDir, true)
	if err != nil {
		t.Fatalf("ImportTask skipped: %v", err)
	}

	if err := engine.StartTaskRun(activeTask.ID, "manual"); err != nil {
		t.Fatalf("StartTaskRun active: %v", err)
	}
	waitForActiveRunDetail(t, engine, activeTask.ID)

	record, err := engine.RunTask(skippedTask.ID, "manual")
	if err != nil {
		t.Fatalf("RunTask skipped should not be blocked by max concurrency: %v", err)
	}
	if got := models.RunStatusFromOutcome(record.Outcome); got != "skipped" {
		t.Fatalf("status = %q, want skipped", got)
	}
}

func TestRunTaskDependencySkipSatisfiesStructuredResultContract(t *testing.T) {
	dir := writeNamedTaskPackage(t, "Dependent Task", "raise SystemExit('should not run')\n", "", `dependencies:
  tasks:
    - id: missing-task
result_contract:
  expect_structured_result: true
`)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}

	record, err := engine.RunTask(task.ID, "manual")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if !record.Outcome.Diagnostics.StructuredResultFound {
		t.Fatal("dependency skip should be treated as a structured CronPlus result")
	}
	diagnosis := DiagnoseRun(engine.Task(task.ID), record)
	if diagnosis.Status != "skipped" {
		t.Fatalf("diagnosis status = %q, want skipped; diagnosis = %+v", diagnosis.Status, diagnosis)
	}
	if !strings.Contains(diagnosis.Summary, "was not found") {
		t.Fatalf("diagnosis summary = %q, want dependency reason", diagnosis.Summary)
	}
}

func TestRunTaskSkipsWhenDependencyHasNoHistory(t *testing.T) {
	managerDir := writeNamedTaskPackage(t, "Browser Manager", "print('manager')\n", "", "")
	dependentDir := writeNamedTaskPackage(t, "Dependent Task", "raise SystemExit('should not run')\n", "", `dependencies:
  tasks:
    - slug: browser-manager
`)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	if _, err := engine.ImportTask(managerDir, true); err != nil {
		t.Fatalf("ImportTask manager: %v", err)
	}
	dependent, err := engine.ImportTask(dependentDir, true)
	if err != nil {
		t.Fatalf("ImportTask dependent: %v", err)
	}

	record, err := engine.RunTask(dependent.ID, "manual")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if got := models.RunStatusFromOutcome(record.Outcome); got != "skipped" {
		t.Fatalf("status = %q, want skipped; outcome = %+v", got, record.Outcome)
	}
	if record.Outcome.ParsedResult == nil || !strings.Contains(record.Outcome.ParsedResult.Summary, "has no completed runs") {
		t.Fatalf("summary = %+v, want no history reason", record.Outcome.ParsedResult)
	}
}

func TestRunTaskRunsWhenDependencyFreshSuccess(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	managerDir := writeNamedTaskPackage(t, "Browser Manager", "print('manager')\n", python, "")
	dependentDir := writeNamedTaskPackage(t, "Dependent Task", "from pathlib import Path\nPath('ran.txt').write_text('ran')\n", python, `dependencies:
  tasks:
    - slug: browser-manager
      max_age_seconds: 3900
`)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	manager, err := engine.ImportTask(managerDir, true)
	if err != nil {
		t.Fatalf("ImportTask manager: %v", err)
	}
	dependent, err := engine.ImportTask(dependentDir, true)
	if err != nil {
		t.Fatalf("ImportTask dependent: %v", err)
	}
	engine.runHistory[manager.ID] = []models.RunRecord{{
		ID:         "manager-run",
		TaskID:     manager.ID,
		FinishedAt: time.Now(),
		Outcome:    models.RunOutcome{ExitCode: 0},
	}}

	record, err := engine.RunTask(dependent.ID, "manual")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if got := models.RunStatusFromOutcome(record.Outcome); got != "success" {
		t.Fatalf("status = %q, want success; outcome = %+v", got, record.Outcome)
	}
	if _, err := os.Stat(filepath.Join(dependentDir, "ran.txt")); err != nil {
		t.Fatalf("dependent script did not run: %v", err)
	}
}

func TestRunTaskSkipsWhenDependencyIsStale(t *testing.T) {
	managerDir := writeNamedTaskPackage(t, "Browser Manager", "print('manager')\n", "", "")
	dependentDir := writeNamedTaskPackage(t, "Dependent Task", "raise SystemExit('should not run')\n", "", `dependencies:
  tasks:
    - slug: browser-manager
      max_age_seconds: 60
`)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	manager, err := engine.ImportTask(managerDir, true)
	if err != nil {
		t.Fatalf("ImportTask manager: %v", err)
	}
	dependent, err := engine.ImportTask(dependentDir, true)
	if err != nil {
		t.Fatalf("ImportTask dependent: %v", err)
	}
	engine.runHistory[manager.ID] = []models.RunRecord{{
		ID:         "manager-run",
		TaskID:     manager.ID,
		FinishedAt: time.Now().Add(-2 * time.Hour),
		Outcome:    models.RunOutcome{ExitCode: 0},
	}}

	record, err := engine.RunTask(dependent.ID, "manual")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if got := models.RunStatusFromOutcome(record.Outcome); got != "skipped" {
		t.Fatalf("status = %q, want skipped; outcome = %+v", got, record.Outcome)
	}
	if record.Outcome.ParsedResult == nil || !strings.Contains(record.Outcome.ParsedResult.Summary, "is stale") {
		t.Fatalf("summary = %+v, want stale dependency reason", record.Outcome.ParsedResult)
	}
}

func TestRunTaskFailsWhenDependencyUnhealthyPolicyIsFail(t *testing.T) {
	managerDir := writeNamedTaskPackage(t, "Browser Manager", "print('manager')\n", "", "")
	dependentDir := writeNamedTaskPackage(t, "Dependent Task", "raise SystemExit('should not run')\n", "", `dependencies:
  tasks:
    - slug: browser-manager
      require_status: success
      on_unhealthy: fail
`)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	manager, err := engine.ImportTask(managerDir, true)
	if err != nil {
		t.Fatalf("ImportTask manager: %v", err)
	}
	dependent, err := engine.ImportTask(dependentDir, true)
	if err != nil {
		t.Fatalf("ImportTask dependent: %v", err)
	}
	engine.runHistory[manager.ID] = []models.RunRecord{{
		ID:         "manager-run",
		TaskID:     manager.ID,
		FinishedAt: time.Now(),
		Outcome:    models.RunOutcome{ExitCode: 1},
	}}

	record, err := engine.RunTask(dependent.ID, "manual")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if got := models.RunStatusFromOutcome(record.Outcome); got != "failure" {
		t.Fatalf("status = %q, want failure; outcome = %+v", got, record.Outcome)
	}
	if record.Outcome.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", record.Outcome.ExitCode)
	}
	if record.Outcome.ParsedResult == nil || !strings.Contains(record.Outcome.ParsedResult.Summary, "latest status is failure") {
		t.Fatalf("summary = %+v, want status mismatch reason", record.Outcome.ParsedResult)
	}
}

func TestDependencyHealthReportsAllDependencies(t *testing.T) {
	managerDir := writeNamedTaskPackage(t, "Browser Manager", "print('manager')\n", "", "")
	freshDir := writeNamedTaskPackage(t, "Fresh Manager", "print('fresh')\n", "", "")
	dependentDir := writeNamedTaskPackage(t, "Dependent Task", "print('dependent')\n", "", `dependencies:
  tasks:
    - slug: browser-manager
      max_age_seconds: 60
    - slug: fresh-manager
      max_age_seconds: 3600
`)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	stale, err := engine.ImportTask(managerDir, true)
	if err != nil {
		t.Fatalf("ImportTask stale manager: %v", err)
	}
	fresh, err := engine.ImportTask(freshDir, true)
	if err != nil {
		t.Fatalf("ImportTask fresh manager: %v", err)
	}
	dependent, err := engine.ImportTask(dependentDir, true)
	if err != nil {
		t.Fatalf("ImportTask dependent: %v", err)
	}
	engine.runHistory[stale.ID] = []models.RunRecord{{
		ID:         "stale-run",
		TaskID:     stale.ID,
		FinishedAt: time.Now().Add(-2 * time.Hour),
		Outcome:    models.RunOutcome{ExitCode: 0},
	}}
	engine.runHistory[fresh.ID] = []models.RunRecord{{
		ID:         "fresh-run",
		TaskID:     fresh.ID,
		FinishedAt: time.Now(),
		Outcome:    models.RunOutcome{ExitCode: 0},
	}}

	report, err := engine.DependencyHealth(dependent.ID)
	if err != nil {
		t.Fatalf("DependencyHealth: %v", err)
	}
	if report.Status != "unhealthy" || len(report.Dependencies) != 2 {
		t.Fatalf("report = %+v, want two dependencies and unhealthy status", report)
	}
	if report.Dependencies[0].Status != "unhealthy" || !strings.Contains(report.Dependencies[0].Reason, "stale") {
		t.Fatalf("dependency 0 = %+v, want stale unhealthy", report.Dependencies[0])
	}
	if report.Dependencies[1].Status != "healthy" || report.Dependencies[1].LastRunID != "fresh-run" {
		t.Fatalf("dependency 1 = %+v, want healthy fresh run", report.Dependencies[1])
	}
}

func TestRebuildTaskEnvironmentRemovesManagedVenvAndRunsSetup(t *testing.T) {
	dir := writeManagedVenvTaskPackage(t)
	venvDir := filepath.Join(dir, ".cronplus-venv")
	if err := os.MkdirAll(filepath.Join(venvDir, "bin"), 0700); err != nil {
		t.Fatalf("mkdir venv: %v", err)
	}
	if err := os.WriteFile(filepath.Join(venvDir, "old.txt"), []byte("old"), 0600); err != nil {
		t.Fatalf("write old env file: %v", err)
	}

	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	setupCalls := 0
	engine.environmentSetupFunc = func(*models.ScriptManifest, string) error {
		setupCalls++
		if setupCalls == 1 {
			return nil
		}
		if _, err := os.Stat(filepath.Join(venvDir, "old.txt")); !os.IsNotExist(err) {
			t.Fatalf("old env file stat = %v, want removed before setup", err)
		}
		if err := os.MkdirAll(venvDir, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(venvDir, "rebuilt.txt"), []byte("ready"), 0600)
	}

	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	waitForEnvironmentState(t, engine, task.ID, "ready", 2*time.Second)
	if setupCalls != 1 {
		t.Fatalf("setupCalls after import = %d, want 1", setupCalls)
	}

	detail, err := engine.RebuildTaskEnvironment(task.ID)
	if err != nil {
		t.Fatalf("RebuildTaskEnvironment: %v", err)
	}
	if !detail.CanRebuild || detail.Setup.State != "pending" {
		t.Fatalf("detail = %+v, want rebuildable pending environment", detail)
	}
	waitForEnvironmentState(t, engine, task.ID, "ready", 2*time.Second)
	if setupCalls != 2 {
		t.Fatalf("setupCalls after rebuild = %d, want 2", setupCalls)
	}
	if _, err := os.Stat(filepath.Join(venvDir, "rebuilt.txt")); err != nil {
		t.Fatalf("rebuilt env marker missing: %v", err)
	}
}

func TestRemoveTaskTerminatesActiveRunAndSkipsHistory(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	script := "import pathlib, time\npathlib.Path('started.txt').write_text('started')\ntime.sleep(30)\n"
	dir := writeTaskPackage(t, script, python)
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}

	if err := engine.StartTaskRun(task.ID, "manual"); err != nil {
		t.Fatalf("StartTaskRun: %v", err)
	}
	waitForActiveRunDetail(t, engine, task.ID)

	if err := engine.RemoveTask(task.ID); err != nil {
		t.Fatalf("RemoveTask: %v", err)
	}
	if tasks := engine.Tasks(); len(tasks) != 0 {
		t.Fatalf("tasks len = %d, want 0", len(tasks))
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if history := engine.RunHistory(task.ID); len(history) != 0 {
			t.Fatalf("history after remove = %+v, want empty", history)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestEngineQueriesReturnCopies(t *testing.T) {
	dir := writeTaskPackage(t, "print('ok')\n", "")
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	engine.runHistory[task.ID] = []models.RunRecord{
		{ID: "run-before", TaskID: task.ID, DeliveryResults: []models.DeliveryResult{{ProfileID: "profile-before"}}},
	}
	engine.AddDeliveryProfile(models.DeliveryProfile{
		ID:                "profile-before",
		Name:              "Before",
		DriverType:        "telegram",
		Config:            map[string]string{"chat_id": "1"},
		AuthorizedChatIDs: []string{"1"},
	})
	engine.mu.Lock()
	engine.tasks[0].Manifest.Dependencies.Tasks = []models.TaskDependency{{Slug: "upstream"}}
	engine.mu.Unlock()

	tasks := engine.Tasks()
	tasks[0].DisplayName = "Mutated"
	tasks[0].Manifest.Script.Name = "Mutated"
	tasks[0].Manifest.Dependencies.Tasks[0].Slug = "mutated"
	if got := engine.Task(task.ID); got.DisplayName == "Mutated" || got.Manifest.Script.Name == "Mutated" {
		t.Fatalf("task query returned mutable internal task: %+v", got)
	}
	if got := engine.Task(task.ID); got.Manifest.Dependencies.Tasks[0].Slug == "mutated" {
		t.Fatalf("task query returned mutable dependency config: %+v", got.Manifest.Dependencies.Tasks)
	}

	history := engine.RunHistory(task.ID)
	history[0].ID = "mutated"
	history[0].DeliveryResults[0].ProfileID = "mutated"
	if got := engine.RunHistory(task.ID); got[0].ID == "mutated" || got[0].DeliveryResults[0].ProfileID == "mutated" {
		t.Fatalf("run history query returned mutable internal history: %+v", got)
	}

	profiles := engine.DeliveryProfiles()
	profiles[0].Config["chat_id"] = "mutated"
	profiles[0].AuthorizedChatIDs[0] = "mutated"
	if got := engine.DeliveryProfiles(); got[0].Config["chat_id"] == "mutated" || got[0].AuthorizedChatIDs[0] == "mutated" {
		t.Fatalf("delivery profile query returned mutable internal profile: %+v", got)
	}
}

func TestAddDeliveryProfileUsesNameSlugID(t *testing.T) {
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)

	id := engine.AddDeliveryProfile(models.DeliveryProfile{
		Name:       "My Telegram",
		DriverType: "telegram",
		Enabled:    true,
	})
	if id != "my-telegram" {
		t.Fatalf("id = %q, want my-telegram", id)
	}

	id2 := engine.AddDeliveryProfile(models.DeliveryProfile{
		Name:       "My Telegram",
		DriverType: "telegram",
		Enabled:    true,
	})
	if id2 != "my-telegram-2" {
		t.Fatalf("second id = %q, want my-telegram-2", id2)
	}
}

func TestAddDeliveryProfileMakesExplicitDuplicateIDUnique(t *testing.T) {
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)

	id := engine.AddDeliveryProfile(models.DeliveryProfile{
		ID:         "telegram",
		Name:       "Primary",
		DriverType: "telegram",
		Enabled:    true,
	})
	if id != "telegram" {
		t.Fatalf("id = %q, want telegram", id)
	}

	id2 := engine.AddDeliveryProfile(models.DeliveryProfile{
		ID:         "telegram",
		Name:       "Duplicate",
		DriverType: "telegram",
		Enabled:    true,
	})
	if id2 != "telegram-2" {
		t.Fatalf("second id = %q, want telegram-2", id2)
	}

	profiles := engine.DeliveryProfiles()
	if len(profiles) != 2 || profiles[0].ID == profiles[1].ID {
		t.Fatalf("profile IDs are not unique: %+v", profiles)
	}
}

func TestUpdateDeliveryProfilePreservesExistingSecrets(t *testing.T) {
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	id := engine.AddDeliveryProfile(models.DeliveryProfile{
		ID:                     "telegram",
		Name:                   "Old",
		DriverType:             "telegram",
		Enabled:                true,
		InboundCommandsEnabled: true,
		Config:                 map[string]string{"bot_token": "old-token", "chat_id": "old-chat"},
		AuthorizedChatIDs:      []string{"1"},
	})

	if err := engine.UpdateDeliveryProfile(models.DeliveryProfile{
		ID:                id,
		Name:              "New",
		DriverType:        "telegram",
		Enabled:           false,
		Config:            map[string]string{"chat_id": "new-chat"},
		AuthorizedChatIDs: []string{"2"},
	}); err != nil {
		t.Fatalf("UpdateDeliveryProfile: %v", err)
	}

	profiles := engine.DeliveryProfiles()
	if len(profiles) != 1 {
		t.Fatalf("profiles len = %d, want 1", len(profiles))
	}
	got := profiles[0]
	if got.Name != "New" || got.Enabled {
		t.Fatalf("profile basic fields = %+v, want updated name and disabled", got)
	}
	if got.Config["bot_token"] != "old-token" || got.Config["chat_id"] != "new-chat" {
		t.Fatalf("config = %+v, want preserved token and updated chat", got.Config)
	}
	if len(got.AuthorizedChatIDs) != 1 || got.AuthorizedChatIDs[0] != "2" {
		t.Fatalf("authorized chat IDs = %+v, want [2]", got.AuthorizedChatIDs)
	}
}

func TestAttachedSchedulerPrimesImportedTaskCurrentMinute(t *testing.T) {
	dir := writeTaskPackage(t, "print('ok')\n", "")
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	scheduler := NewScheduler(engine)
	engine.SetScheduler(scheduler)

	task, err := engine.ImportTask(dir, true)
	if err != nil {
		t.Fatalf("ImportTask: %v", err)
	}

	scheduler.mu.Lock()
	got := scheduler.lastTriggered[task.ID]
	scheduler.mu.Unlock()
	if got == "" {
		t.Fatal("scheduler did not prime imported task for the current matching minute")
	}
}

func waitForActiveRunDetail(t *testing.T, engine *Engine, taskID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		engine.mu.RLock()
		found := false
		for _, info := range engine.activeRunDetails {
			if info.TaskID == taskID {
				found = true
				break
			}
		}
		engine.mu.RUnlock()
		if found {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("active run detail was not recorded before deadline")
}

func waitForEnvironmentState(t *testing.T, engine *Engine, taskID, wantState string, timeout time.Duration) {
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

func waitForSignal(t *testing.T, ch <-chan struct{}, timeout time.Duration, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatal(message)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeTaskPackage(t *testing.T, script, python string) string {
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
  name: Test Task
runtime:
  environment:
    strategy: system
%s  timeout_seconds: 5
  max_output_kb: 64
schedule:
  expression: "* * * * *"
`, pythonLine)
	if err := os.WriteFile(filepath.Join(dir, "test.cronplus.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	return dir
}

func writeNamedTaskPackage(t *testing.T, name, script, python, extraManifest string) string {
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
  name: %q
runtime:
  environment:
    strategy: system
%s  timeout_seconds: 5
  max_output_kb: 64
schedule:
  expression: "* * * * *"
%s`, name, pythonLine, extraManifest)
	if err := os.WriteFile(filepath.Join(dir, "test.cronplus.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	return dir
}

func writeManagedVenvTaskPackage(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte("print('ok')\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	manifest := `manifest_version: 1
script:
  path: ./script.py
  name: Managed Env Task
runtime:
  environment:
    strategy: managed_venv
    python_base_interpreter: python3
  timeout_seconds: 5
  max_output_kb: 64
schedule:
  expression: "* * * * *"
`
	if err := os.WriteFile(filepath.Join(dir, "test.cronplus.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}
