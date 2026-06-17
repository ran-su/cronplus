package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ran-su/cronplus/internal/core"
	"github.com/ran-su/cronplus/internal/models"
)

type runRecordWithDiagnosis struct {
	models.RunRecord
	Diagnosis core.RunDiagnosis `json:"diagnosis"`
}

func handleDependencyHealth(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report, err := engine.DependencyHealth(r.PathValue("id"))
		if writeEngineError(w, err, http.StatusBadRequest, "dependency_health_failed") {
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func handleTaskDependents(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report, err := engine.TaskDependents(r.PathValue("id"))
		if writeEngineError(w, err, http.StatusBadRequest, "dependents_failed") {
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func handleTaskEnvironment(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		detail, err := engine.TaskEnvironment(r.PathValue("id"))
		if writeEngineError(w, err, http.StatusBadRequest, "environment_failed") {
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

func handleRebuildTaskEnvironment(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		detail, err := engine.RebuildTaskEnvironment(r.PathValue("id"))
		if writeEngineError(w, err, http.StatusBadRequest, "environment_rebuild_failed") {
			return
		}
		writeJSON(w, http.StatusAccepted, detail)
	}
}

func handleGetActiveRuns(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"activeRuns": engine.ActiveRuns()})
	}
}

func handleGetActiveRun(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, err := engine.ActiveRun(r.PathValue("runId"))
		if writeEngineError(w, err, http.StatusBadRequest, "active_run_failed") {
			return
		}
		writeJSON(w, http.StatusOK, info)
	}
}

func handleCancelRun(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reason string `json:"reason"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			if err := readJSON(r, &body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body.")
				return
			}
		}
		info, err := engine.CancelRun(r.PathValue("runId"), body.Reason)
		if writeEngineError(w, err, http.StatusBadRequest, "cancel_run_failed") {
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"ok":        true,
			"activeRun": info,
		})
	}
}

func handleGetRetention(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, engine.RetentionPolicy())
	}
}

func handleUpdateRetention(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxRunsPerTask *int `json:"maxRunsPerTask"`
			MaxRunAgeDays  *int `json:"maxRunAgeDays"`
			MaxRunOutputKB *int `json:"maxRunOutputKB"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body.")
			return
		}
		settings := engine.Settings()
		maxRunsPerTask := settings.MaxRunsPerTask
		maxRunAgeDays := settings.MaxRunAgeDays
		maxRunOutputKB := settings.MaxRunOutputKB
		if body.MaxRunsPerTask != nil {
			maxRunsPerTask = *body.MaxRunsPerTask
		}
		if body.MaxRunAgeDays != nil {
			maxRunAgeDays = *body.MaxRunAgeDays
		}
		if body.MaxRunOutputKB != nil {
			maxRunOutputKB = *body.MaxRunOutputKB
		}
		report := engine.UpdateRetentionPolicy(maxRunsPerTask, maxRunAgeDays, maxRunOutputKB)
		if !persistOrError(w, engine) {
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func handleCleanupRetention(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report := engine.CleanupRetentionNow()
		if !persistOrError(w, engine) {
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func handleSchedulePreview(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TaskID      string `json:"taskID"`
			TaskIDSnake string `json:"task_id"`
			Expression  string `json:"expression"`
			Timezone    string `json:"timezone"`
			After       string `json:"after"`
			Count       int    `json:"count"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body.")
			return
		}

		taskID := strings.TrimSpace(body.TaskID)
		if taskID == "" {
			taskID = strings.TrimSpace(body.TaskIDSnake)
		}
		expression := strings.TrimSpace(body.Expression)
		timezone := strings.TrimSpace(body.Timezone)
		if taskID != "" {
			task := engine.Task(taskID)
			if task == nil {
				writeError(w, http.StatusNotFound, "task_not_found", "No task with ID "+taskID)
				return
			}
			if task.Manifest == nil {
				writeError(w, http.StatusBadRequest, "task_no_manifest", core.ErrTaskNoManifest.Error())
				return
			}
			if expression == "" {
				expression = task.Manifest.Schedule.Expression
			}
			if timezone == "" {
				timezone = task.Manifest.Schedule.Timezone
			}
		}
		if expression == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "Request body must include expression or taskID.")
			return
		}
		if timezone == "" {
			timezone = "UTC"
		}

		count := body.Count
		if count <= 0 {
			count = 10
		}
		if count > 50 {
			count = 50
		}
		after := time.Now()
		if strings.TrimSpace(body.After) != "" {
			parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(body.After))
			if err != nil {
				writeJSON(w, http.StatusOK, models.SchedulePreview{
					Expression: expression,
					Timezone:   timezone,
					After:      after,
					Count:      count,
					Valid:      false,
					Message:    "after must be an RFC3339 timestamp.",
				})
				return
			}
			after = parsed
		}

		preview := models.SchedulePreview{
			Expression: expression,
			Timezone:   timezone,
			After:      after,
			Count:      count,
			Valid:      true,
		}
		if _, err := core.ParseCron(expression); err != nil {
			preview.Valid = false
			preview.Message = err.Error()
			writeJSON(w, http.StatusOK, preview)
			return
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			preview.Valid = false
			preview.Message = err.Error()
			writeJSON(w, http.StatusOK, preview)
			return
		}
		manifest := &models.ScriptManifest{
			Schedule: models.ScheduleSection{
				Type:       "cron",
				Expression: expression,
				Timezone:   timezone,
			},
		}
		preview.Runs = core.NextRunTimesForManifest(manifest, count, after)
		if len(preview.Runs) == 0 {
			preview.Valid = false
			preview.Message = "No upcoming runs could be produced for this schedule."
		}
		writeJSON(w, http.StatusOK, preview)
	}
}

func handleGetHealth(engine *core.Engine, info ServerInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tasks := engine.Tasks()
		report := models.HealthReport{
			GeneratedAt:    time.Now(),
			Status:         "healthy",
			Summary:        "CronPlus is running normally.",
			Retention:      engine.RetentionPolicy(),
			ActiveRuns:     engine.ActiveRuns(),
			AttentionItems: buildAttentionItems(engine, tasks),
		}
		report.Tasks.Total = len(tasks)

		var taskPackageUsages []models.DirectoryUsage
		var environmentUsages []models.DirectoryUsage
		seenPackagePaths := map[string]bool{}
		seenEnvironmentPaths := map[string]bool{}
		for _, task := range tasks {
			if task.Enabled {
				report.Tasks.Enabled++
			}
			if task.PackageDir != "" && !seenPackagePaths[task.PackageDir] {
				seenPackagePaths[task.PackageDir] = true
				taskPackageUsages = append(taskPackageUsages, core.DirectoryUsage(task.PackageDir))
			}
			for _, run := range engine.RunHistory(task.ID) {
				report.Runs.Total++
				if models.RunStatusFromOutcome(run.Outcome) == "failure" && time.Since(run.FinishedAt) < 24*time.Hour {
					report.Runs.RecentFailures++
				}
			}
			switch task.EnvironmentSetup.State {
			case "pending":
				report.Environments.Pending++
			case "failed":
				report.Environments.Failed++
			}
			if task.Manifest != nil {
				switch task.Manifest.Runtime.Environment.Strategy {
				case "managed_venv":
					report.Environments.Managed++
				case "venv_path":
					report.Environments.CustomVenv++
				}
			}
			envDetail, err := engine.TaskEnvironment(task.ID)
			if err == nil && envDetail.Usage.Path != "" && !seenEnvironmentPaths[envDetail.Usage.Path] {
				seenEnvironmentPaths[envDetail.Usage.Path] = true
				environmentUsages = append(environmentUsages, envDetail.Usage)
				report.Environments.TotalBytes += envDetail.Usage.Bytes
				if envDetail.Usage.Error != "" {
					report.Environments.UnknownSizes++
				}
			}
		}
		report.Tasks.Disabled = report.Tasks.Total - report.Tasks.Enabled
		report.Storage.StateFile = core.DirectoryUsage(info.StatePath)
		report.Storage.ConfigDir = core.DirectoryUsage(info.ConfigDir)
		report.Storage.TaskPackages = sumDirectoryUsage(taskPackageUsages)
		report.Storage.Environments = sumDirectoryUsage(environmentUsages)

		if report.Environments.Failed > 0 || report.Runs.RecentFailures > 0 || hasDangerAttention(report.AttentionItems) {
			report.Status = "attention"
			report.Summary = "CronPlus has failed runs, failed environments, or critical attention items."
		} else if report.Environments.Pending > 0 || len(report.AttentionItems) > 0 {
			report.Status = "warning"
			report.Summary = "CronPlus is running, with items to review."
		}

		writeJSON(w, http.StatusOK, struct {
			models.HealthReport
			Version string     `json:"version"`
			Server  ServerInfo `json:"server"`
		}{
			HealthReport: report,
			Version:      info.Version,
			Server:       info,
		})
	}
}

func filterAndAnnotateRuns(task *models.Task, runs []models.RunRecord, r *http.Request) []runRecordWithDiagnosis {
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	triggerFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("trigger")))
	deliveryFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("deliveryStatus")))
	if deliveryFilter == "" {
		deliveryFilter = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("delivery_status")))
	}
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 0)

	result := make([]runRecordWithDiagnosis, 0, len(runs))
	for _, run := range runs {
		status := models.RunStatusFromOutcome(run.Outcome)
		if statusFilter != "" && status != models.NormalizeRunStatus(statusFilter) {
			continue
		}
		if triggerFilter != "" && strings.ToLower(run.Trigger) != triggerFilter {
			continue
		}
		if deliveryFilter != "" && !runHasDeliveryStatus(run, deliveryFilter) {
			continue
		}
		diagnosis := core.DiagnoseRun(task, &run)
		if search != "" && !runMatchesSearch(run, diagnosis, search) {
			continue
		}
		result = append(result, runRecordWithDiagnosis{
			RunRecord: run,
			Diagnosis: diagnosis,
		})
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func runHasDeliveryStatus(run models.RunRecord, status string) bool {
	status = normalizeDeliveryFilterStatus(status)
	if status == "" {
		return true
	}
	return runDeliveryAggregateStatus(run) == status
}

func normalizeDeliveryFilterStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "failure" {
		return "failed"
	}
	return status
}

func runDeliveryAggregateStatus(run models.RunRecord) string {
	if len(run.DeliveryResults) == 0 {
		return "none"
	}
	var sent, skipped int
	for _, delivery := range run.DeliveryResults {
		switch normalizeDeliveryFilterStatus(delivery.Status) {
		case "failed":
			return "failed"
		case "success":
			sent++
		case "skipped":
			skipped++
		}
	}
	if sent > 0 {
		return "success"
	}
	if skipped > 0 {
		return "skipped"
	}
	return "none"
}

func runMatchesSearch(run models.RunRecord, diagnosis core.RunDiagnosis, search string) bool {
	fields := []string{
		run.ID,
		run.Trigger,
		diagnosis.Status,
		diagnosis.Summary,
	}
	if run.Outcome.ParsedResult != nil {
		fields = append(fields, run.Outcome.ParsedResult.Status, run.Outcome.ParsedResult.Summary)
	}
	for _, delivery := range run.DeliveryResults {
		fields = append(fields, delivery.ProfileName, delivery.Status, delivery.Error)
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), search) {
			return true
		}
	}
	return false
}

func parsePositiveInt(value string, fallback int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func sumDirectoryUsage(usages []models.DirectoryUsage) models.DirectoryUsage {
	var total models.DirectoryUsage
	for _, usage := range usages {
		if total.Path == "" && len(usages) == 1 {
			total.Path = usage.Path
		}
		total.Exists = total.Exists || usage.Exists
		total.Bytes += usage.Bytes
		total.Files += usage.Files
		total.Directories += usage.Directories
		if total.Error == "" {
			total.Error = usage.Error
		}
	}
	return total
}

func hasDangerAttention(items []map[string]any) bool {
	for _, item := range items {
		if severity, _ := item["severity"].(string); severity == "danger" {
			return true
		}
	}
	return false
}
