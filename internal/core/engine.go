package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ran-su/cronplus/internal/delivery"
	"github.com/ran-su/cronplus/internal/manifest"
	"github.com/ran-su/cronplus/internal/models"
	"github.com/ran-su/cronplus/internal/store"
)

// Engine is the central state owner for the daemon.
type Engine struct {
	mu               sync.RWMutex
	tasks            []*models.Task
	deliveryProfiles []models.DeliveryProfile
	runHistory       map[string][]models.RunRecord
	activeRuns       map[string]bool
	activeRunDetails map[string]models.ActiveRunInfo
	taskGenerations  map[string]int64
	commandLog       []models.CommandRecord

	store           *store.Store
	scheduler       *Scheduler
	Broker          *EventBroker
	DeliveryService *delivery.Service

	maxRunsPerTask    int
	maxConcurrentRuns int

	// OnDeliveryProfilesChanged is called after delivery profile mutations.
	OnDeliveryProfilesChanged func()
}

// NewEngine creates a new engine with the given store and delivery service.
func NewEngine(s *store.Store, deliverySvc *delivery.Service) *Engine {
	return &Engine{
		runHistory:        make(map[string][]models.RunRecord),
		activeRuns:        make(map[string]bool),
		activeRunDetails:  make(map[string]models.ActiveRunInfo),
		taskGenerations:   make(map[string]int64),
		store:             s,
		Broker:            NewEventBroker(),
		DeliveryService:   deliverySvc,
		maxRunsPerTask:    50,
		maxConcurrentRuns: 2,
	}
}

func (e *Engine) SetMaxConcurrentRuns(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n < 1 {
		n = 1
	}
	e.maxConcurrentRuns = n
}

// RestoreState loads persisted state and re-imports task manifests.
func (e *Engine) RestoreState() error {
	state, err := e.store.Load()
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.deliveryProfiles = cloneDeliveryProfiles(state.DeliveryProfiles)
	e.runHistory = cloneRunHistoryMap(state.RunHistory)
	e.commandLog = append([]models.CommandRecord(nil), state.CommandLog...)
	e.mu.Unlock()
	hadPersistedActiveRuns := len(state.ActiveRuns) > 0
	e.cleanupPersistedActiveRuns(state.ActiveRuns)

	// Restore tasks by re-loading manifests
	for _, pt := range state.Tasks {
		if _, err := e.importTask(pt.PackageDir, pt.Enabled, pt.ID, pt.CreatedAt); err != nil {
			log.Printf("[CronPlus] Warning: failed to restore task %s: %v", pt.PackageDir, err)
		}
	}
	if hadPersistedActiveRuns {
		if err := e.PersistState(); err != nil {
			log.Printf("[CronPlus] Warning: failed to clear stale active runs: %v", err)
		}
	}

	return nil
}

// PersistState saves the current state to disk.
func (e *Engine) PersistState() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tasks := make([]store.PersistedTask, len(e.tasks))
	for i, t := range e.tasks {
		tasks[i] = store.PersistedTask{
			ID:              t.ID,
			PackageDir:      t.PackageDir,
			Enabled:         t.Enabled,
			CreatedAt:       t.CreatedAt,
			LastReloadedAt:  t.LastReloadedAt,
			ManifestHash:    t.ManifestHash,
			ManifestModTime: t.ManifestModTime,
		}
	}
	activeRuns := make([]models.ActiveRunInfo, 0, len(e.activeRunDetails))
	for _, info := range e.activeRunDetails {
		activeRuns = append(activeRuns, info)
	}

	state := &store.State{
		Tasks:            tasks,
		DeliveryProfiles: cloneDeliveryProfiles(e.deliveryProfiles),
		RunHistory:       cloneRunHistoryMap(e.runHistory),
		ActiveRuns:       activeRuns,
		CommandLog:       append([]models.CommandRecord(nil), e.commandLog...),
		Settings:         store.Settings{WebServerPort: 9876, WebServerBind: "127.0.0.1"},
	}

	return e.store.Save(state)
}

// --- Task Management ---

// ImportTask loads a task from a package directory.
func (e *Engine) ImportTask(dirPath string, enabled bool) (*models.Task, error) {
	return e.importTask(dirPath, enabled, "", time.Time{})
}

// ReloadTask re-reads an imported task's manifest from disk while preserving its ID and state.
func (e *Engine) ReloadTask(taskID string) (*models.Task, error) {
	e.mu.RLock()
	var packageDir string
	var enabled bool
	var createdAt time.Time
	for _, t := range e.tasks {
		if t.ID == taskID {
			packageDir = t.PackageDir
			enabled = t.Enabled
			createdAt = t.CreatedAt
			break
		}
	}
	e.mu.RUnlock()

	if packageDir == "" {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	return e.importTask(packageDir, enabled, taskID, createdAt)
}

func (e *Engine) importTask(dirPath string, enabled bool, restoredID string, createdAt time.Time) (*models.Task, error) {
	manifestPath, err := manifest.FindManifest(dirPath)
	if err != nil {
		return nil, err
	}

	result, err := manifest.Load(manifestPath)
	if err != nil {
		return nil, err
	}
	if result.HasErrors() {
		msgs := make([]string, len(result.Issues))
		for i, issue := range result.Issues {
			msgs[i] = fmt.Sprintf("[%s] %s: %s", issue.Severity, issue.Path, issue.Message)
		}
		return nil, fmt.Errorf("manifest validation failed:\n%s", joinLines(msgs))
	}

	m := result.Manifest
	manifestHash, manifestModTime, err := fileHashAndModTime(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect manifest: %w", err)
	}
	now := time.Now()
	if createdAt.IsZero() {
		createdAt = now
	}

	// Ensure environment is ready
	if err := EnsureEnvironment(m, filepath.Dir(manifestPath)); err != nil {
		return nil, fmt.Errorf("environment setup failed for %s: %w", m.Script.Name, err)
	}

	profilesChanged := e.mergeInlineProfiles(m)
	if profilesChanged {
		e.notifyDeliveryProfilesChanged()
	}

	displayName := m.Script.Name
	if displayName == "" {
		displayName = filepath.Base(dirPath)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Check for existing task with same package dir
	for i, t := range e.tasks {
		if t.PackageDir == dirPath {
			if restoredID != "" {
				e.tasks[i].ID = restoredID
			}
			e.tasks[i].Manifest = m
			e.tasks[i].ManifestPath = manifestPath
			e.tasks[i].DisplayName = displayName
			e.tasks[i].Enabled = enabled
			e.tasks[i].CreatedAt = createdAt
			e.tasks[i].LastReloadedAt = now
			e.tasks[i].ManifestHash = manifestHash
			e.tasks[i].ManifestModTime = manifestModTime
			if e.taskGenerations[e.tasks[i].ID] == 0 {
				e.taskGenerations[e.tasks[i].ID] = 1
			}
			taskCopy := cloneTask(e.tasks[i])
			e.Broker.Publish("task_updated", taskCopy)
			return taskCopy, nil
		}
	}

	id := restoredID
	if id == "" {
		id = generateID()
	}

	task := &models.Task{
		ID:              id,
		PackageDir:      dirPath,
		ManifestPath:    manifestPath,
		Manifest:        m,
		Enabled:         enabled,
		CreatedAt:       createdAt,
		LastReloadedAt:  now,
		ManifestHash:    manifestHash,
		ManifestModTime: manifestModTime,
		DisplayName:     displayName,
	}

	e.tasks = append(e.tasks, task)
	if e.taskGenerations[id] == 0 {
		e.taskGenerations[id] = 1
	}
	taskCopy := cloneTask(task)
	e.Broker.Publish("task_updated", taskCopy)
	return taskCopy, nil
}

// RemoveTask removes a task by ID.
func (e *Engine) RemoveTask(taskID string) error {
	e.mu.Lock()
	var activeRuns []models.ActiveRunInfo
	for i, t := range e.tasks {
		if t.ID == taskID {
			e.tasks = append(e.tasks[:i], e.tasks[i+1:]...)
			delete(e.runHistory, taskID)
			delete(e.activeRuns, taskID)
			e.taskGenerations[taskID]++
			for runID, info := range e.activeRunDetails {
				if info.TaskID == taskID {
					activeRuns = append(activeRuns, info)
					delete(e.activeRunDetails, runID)
				}
			}
			e.mu.Unlock()
			if len(activeRuns) > 0 {
				log.Printf("[CronPlus] Removing task %s; terminating %d active run(s).", taskID, len(activeRuns))
				for _, info := range activeRuns {
					cleanup := cleanupPersistedRunProcess(info, 5*time.Second)
					logRunCleanup("Removed task active run cleanup", info.RunID, cleanup)
				}
			}
			e.Broker.Publish("task_updated", map[string]string{"id": taskID, "removed": "true"})
			return nil
		}
	}
	e.mu.Unlock()
	return fmt.Errorf("task not found: %s", taskID)
}

// SetTaskEnabled enables or disables a task.
func (e *Engine) SetTaskEnabled(taskID string, enabled bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, t := range e.tasks {
		if t.ID == taskID {
			t.Enabled = enabled
			e.Broker.Publish("task_updated", t)
			return nil
		}
	}
	return fmt.Errorf("task not found: %s", taskID)
}

// RunTask executes a task and returns the run record.
func (e *Engine) RunTask(taskID, trigger string) (*models.RunRecord, error) {
	reserved, err := e.reserveTaskRun(taskID, trigger)
	if err != nil {
		return nil, err
	}
	return e.executeReservedRun(reserved, trigger), nil
}

// StartTaskRun reserves and starts a task run in the background.
func (e *Engine) StartTaskRun(taskID, trigger string) error {
	reserved, err := e.reserveTaskRun(taskID, trigger)
	if err != nil {
		return err
	}
	go e.executeReservedRun(reserved, trigger)
	return nil
}

type reservedTaskRun struct {
	task       *models.Task
	generation int64
}

func (e *Engine) reserveTaskRun(taskID, trigger string) (*reservedTaskRun, error) {
	e.mu.Lock()
	var task *models.Task
	for _, t := range e.tasks {
		if t.ID == taskID {
			task = t
			break
		}
	}
	if task == nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if task.Manifest == nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("task has no valid manifest")
	}
	if e.activeRuns[taskID] {
		e.mu.Unlock()
		return nil, fmt.Errorf("task is already running")
	}
	if e.maxConcurrentRuns > 0 && len(e.activeRuns) >= e.maxConcurrentRuns {
		e.mu.Unlock()
		return nil, fmt.Errorf("maximum concurrent runs reached (%d)", e.maxConcurrentRuns)
	}
	e.activeRuns[taskID] = true
	generation := e.taskGenerations[taskID]
	if generation == 0 {
		generation = 1
		e.taskGenerations[taskID] = generation
	}
	taskSnapshot := cloneTask(task)
	e.mu.Unlock()

	e.Broker.Publish("run_started", map[string]string{
		"taskID":  taskID,
		"trigger": trigger,
	})

	return &reservedTaskRun{task: taskSnapshot, generation: generation}, nil
}

func (e *Engine) executeReservedRun(reserved *reservedTaskRun, trigger string) *models.RunRecord {
	task := reserved.task
	taskID := task.ID
	manifestDir := filepath.Dir(task.ManifestPath)
	startedAt := time.Now()
	runID := generateID()

	// Run the script (this blocks — called in a goroutine by the scheduler/API)
	outcome := RunScriptWithOptions(task.Manifest, manifestDir, RunScriptOptions{
		TaskID: taskID,
		RunID:  runID,
		OnStarted: func(info models.ActiveRunInfo) {
			e.recordActiveRun(info, reserved.generation)
		},
		OnFinished: func(runID string) {
			e.clearActiveRunRecord(runID)
		},
	})

	finishedAt := time.Now()

	record := &models.RunRecord{
		ID:         runID,
		TaskID:     taskID,
		Trigger:    trigger,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Outcome:    *outcome,
	}

	shouldKeepRun := e.taskGenerationMatches(taskID, reserved.generation)

	// Process deliveries
	if shouldKeepRun && e.DeliveryService != nil {
		profiles := e.DeliveryProfiles()
		deliveryResults := e.DeliveryService.Deliver(task, record, profiles)
		record.DeliveryResults = deliveryResults

		for _, dr := range deliveryResults {
			e.Broker.Publish("delivery_sent", map[string]string{
				"taskID":      taskID,
				"profileName": dr.ProfileName,
				"status":      dr.Status,
			})
		}
	}

	// Store run history
	e.mu.Lock()
	delete(e.activeRuns, taskID)
	if e.taskGenerationMatchesLocked(taskID, reserved.generation) {
		history := e.runHistory[taskID]
		history = append([]models.RunRecord{cloneRunRecord(*record)}, history...)
		if len(history) > e.maxRunsPerTask {
			history = history[:e.maxRunsPerTask]
		}
		e.runHistory[taskID] = history
		shouldKeepRun = true
	} else {
		shouldKeepRun = false
	}
	e.mu.Unlock()

	if shouldKeepRun {
		e.Broker.Publish("run_completed", record)
	}

	// Persist after run
	if err := e.PersistState(); err != nil {
		log.Printf("[CronPlus] Warning: failed to persist state after run: %v", err)
	}

	return record
}

func (e *Engine) recordActiveRun(info models.ActiveRunInfo, generation int64) {
	e.mu.Lock()
	if !e.taskGenerationMatchesLocked(info.TaskID, generation) {
		e.mu.Unlock()
		cleanup := cleanupPersistedRunProcess(info, 5*time.Second)
		logRunCleanup("Stale active run cleanup", info.RunID, cleanup)
		return
	}
	e.activeRunDetails[info.RunID] = info
	e.mu.Unlock()
	if err := e.PersistState(); err != nil {
		log.Printf("[CronPlus] Warning: failed to persist active run metadata: %v", err)
	}
}

func (e *Engine) clearActiveRunRecord(runID string) {
	e.mu.Lock()
	delete(e.activeRunDetails, runID)
	e.mu.Unlock()
	if err := e.PersistState(); err != nil {
		log.Printf("[CronPlus] Warning: failed to clear active run metadata: %v", err)
	}
}

func (e *Engine) cleanupPersistedActiveRuns(activeRuns []models.ActiveRunInfo) {
	if len(activeRuns) == 0 {
		return
	}
	grace := 3 * time.Second
	for _, info := range activeRuns {
		cleanup := cleanupPersistedRunProcess(info, grace)
		log.Printf("[CronPlus] Cleaned stale active run %s for task %s: process_group_terminated=%t process_group_force_killed=%t detached_processes_killed=%d run_dir_removed=%t",
			info.RunID,
			info.TaskID,
			cleanup.ProcessGroupTerminated,
			cleanup.ProcessGroupForceKilled,
			cleanup.DetachedProcessesKilled,
			cleanup.RunDirectoryRemoved,
		)
		if cleanup.OrphanScanError != "" {
			log.Printf("[CronPlus] Warning: stale run orphan scan failed for %s: %s", info.RunID, cleanup.OrphanScanError)
		}
		if cleanup.RunDirectoryCleanupError != "" {
			log.Printf("[CronPlus] Warning: stale run directory cleanup failed for %s: %s", info.RunID, cleanup.RunDirectoryCleanupError)
		}
	}
}

func (e *Engine) TerminateActiveRuns(reason string) {
	e.mu.RLock()
	activeRuns := make([]models.ActiveRunInfo, 0, len(e.activeRunDetails))
	for _, info := range e.activeRunDetails {
		activeRuns = append(activeRuns, info)
	}
	e.mu.RUnlock()

	if len(activeRuns) == 0 {
		return
	}
	log.Printf("[CronPlus] Terminating %d active run(s): %s", len(activeRuns), reason)
	for _, info := range activeRuns {
		cleanup := cleanupPersistedRunProcess(info, 5*time.Second)
		log.Printf("[CronPlus] Active run cleanup %s: process_group_terminated=%t process_group_force_killed=%t detached_processes_killed=%d run_dir_removed=%t",
			info.RunID,
			cleanup.ProcessGroupTerminated,
			cleanup.ProcessGroupForceKilled,
			cleanup.DetachedProcessesKilled,
			cleanup.RunDirectoryRemoved,
		)
		e.clearActiveRunRecord(info.RunID)
	}
}

// --- Queries ---

// Tasks returns a copy of all tasks.
func (e *Engine) Tasks() []*models.Task {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]*models.Task, len(e.tasks))
	for i, task := range e.tasks {
		result[i] = cloneTask(task)
	}
	return result
}

// Task returns a task by ID.
func (e *Engine) Task(id string) *models.Task {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, t := range e.tasks {
		if t.ID == id {
			return cloneTask(t)
		}
	}
	return nil
}

// TaskBySlug returns a task by its slug.
func (e *Engine) TaskBySlug(slug string) *models.Task {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, t := range e.tasks {
		if t.Slug() == slug {
			return cloneTask(t)
		}
	}
	return nil
}

// RunHistory returns run history for a task.
func (e *Engine) RunHistory(taskID string) []models.RunRecord {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return cloneRunRecords(e.runHistory[taskID])
}

// RunRecord returns a specific run.
func (e *Engine) RunRecord(taskID, runID string) *models.RunRecord {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for i := range e.runHistory[taskID] {
		if e.runHistory[taskID][i].ID == runID {
			run := cloneRunRecord(e.runHistory[taskID][i])
			return &run
		}
	}
	return nil
}

// IsRunning returns whether a task is currently executing.
func (e *Engine) IsRunning(taskID string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeRuns[taskID]
}

// ManifestStatus compares the loaded manifest with the current file on disk.
func (e *Engine) ManifestStatus(task *models.Task) models.ManifestStatus {
	status := models.ManifestStatus{
		LoadedHash:        task.ManifestHash,
		LastReloadedAt:    task.LastReloadedAt,
		LoadedModifiedAt:  task.ManifestModTime,
		CurrentModifiedAt: task.ManifestModTime,
	}
	if task.ManifestPath == "" {
		status.Error = "task has no manifest path"
		return status
	}
	currentHash, currentModTime, err := fileHashAndModTime(task.ManifestPath)
	if err != nil {
		status.Error = err.Error()
		status.Changed = true
		return status
	}
	status.CurrentHash = currentHash
	status.CurrentModifiedAt = currentModTime
	status.Changed = task.ManifestHash != "" && task.ManifestHash != currentHash
	return status
}

// TaskTimeline summarizes recent run history for a task.
func (e *Engine) TaskTimeline(task *models.Task) models.TaskTimeline {
	history := e.RunHistory(task.ID)
	timeline := models.TaskTimeline{TotalRuns: len(history)}
	timeline.NextRunAt = e.NextRunTime(task)
	if len(history) == 0 {
		return timeline
	}

	var totalDuration int64
	for i, run := range history {
		if i == 0 {
			t := run.FinishedAt
			timeline.LastRunAt = &t
		}
		totalDuration += run.Outcome.DurationMs
		status := models.RunStatusFromOutcome(run.Outcome)
		if status == "success" && timeline.LastSuccessAt == nil {
			t := run.FinishedAt
			timeline.LastSuccessAt = &t
		}
		if status == "failure" && timeline.LastFailureAt == nil {
			t := run.FinishedAt
			timeline.LastFailureAt = &t
		}
		if i == timeline.ConsecutiveFailures && status == "failure" {
			timeline.ConsecutiveFailures++
		}
	}
	timeline.AverageDurationMs = totalDuration / int64(len(history))
	return timeline
}

// --- Delivery Profiles ---

// DeliveryProfiles returns all delivery profiles.
func (e *Engine) DeliveryProfiles() []models.DeliveryProfile {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return cloneDeliveryProfiles(e.deliveryProfiles)
}

// AddDeliveryProfile adds a new delivery profile and returns its ID.
func (e *Engine) AddDeliveryProfile(p models.DeliveryProfile) string {
	p = cloneDeliveryProfile(p)
	e.mu.Lock()
	if p.ID == "" {
		p.ID = e.nextDeliveryProfileIDLocked(p)
	} else {
		p.ID = e.uniqueDeliveryProfileIDLocked(p.ID)
	}
	e.deliveryProfiles = append(e.deliveryProfiles, p)
	e.mu.Unlock()
	e.notifyDeliveryProfilesChanged()
	return p.ID
}

// UpdateDeliveryProfile updates an existing delivery profile.
func (e *Engine) UpdateDeliveryProfile(p models.DeliveryProfile) error {
	p = cloneDeliveryProfile(p)
	e.mu.Lock()
	for i, existing := range e.deliveryProfiles {
		if existing.ID == p.ID {
			e.deliveryProfiles[i] = p
			e.mu.Unlock()
			e.notifyDeliveryProfilesChanged()
			return nil
		}
	}
	e.mu.Unlock()
	return fmt.Errorf("delivery profile not found: %s", p.ID)
}

// RemoveDeliveryProfile removes a delivery profile by ID.
func (e *Engine) RemoveDeliveryProfile(id string) error {
	e.mu.Lock()
	for i, p := range e.deliveryProfiles {
		if p.ID == id {
			e.deliveryProfiles = append(e.deliveryProfiles[:i], e.deliveryProfiles[i+1:]...)
			e.mu.Unlock()
			e.notifyDeliveryProfilesChanged()
			return nil
		}
	}
	e.mu.Unlock()
	return fmt.Errorf("delivery profile not found: %s", id)
}

// SetDeliveryProfileCommands enables or disables inbound commands for a profile.
func (e *Engine) SetDeliveryProfileCommands(id string, enabled bool) error {
	e.mu.Lock()
	for i := range e.deliveryProfiles {
		if e.deliveryProfiles[i].ID == id {
			e.deliveryProfiles[i].InboundCommandsEnabled = enabled
			e.mu.Unlock()
			e.notifyDeliveryProfilesChanged()
			return nil
		}
	}
	e.mu.Unlock()
	return fmt.Errorf("delivery profile not found: %s", id)
}

// --- Command Log ---

// CommandLog returns all command records.
func (e *Engine) CommandLog() []models.CommandRecord {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]models.CommandRecord, len(e.commandLog))
	copy(result, e.commandLog)
	return result
}

// ClearCommandLog removes all command records.
func (e *Engine) ClearCommandLog() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.commandLog = nil
}

// AddCommandRecord appends a command record to the log.
func (e *Engine) AddCommandRecord(record models.CommandRecord) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.commandLog = append([]models.CommandRecord{record}, e.commandLog...)
	if len(e.commandLog) > 200 {
		e.commandLog = e.commandLog[:200]
	}
}

// --- Helpers ---

func (e *Engine) mergeInlineProfiles(m *models.ScriptManifest) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	changed := false

	existingIDs := make(map[string]bool)
	for _, p := range e.deliveryProfiles {
		existingIDs[p.ID] = true
	}

	for _, inline := range m.Delivery.InlineProfiles {
		if existingIDs[inline.ID] {
			log.Printf("[CronPlus] Skipping inline profile '%s' (id: %s) — already exists.", inline.Name, inline.ID)
			continue
		}
		name := inline.Name
		if name == "" {
			name = inline.ID
		}
		e.deliveryProfiles = append(e.deliveryProfiles, models.DeliveryProfile{
			ID:         inline.ID,
			Name:       name,
			DriverType: inline.Driver,
			Enabled:    true,
			Config:     cloneStringMap(inline.Config),
		})
		changed = true
		log.Printf("[CronPlus] Imported inline delivery profile '%s' (id: %s).", name, inline.ID)
	}
	return changed
}

func (e *Engine) notifyDeliveryProfilesChanged() {
	if e.OnDeliveryProfilesChanged != nil {
		e.OnDeliveryProfilesChanged()
	}
}

// NextRunTime returns the next scheduled run time for a task.
func (e *Engine) NextRunTime(task *models.Task) *time.Time {
	if task.Manifest == nil {
		return nil
	}
	expr, err := ParseCron(task.Manifest.Schedule.Expression)
	if err != nil {
		return nil
	}
	tz := task.Manifest.Schedule.Timezone
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return expr.NextRun(time.Now(), loc)
}

func joinLines(lines []string) string {
	result := ""
	for i, l := range lines {
		if i > 0 {
			result += "\n"
		}
		result += l
	}
	return result
}

func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based if crypto/rand fails
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (e *Engine) nextDeliveryProfileIDLocked(profile models.DeliveryProfile) string {
	base := models.Slugify(profile.Name)
	if base == "" {
		base = models.Slugify(profile.DriverType)
	}
	if base == "" {
		return generateID()
	}
	return e.uniqueDeliveryProfileIDLocked(base)
}

func (e *Engine) uniqueDeliveryProfileIDLocked(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return generateID()
	}
	exists := make(map[string]bool, len(e.deliveryProfiles))
	for _, existing := range e.deliveryProfiles {
		exists[existing.ID] = true
	}
	if !exists[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !exists[candidate] {
			return candidate
		}
	}
}

func fileHashAndModTime(path string) (string, time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", time.Time{}, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), info.ModTime(), nil
}

func (e *Engine) taskGenerationMatches(taskID string, generation int64) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.taskGenerationMatchesLocked(taskID, generation)
}

func (e *Engine) taskGenerationMatchesLocked(taskID string, generation int64) bool {
	if generation == 0 || e.taskGenerations[taskID] != generation {
		return false
	}
	for _, task := range e.tasks {
		if task.ID == taskID {
			return true
		}
	}
	return false
}

func logRunCleanup(label, runID string, cleanup models.RunCleanupDiagnostics) {
	log.Printf("[CronPlus] %s %s: process_group_terminated=%t process_group_force_killed=%t detached_processes_killed=%d run_dir_removed=%t",
		label,
		runID,
		cleanup.ProcessGroupTerminated,
		cleanup.ProcessGroupForceKilled,
		cleanup.DetachedProcessesKilled,
		cleanup.RunDirectoryRemoved,
	)
	if cleanup.OrphanScanError != "" {
		log.Printf("[CronPlus] Warning: run orphan scan failed for %s: %s", runID, cleanup.OrphanScanError)
	}
	if cleanup.RunDirectoryCleanupError != "" {
		log.Printf("[CronPlus] Warning: run directory cleanup failed for %s: %s", runID, cleanup.RunDirectoryCleanupError)
	}
}

func cloneTask(task *models.Task) *models.Task {
	if task == nil {
		return nil
	}
	copyTask := *task
	copyTask.Manifest = cloneManifest(task.Manifest)
	return &copyTask
}

func cloneManifest(m *models.ScriptManifest) *models.ScriptManifest {
	if m == nil {
		return nil
	}
	cp := *m
	if m.Runtime.IsolatedRun != nil {
		isolatedRun := *m.Runtime.IsolatedRun
		cp.Runtime.IsolatedRun = &isolatedRun
	}
	cp.Runtime.Env = cloneEnvVarMap(m.Runtime.Env)
	cp.Delivery.Profiles = append([]string(nil), m.Delivery.Profiles...)
	cp.Delivery.SendOn = append([]string(nil), m.Delivery.SendOn...)
	cp.Delivery.InlineProfiles = make([]models.InlineDeliveryProfile, len(m.Delivery.InlineProfiles))
	for i, profile := range m.Delivery.InlineProfiles {
		cp.Delivery.InlineProfiles[i] = profile
		cp.Delivery.InlineProfiles[i].Config = cloneStringMap(profile.Config)
	}
	cp.UI.Tags = append([]string(nil), m.UI.Tags...)
	return &cp
}

func cloneEnvVarMap(in map[string]models.EnvVar) map[string]models.EnvVar {
	if in == nil {
		return nil
	}
	out := make(map[string]models.EnvVar, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneRunHistoryMap(in map[string][]models.RunRecord) map[string][]models.RunRecord {
	out := make(map[string][]models.RunRecord, len(in))
	for taskID, records := range in {
		out[taskID] = cloneRunRecords(records)
	}
	return out
}

func cloneRunRecords(records []models.RunRecord) []models.RunRecord {
	if records == nil {
		return nil
	}
	out := make([]models.RunRecord, len(records))
	for i, record := range records {
		out[i] = cloneRunRecord(record)
	}
	return out
}

func cloneRunRecord(record models.RunRecord) models.RunRecord {
	record.DeliveryResults = append([]models.DeliveryResult(nil), record.DeliveryResults...)
	if record.Outcome.ParsedResult != nil {
		parsed := *record.Outcome.ParsedResult
		parsed.Fields = cloneAnyMap(record.Outcome.ParsedResult.Fields)
		parsed.Data = cloneAnyValue(record.Outcome.ParsedResult.Data)
		if record.Outcome.ParsedResult.Deliverable != nil {
			deliverable := *record.Outcome.ParsedResult.Deliverable
			parsed.Deliverable = &deliverable
		}
		record.Outcome.ParsedResult = &parsed
	}
	return record
}

func cloneDeliveryProfiles(profiles []models.DeliveryProfile) []models.DeliveryProfile {
	if profiles == nil {
		return nil
	}
	out := make([]models.DeliveryProfile, len(profiles))
	for i, profile := range profiles {
		out[i] = cloneDeliveryProfile(profile)
	}
	return out
}

func cloneDeliveryProfile(profile models.DeliveryProfile) models.DeliveryProfile {
	profile.Config = cloneStringMap(profile.Config)
	profile.AuthorizedChatIDs = append([]string(nil), profile.AuthorizedChatIDs...)
	return profile
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneAnyValue(value)
	}
	return out
}

func cloneAnyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneAnyValue(item)
		}
		return out
	default:
		return typed
	}
}
