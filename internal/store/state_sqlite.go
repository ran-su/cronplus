package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ran-su/cronplus/internal/models"
	_ "modernc.org/sqlite"
)

const sqliteStateUserVersion = 1

func (s *Store) loadSQLiteLocked() (*State, error) {
	if _, err := os.Stat(s.dbPath); os.IsNotExist(err) {
		return s.defaultState(), nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to inspect SQLite state: %w", err)
	}

	db, err := s.openSQLiteLocked()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if err := ensureSQLiteSchema(db); err != nil {
		return nil, err
	}
	state, err := readSQLiteState(db)
	if err != nil {
		return nil, err
	}
	normalizeState(state)
	return state, nil
}

func (s *Store) saveSQLiteLocked(state *State) error {
	if err := os.MkdirAll(filepath.Dir(s.dbPath), 0700); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}
	db, err := s.openSQLiteLocked()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := ensureSQLiteSchema(db); err != nil {
		return err
	}
	return writeSQLiteState(db, state)
}

func (s *Store) openSQLiteLocked() (*sql.DB, error) {
	db, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite state: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize SQLite state connection: %w", err)
	}
	return db, nil
}

func ensureSQLiteSchema(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("failed to read SQLite state version: %w", err)
	}
	if version > sqliteStateUserVersion {
		return fmt.Errorf("unsupported SQLite state version %d; this binary supports up to %d", version, sqliteStateUserVersion)
	}
	if version == 0 {
		if err := createSQLiteSchema(db); err != nil {
			return err
		}
	}
	return ensureSQLiteActiveRunColumns(db)
}

func ensureSQLiteActiveRunColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(active_runs)`)
	if err != nil {
		return fmt.Errorf("failed to inspect active run schema: %w", err)
	}
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			_ = rows.Close()
			return fmt.Errorf("failed to scan active run schema: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("failed to inspect active run schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("failed to close active run schema inspection: %w", err)
	}

	columns := []struct {
		name       string
		columnType string
	}{
		{"task_name", "TEXT"},
		{"task_slug", "TEXT"},
		{"trigger", "TEXT"},
		{"browser_json", "TEXT"},
		{"python_executable", "TEXT"},
		{"script_path", "TEXT"},
		{"working_directory", "TEXT"},
		{"environment_strategy", "TEXT"},
		{"timeout_seconds", "INTEGER"},
		{"max_output_kb", "INTEGER"},
		{"cancel_requested", "INTEGER"},
		{"cancel_reason", "TEXT"},
		{"cancel_requested_at", "TEXT"},
		{"stdout_tail", "TEXT"},
		{"stderr_tail", "TEXT"},
	}
	for _, column := range columns {
		if existing[column.name] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE active_runs ADD COLUMN %s %s`, column.name, column.columnType)); err != nil {
			return fmt.Errorf("failed to add active run column %s: %w", column.name, err)
		}
	}
	return nil
}

func createSQLiteSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start SQLite schema migration: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			package_dir TEXT NOT NULL UNIQUE,
			enabled INTEGER NOT NULL,
			created_at TEXT,
			last_reloaded_at TEXT,
			manifest_hash TEXT,
			manifest_mod_time TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS delivery_profiles (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			driver_type TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			config_json TEXT NOT NULL,
			inbound_commands_enabled INTEGER NOT NULL,
			authorized_chat_ids_json TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS run_records (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			trigger TEXT NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL,
			status TEXT NOT NULL,
			exit_code INTEGER NOT NULL,
			timed_out INTEGER NOT NULL,
			duration_ms INTEGER NOT NULL,
			summary TEXT,
			stdout TEXT,
			stderr TEXT,
			parsed_result_json TEXT,
			diagnostics_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_run_records_task_started ON run_records(task_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_run_records_status ON run_records(status)`,
		`CREATE INDEX IF NOT EXISTS idx_run_records_trigger ON run_records(trigger)`,
		`CREATE TABLE IF NOT EXISTS delivery_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			profile_id TEXT NOT NULL,
			profile_name TEXT NOT NULL,
			status TEXT NOT NULL,
			error TEXT,
			FOREIGN KEY(run_id) REFERENCES run_records(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_delivery_results_run ON delivery_results(run_id, position)`,
		`CREATE TABLE IF NOT EXISTS active_runs (
			run_id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			task_name TEXT,
			task_slug TEXT,
			trigger TEXT,
			browser_json TEXT,
			root_pid INTEGER,
			process_group_id INTEGER,
			run_directory TEXT,
			python_executable TEXT,
			script_path TEXT,
			working_directory TEXT,
			environment_strategy TEXT,
			timeout_seconds INTEGER,
			max_output_kb INTEGER,
			started_at TEXT NOT NULL,
			cancel_requested INTEGER,
			cancel_reason TEXT,
			cancel_requested_at TEXT,
			stdout_tail TEXT,
			stderr_tail TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS command_log (
			id TEXT PRIMARY KEY,
			position INTEGER NOT NULL,
			channel_type TEXT NOT NULL,
			chat_id TEXT,
			command_text TEXT NOT NULL,
			matched_command TEXT,
			reply_text TEXT,
			received_at TEXT NOT NULL,
			error TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_command_log_received ON command_log(received_at DESC)`,
		`PRAGMA user_version = 1`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to apply SQLite state schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit SQLite state schema: %w", err)
	}
	return nil
}

func readSQLiteState(db *sql.DB) (*State, error) {
	state := &State{
		Tasks:            []PersistedTask{},
		DeliveryProfiles: []models.DeliveryProfile{},
		RunHistory:       map[string][]models.RunRecord{},
		ActiveRuns:       []models.ActiveRunInfo{},
		CommandLog:       []models.CommandRecord{},
		Settings:         Settings{WebServerPort: 9876, WebServerBind: "127.0.0.1"},
	}
	if err := readSQLiteSettings(db, &state.Settings); err != nil {
		return nil, err
	}
	tasks, err := readSQLiteTasks(db)
	if err != nil {
		return nil, err
	}
	state.Tasks = tasks
	profiles, err := readSQLiteDeliveryProfiles(db)
	if err != nil {
		return nil, err
	}
	state.DeliveryProfiles = profiles
	history, err := readSQLiteRunHistory(db)
	if err != nil {
		return nil, err
	}
	state.RunHistory = history
	activeRuns, err := readSQLiteActiveRuns(db)
	if err != nil {
		return nil, err
	}
	state.ActiveRuns = activeRuns
	commandLog, err := readSQLiteCommandLog(db)
	if err != nil {
		return nil, err
	}
	state.CommandLog = commandLog
	return state, nil
}

func readSQLiteSettings(db *sql.DB, settings *Settings) error {
	rows, err := db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return fmt.Errorf("failed to read settings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("failed to scan setting: %w", err)
		}
		switch key {
		case "webServerPort":
			if port, err := strconv.Atoi(value); err == nil {
				settings.WebServerPort = port
			}
		case "webServerBind":
			settings.WebServerBind = value
		case "maxRunsPerTask":
			if n, err := strconv.Atoi(value); err == nil {
				settings.MaxRunsPerTask = n
			}
		case "maxRunAgeDays":
			if n, err := strconv.Atoi(value); err == nil {
				settings.MaxRunAgeDays = n
			}
		case "maxRunOutputKB":
			if n, err := strconv.Atoi(value); err == nil {
				settings.MaxRunOutputKB = n
			}
		}
	}
	return rows.Err()
}

func readSQLiteTasks(db *sql.DB) ([]PersistedTask, error) {
	rows, err := db.Query(`SELECT id, package_dir, enabled, created_at, last_reloaded_at, manifest_hash, manifest_mod_time FROM tasks ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("failed to read tasks: %w", err)
	}
	defer rows.Close()
	var tasks []PersistedTask
	for rows.Next() {
		var task PersistedTask
		var enabled int
		var createdAt, lastReloadedAt, manifestModTime, manifestHash sql.NullString
		if err := rows.Scan(&task.ID, &task.PackageDir, &enabled, &createdAt, &lastReloadedAt, &manifestHash, &manifestModTime); err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}
		task.Enabled = enabled != 0
		task.CreatedAt = parseSQLiteTime(createdAt.String)
		task.LastReloadedAt = parseSQLiteTime(lastReloadedAt.String)
		task.ManifestHash = manifestHash.String
		task.ManifestModTime = parseSQLiteTime(manifestModTime.String)
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func readSQLiteDeliveryProfiles(db *sql.DB) ([]models.DeliveryProfile, error) {
	rows, err := db.Query(`SELECT id, name, driver_type, enabled, config_json, inbound_commands_enabled, authorized_chat_ids_json FROM delivery_profiles ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("failed to read delivery profiles: %w", err)
	}
	defer rows.Close()
	var profiles []models.DeliveryProfile
	for rows.Next() {
		var profile models.DeliveryProfile
		var enabled, inboundCommandsEnabled int
		var configJSON, authorizedChatIDsJSON string
		if err := rows.Scan(&profile.ID, &profile.Name, &profile.DriverType, &enabled, &configJSON, &inboundCommandsEnabled, &authorizedChatIDsJSON); err != nil {
			return nil, fmt.Errorf("failed to scan delivery profile: %w", err)
		}
		profile.Enabled = enabled != 0
		profile.InboundCommandsEnabled = inboundCommandsEnabled != 0
		if err := json.Unmarshal([]byte(configJSON), &profile.Config); err != nil {
			return nil, fmt.Errorf("failed to decode delivery profile config for %s: %w", profile.ID, err)
		}
		if err := json.Unmarshal([]byte(authorizedChatIDsJSON), &profile.AuthorizedChatIDs); err != nil {
			return nil, fmt.Errorf("failed to decode delivery profile authorized chats for %s: %w", profile.ID, err)
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func readSQLiteRunHistory(db *sql.DB) (map[string][]models.RunRecord, error) {
	deliveryResults, err := readSQLiteDeliveryResults(db)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id, task_id, trigger, started_at, finished_at, status, summary, stdout, stderr, parsed_result_json, timed_out, duration_ms, exit_code, diagnostics_json FROM run_records ORDER BY task_id, started_at DESC, rowid DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to read run history: %w", err)
	}
	defer rows.Close()
	history := map[string][]models.RunRecord{}
	for rows.Next() {
		var record models.RunRecord
		var summary, stdout, stderr, parsedResultJSON, diagnosticsJSON sql.NullString
		var startedAt, finishedAt string
		var status string
		var timedOut int
		if err := rows.Scan(&record.ID, &record.TaskID, &record.Trigger, &startedAt, &finishedAt, &status, &summary, &stdout, &stderr, &parsedResultJSON, &timedOut, &record.Outcome.DurationMs, &record.Outcome.ExitCode, &diagnosticsJSON); err != nil {
			return nil, fmt.Errorf("failed to scan run record: %w", err)
		}
		record.StartedAt = parseSQLiteTime(startedAt)
		record.FinishedAt = parseSQLiteTime(finishedAt)
		record.Outcome.Stdout = stdout.String
		record.Outcome.Stderr = stderr.String
		record.Outcome.TimedOut = timedOut != 0
		if parsedResultJSON.Valid && parsedResultJSON.String != "" {
			var parsed models.ParsedResult
			if err := json.Unmarshal([]byte(parsedResultJSON.String), &parsed); err == nil {
				record.Outcome.ParsedResult = &parsed
			}
		}
		if diagnosticsJSON.Valid && diagnosticsJSON.String != "" {
			_ = json.Unmarshal([]byte(diagnosticsJSON.String), &record.Outcome.Diagnostics)
		}
		applyStoredRunResult(&record, status, summary.String)
		record.DeliveryResults = deliveryResults[record.ID]
		history[record.TaskID] = append(history[record.TaskID], record)
	}
	return history, rows.Err()
}

func applyStoredRunResult(record *models.RunRecord, status, summary string) {
	status = models.NormalizeRunStatus(status)
	hasStatus := models.IsValidRunStatus(status)
	if record.Outcome.ParsedResult == nil {
		if hasStatus || summary != "" {
			record.Outcome.ParsedResult = &models.ParsedResult{Summary: summary}
		}
	} else if record.Outcome.ParsedResult.Summary == "" {
		record.Outcome.ParsedResult.Summary = summary
	}
	if !hasStatus || record.Outcome.ParsedResult == nil {
		return
	}
	if !models.IsValidRunStatus(record.Outcome.ParsedResult.Status) {
		record.Outcome.ParsedResult.Status = status
	}
}

func readSQLiteDeliveryResults(db *sql.DB) (map[string][]models.DeliveryResult, error) {
	rows, err := db.Query(`SELECT run_id, profile_id, profile_name, status, error FROM delivery_results ORDER BY run_id, position`)
	if err != nil {
		return nil, fmt.Errorf("failed to read delivery results: %w", err)
	}
	defer rows.Close()
	results := map[string][]models.DeliveryResult{}
	for rows.Next() {
		var runID string
		var result models.DeliveryResult
		var resultError sql.NullString
		if err := rows.Scan(&runID, &result.ProfileID, &result.ProfileName, &result.Status, &resultError); err != nil {
			return nil, fmt.Errorf("failed to scan delivery result: %w", err)
		}
		result.Error = resultError.String
		results[runID] = append(results[runID], result)
	}
	return results, rows.Err()
}

func readSQLiteActiveRuns(db *sql.DB) ([]models.ActiveRunInfo, error) {
	rows, err := db.Query(`SELECT task_id, run_id, task_name, task_slug, trigger, browser_json, root_pid, process_group_id, run_directory, python_executable, script_path, working_directory, environment_strategy, timeout_seconds, max_output_kb, started_at, cancel_requested, cancel_reason, cancel_requested_at, stdout_tail, stderr_tail FROM active_runs ORDER BY started_at`)
	if err != nil {
		return nil, fmt.Errorf("failed to read active runs: %w", err)
	}
	defer rows.Close()
	var activeRuns []models.ActiveRunInfo
	for rows.Next() {
		var info models.ActiveRunInfo
		var startedAt string
		var taskName, taskSlug, trigger, browserJSON, runDirectory, pythonExecutable, scriptPath, workingDirectory, environmentStrategy, cancelReason, cancelRequestedAt, stdoutTail, stderrTail sql.NullString
		var rootPID, processGroupID, timeoutSeconds, maxOutputKB, cancelRequested sql.NullInt64
		if err := rows.Scan(&info.TaskID, &info.RunID, &taskName, &taskSlug, &trigger, &browserJSON, &rootPID, &processGroupID, &runDirectory, &pythonExecutable, &scriptPath, &workingDirectory, &environmentStrategy, &timeoutSeconds, &maxOutputKB, &startedAt, &cancelRequested, &cancelReason, &cancelRequestedAt, &stdoutTail, &stderrTail); err != nil {
			return nil, fmt.Errorf("failed to scan active run: %w", err)
		}
		info.TaskName = taskName.String
		info.TaskSlug = taskSlug.String
		info.Trigger = trigger.String
		if browserJSON.Valid && browserJSON.String != "" {
			_ = json.Unmarshal([]byte(browserJSON.String), &info.Browser)
		}
		info.RootPID = int(rootPID.Int64)
		info.ProcessGroupID = int(processGroupID.Int64)
		info.RunDirectory = runDirectory.String
		info.PythonExecutable = pythonExecutable.String
		info.ScriptPath = scriptPath.String
		info.WorkingDirectory = workingDirectory.String
		info.EnvironmentStrategy = environmentStrategy.String
		info.TimeoutSeconds = int(timeoutSeconds.Int64)
		info.MaxOutputKB = int(maxOutputKB.Int64)
		info.StartedAt = parseSQLiteTime(startedAt)
		info.CancelRequested = cancelRequested.Int64 != 0
		info.CancelReason = cancelReason.String
		if cancelRequestedAt.String != "" {
			t := parseSQLiteTime(cancelRequestedAt.String)
			info.CancelRequestedAt = &t
		}
		info.StdoutTail = stdoutTail.String
		info.StderrTail = stderrTail.String
		activeRuns = append(activeRuns, info)
	}
	return activeRuns, rows.Err()
}

func readSQLiteCommandLog(db *sql.DB) ([]models.CommandRecord, error) {
	rows, err := db.Query(`SELECT id, channel_type, chat_id, command_text, matched_command, reply_text, received_at, error FROM command_log ORDER BY position`)
	if err != nil {
		return nil, fmt.Errorf("failed to read command log: %w", err)
	}
	defer rows.Close()
	var records []models.CommandRecord
	for rows.Next() {
		var record models.CommandRecord
		var matchedCommand, replyText, recordError sql.NullString
		var receivedAt string
		if err := rows.Scan(&record.ID, &record.ChannelType, &record.ChatID, &record.CommandText, &matchedCommand, &replyText, &receivedAt, &recordError); err != nil {
			return nil, fmt.Errorf("failed to scan command record: %w", err)
		}
		record.MatchedCommand = matchedCommand.String
		record.ReplyText = replyText.String
		record.ReceivedAt = parseSQLiteTime(receivedAt)
		record.Error = recordError.String
		records = append(records, record)
	}
	return records, rows.Err()
}

func writeSQLiteState(db *sql.DB, state *State) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start SQLite state write: %w", err)
	}
	defer tx.Rollback()

	deletes := []string{
		`DELETE FROM delivery_results`,
		`DELETE FROM run_records`,
		`DELETE FROM active_runs`,
		`DELETE FROM command_log`,
		`DELETE FROM delivery_profiles`,
		`DELETE FROM tasks`,
		`DELETE FROM settings`,
	}
	for _, stmt := range deletes {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to clear SQLite state: %w", err)
		}
	}

	if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES
		('webServerPort', ?),
		('webServerBind', ?),
		('maxRunsPerTask', ?),
		('maxRunAgeDays', ?),
		('maxRunOutputKB', ?)`,
		strconv.Itoa(state.Settings.WebServerPort),
		state.Settings.WebServerBind,
		strconv.Itoa(state.Settings.MaxRunsPerTask),
		strconv.Itoa(state.Settings.MaxRunAgeDays),
		strconv.Itoa(state.Settings.MaxRunOutputKB)); err != nil {
		return fmt.Errorf("failed to write settings: %w", err)
	}
	if err := writeSQLiteTasks(tx, state.Tasks); err != nil {
		return err
	}
	if err := writeSQLiteDeliveryProfiles(tx, state.DeliveryProfiles); err != nil {
		return err
	}
	if err := writeSQLiteRunHistory(tx, state.RunHistory); err != nil {
		return err
	}
	if err := writeSQLiteActiveRuns(tx, state.ActiveRuns); err != nil {
		return err
	}
	if err := writeSQLiteCommandLog(tx, state.CommandLog); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit SQLite state: %w", err)
	}
	return nil
}

func writeSQLiteTasks(tx *sql.Tx, tasks []PersistedTask) error {
	stmt, err := tx.Prepare(`INSERT INTO tasks(id, package_dir, enabled, created_at, last_reloaded_at, manifest_hash, manifest_mod_time) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare task insert: %w", err)
	}
	defer stmt.Close()
	for _, task := range tasks {
		if _, err := stmt.Exec(task.ID, task.PackageDir, boolInt(task.Enabled), formatSQLiteTime(task.CreatedAt), formatSQLiteTime(task.LastReloadedAt), task.ManifestHash, formatSQLiteTime(task.ManifestModTime)); err != nil {
			return fmt.Errorf("failed to write task %s: %w", task.ID, err)
		}
	}
	return nil
}

func writeSQLiteDeliveryProfiles(tx *sql.Tx, profiles []models.DeliveryProfile) error {
	stmt, err := tx.Prepare(`INSERT INTO delivery_profiles(id, name, driver_type, enabled, config_json, inbound_commands_enabled, authorized_chat_ids_json) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare delivery profile insert: %w", err)
	}
	defer stmt.Close()
	for _, profile := range profiles {
		configJSON, err := marshalJSONString(profile.Config)
		if err != nil {
			return fmt.Errorf("failed to encode delivery profile config for %s: %w", profile.ID, err)
		}
		authorizedChatIDsJSON, err := marshalJSONString(profile.AuthorizedChatIDs)
		if err != nil {
			return fmt.Errorf("failed to encode delivery profile authorized chats for %s: %w", profile.ID, err)
		}
		if _, err := stmt.Exec(profile.ID, profile.Name, profile.DriverType, boolInt(profile.Enabled), configJSON, boolInt(profile.InboundCommandsEnabled), authorizedChatIDsJSON); err != nil {
			return fmt.Errorf("failed to write delivery profile %s: %w", profile.ID, err)
		}
	}
	return nil
}

func writeSQLiteRunHistory(tx *sql.Tx, runHistory map[string][]models.RunRecord) error {
	recordStmt, err := tx.Prepare(`INSERT INTO run_records(id, task_id, trigger, started_at, finished_at, status, exit_code, timed_out, duration_ms, summary, stdout, stderr, parsed_result_json, diagnostics_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare run record insert: %w", err)
	}
	defer recordStmt.Close()
	deliveryStmt, err := tx.Prepare(`INSERT INTO delivery_results(run_id, position, profile_id, profile_name, status, error) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare delivery result insert: %w", err)
	}
	defer deliveryStmt.Close()
	for taskID, records := range runHistory {
		for _, record := range records {
			if record.TaskID == "" {
				record.TaskID = taskID
			}
			parsedResultJSON := ""
			summary := ""
			if record.Outcome.ParsedResult != nil {
				var err error
				parsedResultJSON, err = marshalJSONString(record.Outcome.ParsedResult)
				if err != nil {
					continue
				}
				summary = record.Outcome.ParsedResult.Summary
			}
			diagnosticsJSON, err := marshalJSONString(record.Outcome.Diagnostics)
			if err != nil {
				continue
			}
			status := models.RunStatusFromOutcome(record.Outcome)
			if _, err := recordStmt.Exec(record.ID, record.TaskID, record.Trigger, formatSQLiteTime(record.StartedAt), formatSQLiteTime(record.FinishedAt), status, record.Outcome.ExitCode, boolInt(record.Outcome.TimedOut), record.Outcome.DurationMs, summary, record.Outcome.Stdout, record.Outcome.Stderr, nullString(parsedResultJSON), diagnosticsJSON); err != nil {
				continue
			}
			for i, result := range record.DeliveryResults {
				_, _ = deliveryStmt.Exec(record.ID, i, result.ProfileID, result.ProfileName, result.Status, nullString(result.Error))
			}
		}
	}
	return nil
}

func writeSQLiteActiveRuns(tx *sql.Tx, activeRuns []models.ActiveRunInfo) error {
	stmt, err := tx.Prepare(`INSERT INTO active_runs(task_id, run_id, task_name, task_slug, trigger, browser_json, root_pid, process_group_id, run_directory, python_executable, script_path, working_directory, environment_strategy, timeout_seconds, max_output_kb, started_at, cancel_requested, cancel_reason, cancel_requested_at, stdout_tail, stderr_tail) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare active run insert: %w", err)
	}
	defer stmt.Close()
	for _, info := range activeRuns {
		cancelRequestedAt := ""
		if info.CancelRequestedAt != nil {
			cancelRequestedAt = formatSQLiteTime(*info.CancelRequestedAt)
		}
		browserJSON, err := marshalJSONString(info.Browser)
		if err != nil {
			return fmt.Errorf("failed to encode active run browser diagnostics for %s: %w", info.RunID, err)
		}
		if !info.Browser.Enabled {
			browserJSON = ""
		}
		if _, err := stmt.Exec(info.TaskID, info.RunID, nullString(info.TaskName), nullString(info.TaskSlug), nullString(info.Trigger), nullString(browserJSON), info.RootPID, info.ProcessGroupID, nullString(info.RunDirectory), nullString(info.PythonExecutable), nullString(info.ScriptPath), nullString(info.WorkingDirectory), nullString(info.EnvironmentStrategy), info.TimeoutSeconds, info.MaxOutputKB, formatSQLiteTime(info.StartedAt), boolInt(info.CancelRequested), nullString(info.CancelReason), nullString(cancelRequestedAt), nullString(info.StdoutTail), nullString(info.StderrTail)); err != nil {
			return fmt.Errorf("failed to write active run %s: %w", info.RunID, err)
		}
	}
	return nil
}

func writeSQLiteCommandLog(tx *sql.Tx, records []models.CommandRecord) error {
	stmt, err := tx.Prepare(`INSERT INTO command_log(id, position, channel_type, chat_id, command_text, matched_command, reply_text, received_at, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare command log insert: %w", err)
	}
	defer stmt.Close()
	for i, record := range records {
		if _, err := stmt.Exec(record.ID, i, record.ChannelType, record.ChatID, record.CommandText, nullString(record.MatchedCommand), nullString(record.ReplyText), formatSQLiteTime(record.ReceivedAt), nullString(record.Error)); err != nil {
			return fmt.Errorf("failed to write command record %s: %w", record.ID, err)
		}
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatSQLiteTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseSQLiteTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func marshalJSONString(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
