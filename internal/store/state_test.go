package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

func TestLoadImportsLegacyJSONIntoSQLite(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "state.json")
	dbPath := filepath.Join(dir, "state.db")
	legacy := `{
  "tasks": [
    {"id": "task-enabled", "packageDir": "/tmp/enabled"},
    {"id": "task-disabled", "packageDir": "/tmp/disabled", "enabled": false}
  ],
  "deliveryProfiles": [
    {"name": "Main Telegram", "config": {"bot_token": "token", "chat_id": "1"}},
    {"id": "explicit", "name": "Explicit", "driverType": "telegram", "enabled": false}
  ],
  "settings": {"webServerPort": 9987, "webServerBind": "0.0.0.0"}
}`
	if err := os.WriteFile(jsonPath, []byte(legacy), 0600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	st := New(jsonPath)
	state, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if st.Path() != dbPath {
		t.Fatalf("store path = %q, want %q", st.Path(), dbPath)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("SQLite state was not created: %v", err)
	}
	if _, err := os.Stat(jsonPath + ".bak"); err != nil {
		t.Fatalf("legacy JSON backup missing: %v", err)
	}

	if len(state.Tasks) != 2 {
		t.Fatalf("tasks len = %d, want 2", len(state.Tasks))
	}
	if !state.Tasks[0].Enabled {
		t.Fatal("missing legacy task enabled flag was not defaulted to true")
	}
	if state.Tasks[1].Enabled {
		t.Fatal("explicit disabled legacy task was not preserved")
	}

	if len(state.DeliveryProfiles) != 2 {
		t.Fatalf("profiles len = %d, want 2", len(state.DeliveryProfiles))
	}
	firstProfile := state.DeliveryProfiles[0]
	if firstProfile.ID != "main-telegram" {
		t.Fatalf("generated profile id = %q, want main-telegram", firstProfile.ID)
	}
	if firstProfile.DriverType != "telegram" {
		t.Fatalf("driver type = %q, want telegram", firstProfile.DriverType)
	}
	if !firstProfile.Enabled {
		t.Fatal("missing legacy profile enabled flag was not defaulted to true")
	}
	if firstProfile.Config["bot_token"] != "token" || firstProfile.Config["chat_id"] != "1" {
		t.Fatalf("profile config = %+v, want preserved telegram config", firstProfile.Config)
	}
	if state.DeliveryProfiles[1].Enabled {
		t.Fatal("explicit disabled legacy profile was not preserved")
	}
	if state.Settings.WebServerPort != 9987 || state.Settings.WebServerBind != "0.0.0.0" {
		t.Fatalf("settings = %+v, want preserved app config", state.Settings)
	}

	if err := os.WriteFile(jsonPath, []byte(`{"tasks":[{"id":"json-change","packageDir":"/tmp/json-change"}]}`), 0600); err != nil {
		t.Fatalf("rewrite legacy JSON: %v", err)
	}
	fromDB, err := st.Load()
	if err != nil {
		t.Fatalf("Load from SQLite: %v", err)
	}
	if len(fromDB.Tasks) != 2 || fromDB.Tasks[0].ID != "task-enabled" {
		t.Fatalf("state was reimported from JSON instead of SQLite: %+v", fromDB.Tasks)
	}
}

func TestLegacyImportKeepsCoreStateWhenHistoryIsInvalid(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	legacy := `{
  "tasks": [{"id": "task-1", "packageDir": "/tmp/task-1"}],
  "deliveryProfiles": [{"id": "telegram", "name": "Telegram", "driverType": "telegram", "config": {"bot_token": "token", "chat_id": "1"}}],
  "settings": {"webServerPort": 9988, "webServerBind": "0.0.0.0"},
  "runHistory": "not importable history",
  "activeRuns": "not importable active runs",
  "commandLog": "not importable command log"
}`
	if err := os.WriteFile(statePath, []byte(legacy), 0600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	got, err := New(statePath).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].ID != "task-1" || !got.Tasks[0].Enabled {
		t.Fatalf("tasks = %+v, want imported task with default enabled", got.Tasks)
	}
	if len(got.DeliveryProfiles) != 1 || got.DeliveryProfiles[0].Config["bot_token"] != "token" {
		t.Fatalf("profiles = %+v, want imported delivery config", got.DeliveryProfiles)
	}
	if got.Settings.WebServerPort != 9988 || got.Settings.WebServerBind != "0.0.0.0" {
		t.Fatalf("settings = %+v, want imported app config", got.Settings)
	}
	if len(got.RunHistory) != 0 || len(got.ActiveRuns) != 0 || len(got.CommandLog) != 0 {
		t.Fatalf("best-effort fields = history:%+v active:%+v commands:%+v, want skipped invalid data", got.RunHistory, got.ActiveRuns, got.CommandLog)
	}
}

func TestSQLiteSaveLoadRoundTrip(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	st := New(statePath)
	startedAt := time.Date(2026, 6, 14, 10, 30, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2 * time.Second)
	state := &State{
		Tasks: []PersistedTask{
			{ID: "task-1", PackageDir: "/tmp/task-1", Enabled: true, CreatedAt: startedAt, LastReloadedAt: finishedAt, ManifestHash: "hash", ManifestModTime: finishedAt},
		},
		DeliveryProfiles: []models.DeliveryProfile{
			{ID: "telegram", Name: "Telegram", DriverType: "telegram", Enabled: true, Config: map[string]string{"bot_token": "token", "chat_id": "1"}, InboundCommandsEnabled: true, AuthorizedChatIDs: []string{"1"}},
		},
		RunHistory: map[string][]models.RunRecord{
			"task-1": {
				{
					ID:         "run-1",
					TaskID:     "task-1",
					Trigger:    "manual",
					StartedAt:  startedAt,
					FinishedAt: finishedAt,
					Outcome: models.RunOutcome{
						ExitCode:   0,
						Stdout:     "out",
						Stderr:     "err",
						DurationMs: 2000,
						ParsedResult: &models.ParsedResult{
							Status:  "success",
							Summary: "ready",
							Data:    map[string]any{"count": float64(1)},
						},
						Diagnostics: models.RunDiagnostics{PythonExecutable: "python3", ScriptPath: "/tmp/task-1/script.py"},
					},
					DeliveryResults: []models.DeliveryResult{{ProfileID: "telegram", ProfileName: "Telegram", Status: "success"}},
				},
			},
		},
		ActiveRuns: []models.ActiveRunInfo{
			{TaskID: "task-1", RunID: "active-1", RootPID: 123, ProcessGroupID: 123, RunDirectory: "/tmp/run", StartedAt: startedAt},
		},
		CommandLog: []models.CommandRecord{
			{ID: "cmd-1", ChannelType: "telegram", ChatID: "1", CommandText: "/run task-1", MatchedCommand: "/run", ReplyText: "Started", ReceivedAt: startedAt},
		},
		Settings: Settings{WebServerPort: 9999, WebServerBind: "127.0.0.1", MaxRunsPerTask: 12, MaxRunAgeDays: 30, MaxRunOutputKB: 128},
	}

	if err := st.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(statePath), "state.db")); err != nil {
		t.Fatalf("SQLite state missing after save: %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("legacy JSON state should not be written on SQLite save, err=%v", err)
	}

	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].ID != "task-1" || !got.Tasks[0].Enabled {
		t.Fatalf("tasks = %+v", got.Tasks)
	}
	if len(got.DeliveryProfiles) != 1 || got.DeliveryProfiles[0].Config["bot_token"] != "token" || !got.DeliveryProfiles[0].InboundCommandsEnabled {
		t.Fatalf("profiles = %+v", got.DeliveryProfiles)
	}
	runs := got.RunHistory["task-1"]
	if len(runs) != 1 || runs[0].ID != "run-1" || runs[0].Outcome.ParsedResult == nil || runs[0].Outcome.ParsedResult.Summary != "ready" {
		t.Fatalf("run history = %+v", got.RunHistory)
	}
	if len(runs[0].DeliveryResults) != 1 || runs[0].DeliveryResults[0].Status != "success" {
		t.Fatalf("delivery results = %+v", runs[0].DeliveryResults)
	}
	if len(got.ActiveRuns) != 1 || got.ActiveRuns[0].RunID != "active-1" {
		t.Fatalf("active runs = %+v", got.ActiveRuns)
	}
	if len(got.CommandLog) != 1 || got.CommandLog[0].ID != "cmd-1" {
		t.Fatalf("command log = %+v", got.CommandLog)
	}
	if got.Settings.WebServerPort != 9999 || got.Settings.MaxRunsPerTask != 12 || got.Settings.MaxRunAgeDays != 30 || got.Settings.MaxRunOutputKB != 128 {
		t.Fatalf("settings = %+v", got.Settings)
	}
}

func TestSQLiteActiveRunColumnMigrationLoadsOldRows(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	st := New(statePath)
	db, err := st.openSQLiteLocked()
	if err != nil {
		t.Fatalf("openSQLiteLocked: %v", err)
	}
	if err := createSQLiteSchema(db); err != nil {
		t.Fatalf("createSQLiteSchema: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE active_runs`); err != nil {
		t.Fatalf("drop active_runs: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE active_runs (
		run_id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		root_pid INTEGER,
		process_group_id INTEGER,
		run_directory TEXT,
		started_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create old active_runs: %v", err)
	}
	startedAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO active_runs(task_id, run_id, root_pid, process_group_id, run_directory, started_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"task-1", "run-1", 123, 123, "/tmp/run", formatSQLiteTime(startedAt)); err != nil {
		t.Fatalf("insert old active run: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.ActiveRuns) != 1 {
		t.Fatalf("active runs = %+v, want migrated row", got.ActiveRuns)
	}
	run := got.ActiveRuns[0]
	if run.RunID != "run-1" || run.TaskID != "task-1" || run.TimeoutSeconds != 0 || run.CancelRequested {
		t.Fatalf("active run = %+v, want old row with zero-value new fields", run)
	}
}

func TestSQLiteLoadsStoredRunStatusWithoutParsedResult(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	st := New(statePath)
	if err := st.Save(&State{
		Tasks: []PersistedTask{{ID: "task-1", PackageDir: "/tmp/task-1", Enabled: true}},
		Settings: Settings{
			WebServerPort: 9876,
			WebServerBind: "127.0.0.1",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	db, err := st.openSQLiteLocked()
	if err != nil {
		t.Fatalf("openSQLiteLocked: %v", err)
	}
	defer db.Close()
	startedAt := time.Date(2026, 6, 14, 11, 0, 0, 0, time.UTC)
	_, err = db.Exec(`INSERT INTO run_records(id, task_id, trigger, started_at, finished_at, status, exit_code, timed_out, duration_ms, summary, stdout, stderr, parsed_result_json, diagnostics_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)`,
		"run-skipped", "task-1", "manual", formatSQLiteTime(startedAt), formatSQLiteTime(startedAt), "skipped", 0, 0, 0, "dependency did not pass", "", "", `{}`)
	if err != nil {
		t.Fatalf("insert run record: %v", err)
	}

	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	runs := got.RunHistory["task-1"]
	if len(runs) != 1 {
		t.Fatalf("run history = %+v, want one record", got.RunHistory)
	}
	if status := models.RunStatusFromOutcome(runs[0].Outcome); status != "skipped" {
		t.Fatalf("status = %q, want skipped", status)
	}
	if runs[0].Outcome.ParsedResult == nil || runs[0].Outcome.ParsedResult.Summary != "dependency did not pass" {
		t.Fatalf("parsed result = %+v, want stored summary", runs[0].Outcome.ParsedResult)
	}
}
