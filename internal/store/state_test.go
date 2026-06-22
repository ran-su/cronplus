package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

func TestLoadIgnoresLegacyJSONWhenSQLiteIsMissing(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "state.json")
	dbPath := filepath.Join(dir, "state.db")
	legacy := `{"tasks":[{"id":"legacy-task","packageDir":"/tmp/legacy"}],"settings":{"webServerPort":9987}}`
	if err := os.WriteFile(jsonPath, []byte(legacy), 0600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	st := New(dbPath)
	state, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if st.Path() != dbPath {
		t.Fatalf("store path = %q, want %q", st.Path(), dbPath)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("SQLite state should not be created by read-only load, err=%v", err)
	}
	if _, err := os.Stat(jsonPath + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("legacy JSON backup should not be created, err=%v", err)
	}
	if len(state.Tasks) != 0 || len(state.DeliveryProfiles) != 0 || len(state.RunHistory) != 0 {
		t.Fatalf("state = %+v, want empty SQLite state; legacy JSON must be ignored", state)
	}
	if state.Settings.WebServerPort != 9876 || state.Settings.WebServerBind != "127.0.0.1" {
		t.Fatalf("settings = %+v, want defaults", state.Settings)
	}
}

func TestSQLiteSaveLoadRoundTrip(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
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
	statePath := filepath.Join(t.TempDir(), "state.db")
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
	statePath := filepath.Join(t.TempDir(), "state.db")
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

func TestSQLiteSaveFailsWhenRunHistoryCannotBeSerialized(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
	st := New(statePath)
	state := &State{
		Tasks: []PersistedTask{{ID: "task-1", PackageDir: "/tmp/task-1", Enabled: true}},
		RunHistory: map[string][]models.RunRecord{
			"task-1": {
				{
					ID:         "run-1",
					TaskID:     "task-1",
					Trigger:    "manual",
					StartedAt:  time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
					FinishedAt: time.Date(2026, 6, 14, 12, 0, 1, 0, time.UTC),
					Outcome: models.RunOutcome{
						ParsedResult: &models.ParsedResult{
							Status:  "success",
							Summary: "bad payload",
							Data: map[string]any{
								"bad": make(chan int),
							},
						},
						Diagnostics: models.RunDiagnostics{},
					},
				},
			},
		},
		Settings: Settings{WebServerPort: 9876, WebServerBind: "127.0.0.1"},
	}

	err := st.Save(state)
	if err == nil {
		t.Fatal("Save() error = nil, want serialization failure")
	}
	if !strings.Contains(err.Error(), "failed to encode parsed result") {
		t.Fatalf("Save() error = %v, want parsed result encoding failure", err)
	}
}

func TestSQLiteSavePreservesExistingRunHistoryOnFailedRewrite(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
	st := New(statePath)
	goodState := &State{
		Tasks: []PersistedTask{{ID: "task-1", PackageDir: "/tmp/task-1", Enabled: true}},
		RunHistory: map[string][]models.RunRecord{
			"task-1": {
				{
					ID:         "run-good",
					TaskID:     "task-1",
					Trigger:    "manual",
					StartedAt:  time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
					FinishedAt: time.Date(2026, 6, 14, 12, 0, 1, 0, time.UTC),
					Outcome: models.RunOutcome{
						ParsedResult: &models.ParsedResult{Status: "success", Summary: "ok"},
						Diagnostics: models.RunDiagnostics{},
					},
				},
			},
		},
		Settings: Settings{WebServerPort: 9876, WebServerBind: "127.0.0.1"},
	}
	if err := st.Save(goodState); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	badState := &State{
		Tasks: goodState.Tasks,
		RunHistory: map[string][]models.RunRecord{
			"task-1": {
				{
					ID:         "run-bad",
					TaskID:     "task-1",
					Trigger:    "manual",
					StartedAt:  time.Date(2026, 6, 14, 13, 0, 0, 0, time.UTC),
					FinishedAt: time.Date(2026, 6, 14, 13, 0, 1, 0, time.UTC),
					Outcome: models.RunOutcome{
						ParsedResult: &models.ParsedResult{
							Status:  "success",
							Summary: "bad payload",
							Data: map[string]any{
								"bad": make(chan int),
							},
						},
						Diagnostics: models.RunDiagnostics{},
					},
				},
			},
		},
		Settings: goodState.Settings,
	}
	if err := st.Save(badState); err == nil {
		t.Fatal("failed Save error = nil, want serialization failure")
	}

	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load after failed save: %v", err)
	}
	runs := got.RunHistory["task-1"]
	if len(runs) != 1 || runs[0].ID != "run-good" {
		t.Fatalf("run history after failed save = %+v, want preserved prior data", got.RunHistory)
	}
}

