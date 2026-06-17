package core

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

func (e *Engine) DependencyHealth(taskID string) (models.DependencyHealthReport, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	task := e.taskByIDLocked(taskID)
	if task == nil {
		return models.DependencyHealthReport{}, taskNotFoundError(taskID)
	}
	report := models.DependencyHealthReport{
		TaskID:   task.ID,
		TaskName: task.DisplayName,
		TaskSlug: task.Slug(),
		Status:   "none",
		Summary:  "This task does not declare task dependencies.",
	}
	if task.Manifest == nil || len(task.Manifest.Dependencies.Tasks) == 0 {
		return report, nil
	}

	now := time.Now()
	report.Dependencies = make([]models.DependencyHealthItem, 0, len(task.Manifest.Dependencies.Tasks))
	unhealthy := 0
	for i, dependency := range task.Manifest.Dependencies.Tasks {
		item := e.evaluateTaskDependencyHealthLocked(i, dependency, now)
		if item.Status != "healthy" {
			unhealthy++
		}
		report.Dependencies = append(report.Dependencies, item)
	}
	if unhealthy > 0 {
		report.Status = "unhealthy"
		report.Summary = fmt.Sprintf("%d of %d dependencies need attention.", unhealthy, len(report.Dependencies))
	} else {
		report.Status = "healthy"
		report.Summary = fmt.Sprintf("%d dependencies are healthy.", len(report.Dependencies))
	}
	return report, nil
}

func (e *Engine) TaskDependents(taskID string) (models.TaskDependentsReport, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	task := e.taskByIDLocked(taskID)
	if task == nil {
		return models.TaskDependentsReport{}, taskNotFoundError(taskID)
	}
	report := models.TaskDependentsReport{
		TaskID:   task.ID,
		TaskName: task.DisplayName,
		TaskSlug: task.Slug(),
	}
	for _, candidate := range e.tasks {
		if candidate == nil || candidate.Manifest == nil || candidate.ID == task.ID {
			continue
		}
		for i, dependency := range candidate.Manifest.Dependencies.Tasks {
			if !dependencyReferencesTask(dependency, task) {
				continue
			}
			report.Dependents = append(report.Dependents, models.TaskDependent{
				TaskID:         candidate.ID,
				TaskName:       candidate.DisplayName,
				TaskSlug:       candidate.Slug(),
				Index:          i,
				Selector:       dependencySelector(dependency),
				RequiredStatus: normalizedDependencyRequiredStatus(dependency),
				MaxAgeSeconds:  dependency.MaxAgeSeconds,
				OnUnhealthy:    normalizedDependencyOnUnhealthy(dependency),
			})
		}
	}
	sort.Slice(report.Dependents, func(i, j int) bool {
		if report.Dependents[i].TaskName == report.Dependents[j].TaskName {
			return report.Dependents[i].Index < report.Dependents[j].Index
		}
		return report.Dependents[i].TaskName < report.Dependents[j].TaskName
	})
	return report, nil
}

func (e *Engine) TaskEnvironment(taskID string) (models.TaskEnvironmentDetail, error) {
	task := e.Task(taskID)
	if task == nil {
		return models.TaskEnvironmentDetail{}, taskNotFoundError(taskID)
	}
	return e.taskEnvironmentDetail(task), nil
}

func (e *Engine) RebuildTaskEnvironment(taskID string) (models.TaskEnvironmentDetail, error) {
	e.mu.Lock()
	task := e.taskByIDLocked(taskID)
	if task == nil {
		e.mu.Unlock()
		return models.TaskEnvironmentDetail{}, taskNotFoundError(taskID)
	}
	if task.Manifest == nil {
		e.mu.Unlock()
		return models.TaskEnvironmentDetail{}, ErrTaskNoManifest
	}
	if !environmentSetupRequired(task.Manifest) {
		e.mu.Unlock()
		return models.TaskEnvironmentDetail{}, ErrEnvironmentNotRebuildable
	}
	if e.activeRuns[taskID] {
		e.mu.Unlock()
		return models.TaskEnvironmentDetail{}, ErrTaskAlreadyRunning
	}
	if task.EnvironmentSetup.State == "pending" {
		e.mu.Unlock()
		return models.TaskEnvironmentDetail{}, ErrEnvironmentSetupPending
	}

	now := time.Now()
	task.EnvironmentSetup = models.EnvironmentSetupStatus{
		State:     "pending",
		Message:   "Rebuilding Python environment...",
		StartedAt: now,
	}
	setupGen, setupManifest, setupLock, _ := e.beginEnvironmentSetupLocked(task.ID, task.Manifest)
	manifestDir := filepath.Dir(task.ManifestPath)
	envPath := managedVenvPath(manifestDir)
	taskCopy := cloneTask(task)
	e.mu.Unlock()

	e.Broker.Publish("task_updated", taskCopy)
	if err := e.PersistState(); err != nil {
		logPersistWarning("failed to persist environment rebuild state", err)
	}

	go e.rebuildEnvironmentSetup(taskCopy.ID, setupGen, setupManifest, manifestDir, envPath, setupLock)
	return e.taskEnvironmentDetail(taskCopy), nil
}

func (e *Engine) ActiveRuns() []models.ActiveRunInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	activeRuns := make([]models.ActiveRunInfo, 0, len(e.activeRunDetails))
	now := time.Now()
	for _, info := range e.activeRunDetails {
		if controller := e.activeRunControllers[info.RunID]; controller != nil {
			applyActiveRunController(&info, controller, now)
		} else {
			info.ElapsedMs = elapsedMs(info.StartedAt, now)
		}
		activeRuns = append(activeRuns, info)
	}
	sort.Slice(activeRuns, func(i, j int) bool {
		return activeRuns[i].StartedAt.Before(activeRuns[j].StartedAt)
	})
	return activeRuns
}

func (e *Engine) ActiveRun(runID string) (*models.ActiveRunInfo, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	info, ok := e.activeRunDetails[runID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRunNotActive, runID)
	}
	if controller := e.activeRunControllers[runID]; controller != nil {
		applyActiveRunController(&info, controller, time.Now())
	} else {
		info.ElapsedMs = elapsedMs(info.StartedAt, time.Now())
	}
	return &info, nil
}

func (e *Engine) CancelRun(runID, reason string) (models.ActiveRunInfo, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Canceled by user request."
	}
	e.mu.Lock()
	info, ok := e.activeRunDetails[runID]
	if !ok {
		e.mu.Unlock()
		return models.ActiveRunInfo{}, fmt.Errorf("%w: %s", ErrRunNotActive, runID)
	}
	if e.activeRunControllers == nil {
		e.activeRunControllers = make(map[string]*activeRunController)
	}
	controller := e.activeRunControllers[runID]
	if controller == nil {
		controller = &activeRunController{}
		e.activeRunControllers[runID] = controller
	}
	now := time.Now()
	controller.cancelRequested = true
	controller.cancelReason = reason
	controller.cancelRequestedAt = &now
	applyActiveRunController(&info, controller, now)
	e.activeRunDetails[runID] = info
	cancel := controller.cancel
	e.mu.Unlock()

	if err := e.PersistState(); err != nil {
		logPersistWarning("failed to persist active run cancellation request", err)
	}

	if cancel == nil {
		cleanup := cleanupPersistedRunProcess(info, 5*time.Second)
		logRunCleanup("Active run cancellation cleanup", info.RunID, cleanup)
		e.mu.Lock()
		delete(e.activeRunDetails, runID)
		delete(e.activeRunControllers, runID)
		delete(e.activeRuns, info.TaskID)
		e.mu.Unlock()
		if err := e.PersistState(); err != nil {
			logPersistWarning("failed to persist active run cancellation cleanup", err)
		}
		return info, nil
	}
	cancel(reason)
	return info, nil
}

func (e *Engine) taskEnvironmentDetail(task *models.Task) models.TaskEnvironmentDetail {
	detail := models.TaskEnvironmentDetail{
		TaskID:   task.ID,
		TaskName: task.DisplayName,
		TaskSlug: task.Slug(),
		Setup:    task.EnvironmentSetup,
		Running:  e.IsRunning(task.ID),
	}
	if task.Manifest == nil {
		detail.Message = "Task manifest is not loaded."
		return detail
	}

	manifestDir := filepath.Dir(task.ManifestPath)
	env := task.Manifest.Runtime.Environment
	strategy := strings.TrimSpace(env.Strategy)
	if strategy == "" {
		strategy = "system"
	}
	detail.Strategy = strategy
	detail.PythonExecutable = resolvePython(task.Manifest, manifestDir)
	detail.PythonBase = env.PythonInterpreter
	detail.RequirementsFile = resolveTaskPath(manifestDir, env.RequirementsFile)
	detail.EnvFile = resolveTaskPath(manifestDir, task.Manifest.Runtime.EnvFile)

	switch strategy {
	case "managed_venv":
		detail.VenvPath = managedVenvPath(manifestDir)
		detail.CanRebuild = true
		detail.Usage = DirectoryUsage(detail.VenvPath)
	case "venv_path":
		detail.VenvPath = resolveTaskPath(manifestDir, env.VenvPath)
		detail.Usage = DirectoryUsage(detail.VenvPath)
	default:
		detail.Usage = models.DirectoryUsage{}
	}
	return detail
}

func (e *Engine) rebuildEnvironmentSetup(taskID string, generation int64, m *models.ScriptManifest, manifestDir, envPath string, setupLock *sync.Mutex) {
	if setupLock != nil {
		setupLock.Lock()
	}
	if !e.environmentSetupGenerationMatches(taskID, generation) {
		if setupLock != nil {
			setupLock.Unlock()
		}
		return
	}
	removeErr := os.RemoveAll(envPath)
	if setupLock != nil {
		setupLock.Unlock()
	}
	if removeErr != nil {
		e.finishEnvironmentSetup(taskID, generation, removeErr)
		return
	}
	e.runEnvironmentSetup(taskID, generation, m, manifestDir, setupLock)
}

func (e *Engine) finishEnvironmentSetup(taskID string, generation int64, err error) {
	e.mu.Lock()
	if !e.environmentSetupGenerationMatchesLocked(taskID, generation) {
		e.mu.Unlock()
		return
	}
	var taskCopy *models.Task
	for i := range e.tasks {
		if e.tasks[i].ID != taskID {
			continue
		}
		now := time.Now()
		startedAt := e.tasks[i].EnvironmentSetup.StartedAt
		if startedAt.IsZero() {
			startedAt = now
		}
		if err != nil {
			e.tasks[i].EnvironmentSetup = models.EnvironmentSetupStatus{
				State:      "failed",
				Message:    err.Error(),
				StartedAt:  startedAt,
				FinishedAt: now,
			}
		} else {
			e.tasks[i].EnvironmentSetup = models.EnvironmentSetupStatus{
				State:      "ready",
				StartedAt:  startedAt,
				FinishedAt: now,
			}
		}
		taskCopy = cloneTask(e.tasks[i])
		break
	}
	e.mu.Unlock()
	if taskCopy == nil {
		return
	}
	e.Broker.Publish("task_updated", taskCopy)
	if persistErr := e.PersistState(); persistErr != nil {
		logPersistWarning("failed to persist environment setup state", persistErr)
	}
}

func (e *Engine) taskByIDLocked(taskID string) *models.Task {
	for _, task := range e.tasks {
		if task.ID == taskID {
			return task
		}
	}
	return nil
}

func (e *Engine) evaluateTaskDependencyHealthLocked(index int, dependency models.TaskDependency, now time.Time) models.DependencyHealthItem {
	requiredStatus := normalizedDependencyRequiredStatus(dependency)
	onUnhealthy := normalizedDependencyOnUnhealthy(dependency)
	selector := dependencySelector(dependency)
	item := models.DependencyHealthItem{
		Index:          index,
		Selector:       selector,
		Config:         dependency,
		RequiredStatus: requiredStatus,
		MaxAgeSeconds:  dependency.MaxAgeSeconds,
		OnUnhealthy:    onUnhealthy,
		Status:         "healthy",
	}

	target, ambiguous := e.findDependencyTargetLocked(dependency)
	if ambiguous {
		item.Status = "unhealthy"
		item.Ambiguous = true
		item.Reason = fmt.Sprintf("Dependency %s matches multiple tasks.", selector)
		return item
	}
	if target == nil {
		item.Status = "unhealthy"
		item.Reason = fmt.Sprintf("Dependency %s was not found.", selector)
		return item
	}

	item.TargetID = target.ID
	item.TargetName = target.DisplayName
	item.TargetSlug = target.Slug()
	history := e.runHistory[target.ID]
	if len(history) == 0 {
		item.Status = "unhealthy"
		item.Reason = fmt.Sprintf("Dependency %s has no completed runs.", selector)
		return item
	}

	last := history[0]
	lastStatus := models.RunStatusFromOutcome(last.Outcome)
	item.LastRunID = last.ID
	item.LastStatus = lastStatus
	if !last.FinishedAt.IsZero() {
		finishedAt := last.FinishedAt
		item.LastFinishedAt = &finishedAt
		age := now.Sub(last.FinishedAt)
		if age >= 0 {
			item.LastAgeSeconds = int64(age.Seconds())
		}
	}
	if lastStatus != requiredStatus {
		item.Status = "unhealthy"
		item.Reason = fmt.Sprintf("Dependency %s latest status is %s; required %s.", selector, lastStatus, requiredStatus)
		return item
	}
	if dependency.MaxAgeSeconds > 0 {
		maxAge := time.Duration(dependency.MaxAgeSeconds) * time.Second
		if last.FinishedAt.IsZero() {
			item.Status = "unhealthy"
			item.Reason = fmt.Sprintf("Dependency %s latest run has no finish time; required age <= %s.", selector, maxAge)
			return item
		}
		age := now.Sub(last.FinishedAt)
		if age > maxAge {
			item.Status = "unhealthy"
			item.Reason = fmt.Sprintf("Dependency %s latest %s is stale; age %s exceeds max %s.", selector, requiredStatus, age.Round(time.Second), maxAge)
			return item
		}
	}
	return item
}

func dependencyHealthGateResult(item models.DependencyHealthItem) *dependencyGateResult {
	if item.Status == "healthy" {
		return nil
	}
	data := map[string]any{
		"dependencyIndex": item.Index,
		"selector":        item.Selector,
		"requiredStatus":  item.RequiredStatus,
		"maxAgeSeconds":   item.MaxAgeSeconds,
		"onUnhealthy":     item.OnUnhealthy,
	}
	if strings.TrimSpace(item.Config.ID) != "" {
		data["dependencyID"] = strings.TrimSpace(item.Config.ID)
	}
	if strings.TrimSpace(item.Config.Slug) != "" {
		data["dependencySlug"] = strings.TrimSpace(item.Config.Slug)
	}
	if item.TargetID != "" {
		data["targetID"] = item.TargetID
		data["targetName"] = item.TargetName
	}
	if item.LastStatus != "" {
		data["lastStatus"] = item.LastStatus
	}
	if item.LastFinishedAt != nil {
		data["lastFinishedAt"] = *item.LastFinishedAt
	}
	if item.LastAgeSeconds > 0 {
		data["lastAgeSeconds"] = item.LastAgeSeconds
	}
	return unhealthyDependencyResult(item.OnUnhealthy, item.Reason, data)
}

func dependencyReferencesTask(dependency models.TaskDependency, task *models.Task) bool {
	if strings.TrimSpace(dependency.ID) != "" {
		return strings.TrimSpace(dependency.ID) == task.ID
	}
	if strings.TrimSpace(dependency.Slug) != "" {
		return strings.TrimSpace(dependency.Slug) == task.Slug()
	}
	return false
}

func normalizedDependencyRequiredStatus(dependency models.TaskDependency) string {
	requiredStatus := models.NormalizeRunStatus(dependency.RequireStatus)
	if requiredStatus == "" {
		requiredStatus = "success"
	}
	return requiredStatus
}

func normalizedDependencyOnUnhealthy(dependency models.TaskDependency) string {
	onUnhealthy := strings.ToLower(strings.TrimSpace(dependency.OnUnhealthy))
	if onUnhealthy == "" {
		onUnhealthy = "skip"
	}
	return onUnhealthy
}

func managedVenvPath(manifestDir string) string {
	return filepath.Join(manifestDir, ".cronplus-venv")
}

func resolveTaskPath(manifestDir, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(manifestDir, path)
}

func DirectoryUsage(path string) models.DirectoryUsage {
	path = strings.TrimSpace(path)
	if path == "" {
		return models.DirectoryUsage{}
	}
	usage := models.DirectoryUsage{Path: path}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return usage
		}
		usage.Error = err.Error()
		return usage
	}
	usage.Exists = true
	if !info.IsDir() {
		usage.Bytes = info.Size()
		usage.Files = 1
		return usage
	}

	err = filepath.WalkDir(path, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if usage.Error == "" {
				usage.Error = walkErr.Error()
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if usage.Error == "" {
				usage.Error = err.Error()
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			usage.Files++
			usage.Bytes += info.Size()
			return nil
		}
		if d.IsDir() {
			if p != path {
				usage.Directories++
			}
			return nil
		}
		usage.Files++
		usage.Bytes += info.Size()
		return nil
	})
	if err != nil && usage.Error == "" {
		usage.Error = err.Error()
	}
	return usage
}

func mergeDirectoryUsage(usages ...models.DirectoryUsage) models.DirectoryUsage {
	var merged models.DirectoryUsage
	for _, usage := range usages {
		if usage.Path != "" && merged.Path == "" {
			merged.Path = usage.Path
		}
		merged.Exists = merged.Exists || usage.Exists
		merged.Bytes += usage.Bytes
		merged.Files += usage.Files
		merged.Directories += usage.Directories
		if merged.Error == "" {
			merged.Error = usage.Error
		}
	}
	return merged
}

func logPersistWarning(message string, err error) {
	if err != nil {
		log.Printf("[CronPlus] Warning: %s: %v", message, err)
	}
}
