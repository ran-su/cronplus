package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	tasks := engine.Tasks()
	tasks[0].DisplayName = "Mutated"
	tasks[0].Manifest.Script.Name = "Mutated"
	if got := engine.Task(task.ID); got.DisplayName == "Mutated" || got.Manifest.Script.Name == "Mutated" {
		t.Fatalf("task query returned mutable internal task: %+v", got)
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
