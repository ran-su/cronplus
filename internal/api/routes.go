package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ran-su/cronplus/internal/core"
	"github.com/ran-su/cronplus/internal/models"
)

// Routes registers all API endpoints.
func Routes(mux *http.ServeMux, engine *core.Engine, version string) {
	RoutesWithInfo(mux, engine, ServerInfo{Version: version})
}

// RoutesWithInfo registers all API endpoints with daemon metadata.
func RoutesWithInfo(mux *http.ServeMux, engine *core.Engine, info ServerInfo) {
	if info.Version == "" {
		info.Version = "dev"
	}
	if info.MaxConcurrentRuns == 0 {
		info.MaxConcurrentRuns = engine.MaxConcurrentRuns()
	}
	mux.HandleFunc("GET /api/status", handleGetStatus(engine, info))
	mux.HandleFunc("POST /api/system/pick-directory", handlePickDirectory(pickDirectory))
	mux.HandleFunc("GET /api/tasks", handleGetTasks(engine))
	mux.HandleFunc("POST /api/tasks/check", handleCheckTaskPackage(engine))
	mux.HandleFunc("GET /api/tasks/{id}", handleGetTask(engine))
	mux.HandleFunc("POST /api/tasks/import", handleImportTask(engine))
	mux.HandleFunc("DELETE /api/tasks/{id}", handleDeleteTask(engine))
	mux.HandleFunc("POST /api/tasks/{id}/reload", handleReloadTask(engine))
	mux.HandleFunc("POST /api/tasks/{id}/check", handleCheckImportedTask(engine))
	mux.HandleFunc("POST /api/tasks/{id}/run", handleRunTask(engine))
	mux.HandleFunc("GET /api/tasks/{id}/delivery-preview", handleDeliveryPreview(engine))
	mux.HandleFunc("POST /api/tasks/{id}/enable", handleSetTaskEnabled(engine, true))
	mux.HandleFunc("POST /api/tasks/{id}/disable", handleSetTaskEnabled(engine, false))
	mux.HandleFunc("GET /api/tasks/{id}/runs", handleGetTaskRuns(engine))
	mux.HandleFunc("GET /api/tasks/{id}/runs/{runId}", handleGetTaskRun(engine))
	mux.HandleFunc("GET /api/deliveries", handleGetDeliveries(engine))
	mux.HandleFunc("POST /api/deliveries", handleCreateDelivery(engine))
	mux.HandleFunc("PUT /api/deliveries/{id}", handleUpdateDelivery(engine))
	mux.HandleFunc("POST /api/deliveries/{id}/commands/enable", handleSetDeliveryCommands(engine, true))
	mux.HandleFunc("POST /api/deliveries/{id}/commands/disable", handleSetDeliveryCommands(engine, false))
	mux.HandleFunc("POST /api/deliveries/{id}/test", handleTestDelivery(engine))
	mux.HandleFunc("DELETE /api/deliveries/{id}", handleDeleteDelivery(engine))
	mux.HandleFunc("GET /api/commands", handleGetCommands(engine))
	mux.HandleFunc("DELETE /api/commands", handleClearCommands(engine))
	mux.HandleFunc("GET /api/events", SSEHandler(engine.Broker))
}

// --- Status ---

func handleGetStatus(engine *core.Engine, info ServerInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tasks := engine.Tasks()
		enabled := 0
		var recentFailures int
		var nextRun *map[string]any
		var nextRunTime *time.Time

		for _, t := range tasks {
			if t.Enabled {
				enabled++
			}
			runs := engine.RunHistory(t.ID)
			if len(runs) > 0 {
				latest := runs[0]
				if models.RunStatusFromOutcome(latest.Outcome) == "failure" && time.Since(latest.FinishedAt) < 24*time.Hour {
					recentFailures++
				}
			}
			if t.Enabled {
				if nr := engine.NextRunTime(t); nr != nil {
					if nextRunTime == nil || nr.Before(*nextRunTime) {
						m := map[string]any{
							"taskName":    t.DisplayName,
							"taskID":      t.ID,
							"scheduledAt": nr.Format(time.RFC3339),
						}
						nextRun = &m
						nextRunTime = nr
					}
				}
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"version": info.Version,
			"server":  info,
			"tasks": map[string]int{
				"total":    len(tasks),
				"enabled":  enabled,
				"disabled": len(tasks) - enabled,
			},
			"nextRun":        nextRun,
			"recentFailures": recentFailures,
			"attentionItems": buildAttentionItems(engine, tasks),
		})
	}
}

// --- Tasks ---

func handleGetTasks(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tasks := engine.Tasks()
		result := make([]map[string]any, len(tasks))
		for i, t := range tasks {
			lastRun := engine.RunHistory(t.ID)
			m := map[string]any{
				"id":         t.ID,
				"name":       t.DisplayName,
				"slug":       t.Slug(),
				"enabled":    t.Enabled,
				"packageDir": t.PackageDir,
				"running":    engine.IsRunning(t.ID),
			}
			if t.Manifest != nil {
				m["scheduleSummary"] = t.Manifest.Schedule.Expression
				m["description"] = t.Manifest.Script.Description
			}
			m["manifestStatus"] = engine.ManifestStatus(t)
			m["environmentSetup"] = t.EnvironmentSetup
			m["timeline"] = engine.TaskTimeline(t)
			if nr := engine.NextRunTime(t); nr != nil {
				m["nextRun"] = nr.Format(time.RFC3339)
			}
			m["nextRuns"] = formatTimes(engine.NextRunTimes(t, 5))
			if len(lastRun) > 0 {
				lr := lastRun[0]
				status := models.RunStatusFromOutcome(lr.Outcome)
				m["lastRun"] = map[string]any{
					"status":     status,
					"finishedAt": lr.FinishedAt.Format(time.RFC3339),
				}
				m["lastDiagnosis"] = core.DiagnoseRun(t, &lr)
			}
			result[i] = m
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": result})
	}
}

func handleGetTask(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		task := engine.Task(id)
		if task == nil {
			writeError(w, http.StatusNotFound, "task_not_found", "No task with ID "+id)
			return
		}
		m := map[string]any{
			"id":         task.ID,
			"name":       task.DisplayName,
			"slug":       task.Slug(),
			"enabled":    task.Enabled,
			"packageDir": task.PackageDir,
			"running":    engine.IsRunning(task.ID),
			"createdAt":  task.CreatedAt.Format(time.RFC3339),
		}
		if task.Manifest != nil {
			m["manifest"] = task.Manifest
			m["scheduleSummary"] = task.Manifest.Schedule.Expression
			m["description"] = task.Manifest.Script.Description
		}
		m["manifestStatus"] = engine.ManifestStatus(task)
		m["environmentSetup"] = task.EnvironmentSetup
		m["timeline"] = engine.TaskTimeline(task)
		if nr := engine.NextRunTime(task); nr != nil {
			m["nextRun"] = nr.Format(time.RFC3339)
		}
		m["nextRuns"] = formatTimes(engine.NextRunTimes(task, 5))
		writeJSON(w, http.StatusOK, m)
	}
}

func handleCheckTaskPackage(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Path) == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "Request body must include 'path'.")
			return
		}
		writeJSON(w, http.StatusOK, core.CheckTaskPackage(body.Path))
	}
}

func handleImportTask(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path    string `json:"path"`
			Enabled *bool  `json:"enabled,omitempty"`
		}
		if err := readJSON(r, &body); err != nil || body.Path == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "Request body must include 'path'.")
			return
		}

		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		task, err := engine.ImportTask(body.Path, enabled)
		if writeEngineError(w, err, http.StatusBadRequest, "import_failed") {
			return
		}

		if !persistOrError(w, engine) {
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":               task.ID,
			"name":             task.DisplayName,
			"environmentSetup": task.EnvironmentSetup,
		})
	}
}

func handleDeleteTask(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if writeEngineError(w, engine.RemoveTask(id), http.StatusBadRequest, "remove_failed") {
			return
		}
		if !persistOrError(w, engine) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func handleReloadTask(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		task, err := engine.ReloadTask(id)
		if writeEngineError(w, err, http.StatusBadRequest, "reload_failed") {
			return
		}
		if !persistOrError(w, engine) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":               task.ID,
			"name":             task.DisplayName,
			"environmentSetup": task.EnvironmentSetup,
		})
	}
}

func handleCheckImportedTask(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		task := engine.Task(id)
		if task == nil {
			writeError(w, http.StatusNotFound, "task_not_found", "No task with ID "+id)
			return
		}
		writeJSON(w, http.StatusOK, core.CheckTaskPackage(task.PackageDir))
	}
}

func handleRunTask(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		runID, err := engine.StartTaskRunWithID(id, "manual")
		if writeEngineError(w, err, http.StatusBadRequest, "run_failed") {
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{
			"taskID": id,
			"runID":  runID,
			"status": "started",
		})
	}
}

func handleDeliveryPreview(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		task := engine.Task(id)
		if task == nil {
			writeError(w, http.StatusNotFound, "task_not_found", "No task with ID "+id)
			return
		}
		if engine.DeliveryService == nil {
			writeError(w, http.StatusBadRequest, "delivery_unavailable", "Delivery service is not configured.")
			return
		}
		runs := engine.RunHistory(id)
		if len(runs) == 0 {
			writeError(w, http.StatusNotFound, "run_not_found", "No runs recorded for this task.")
			return
		}
		msg, err := engine.DeliveryService.PreviewMessage(task, &runs[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, "preview_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"taskID":  id,
			"runID":   runs[0].ID,
			"message": msg,
		})
	}
}

func handleSetTaskEnabled(engine *core.Engine, enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if writeEngineError(w, engine.SetTaskEnabled(id, enabled), http.StatusBadRequest, "set_enabled_failed") {
			return
		}
		if !persistOrError(w, engine) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// --- Runs ---

func handleGetTaskRuns(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("id")
		if engine.Task(taskID) == nil {
			writeError(w, http.StatusNotFound, "task_not_found", "No task with ID "+taskID)
			return
		}
		runs := engine.RunHistory(taskID)
		if runs == nil {
			runs = []models.RunRecord{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
	}
}

func handleGetTaskRun(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("id")
		runID := r.PathValue("runId")
		if engine.Task(taskID) == nil {
			writeError(w, http.StatusNotFound, "task_not_found", "No task with ID "+taskID)
			return
		}
		run := engine.RunRecord(taskID, runID)
		if run == nil {
			writeError(w, http.StatusNotFound, "run_not_found", "No run with ID "+runID)
			return
		}
		task := engine.Task(taskID)
		writeJSON(w, http.StatusOK, struct {
			models.RunRecord
			Diagnosis core.RunDiagnosis `json:"diagnosis"`
		}{
			RunRecord: *run,
			Diagnosis: core.DiagnoseRun(task, run),
		})
	}
}

// --- Deliveries ---

func handleGetDeliveries(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profiles := engine.DeliveryProfiles()
		tasks := engine.Tasks()
		// Redact sensitive config values
		safe := make([]map[string]any, len(profiles))
		for i, p := range profiles {
			safe[i] = map[string]any{
				"id":                     p.ID,
				"name":                   p.Name,
				"driverType":             p.DriverType,
				"enabled":                p.Enabled,
				"inboundCommandsEnabled": p.InboundCommandsEnabled,
				"hasConfig":              len(p.Config) > 0,
				"configFields": map[string]bool{
					"botToken": p.Config["bot_token"] != "",
					"chatID":   p.Config["chat_id"] != "",
				},
				"authorizedChatIDs": append([]string(nil), p.AuthorizedChatIDs...),
				"usedByTasks":       deliveryProfileUsage(p, tasks),
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"profiles": safe})
	}
}

func handleCreateDelivery(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p models.DeliveryProfile
		if err := readJSON(r, &p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body.")
			return
		}
		normalized, err := normalizeNewDeliveryProfile(p)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_delivery_profile", err.Error())
			return
		}
		id := engine.AddDeliveryProfile(normalized)
		if !persistOrError(w, engine) {
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": id})
	}
}

func handleUpdateDelivery(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var p models.DeliveryProfile
		if err := readJSON(r, &p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body.")
			return
		}
		existing, ok := deliveryProfileByID(engine, id)
		if !ok {
			writeError(w, http.StatusNotFound, "profile_not_found", "delivery profile not found: "+id)
			return
		}
		p.ID = id
		if err := validateUpdatedDeliveryProfile(existing, p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_delivery_profile", err.Error())
			return
		}
		if writeEngineError(w, engine.UpdateDeliveryProfile(p), http.StatusBadRequest, "update_failed") {
			return
		}
		if !persistOrError(w, engine) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func handleTestDelivery(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if engine.DeliveryService == nil {
			writeError(w, http.StatusBadRequest, "delivery_unavailable", "Delivery service is not configured.")
			return
		}

		var body struct {
			Message string `json:"message"`
		}
		_ = readJSON(r, &body)
		if body.Message == "" {
			body.Message = "CronPlus delivery test"
		}

		var profile *models.DeliveryProfile
		for _, p := range engine.DeliveryProfiles() {
			if p.ID == id {
				pCopy := p
				profile = &pCopy
				break
			}
		}
		if profile == nil {
			writeError(w, http.StatusNotFound, "profile_not_found", "No delivery profile with ID "+id)
			return
		}
		if err := engine.DeliveryService.SendTest(*profile, body.Message); err != nil {
			writeError(w, http.StatusBadRequest, "test_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func normalizeNewDeliveryProfile(p models.DeliveryProfile) (models.DeliveryProfile, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.DriverType = strings.TrimSpace(p.DriverType)
	if p.DriverType == "" {
		p.DriverType = "telegram"
	}
	if p.Name == "" {
		return p, fmt.Errorf("profile name is required")
	}
	if p.DriverType != "telegram" {
		return p, fmt.Errorf("unsupported delivery driver %q", p.DriverType)
	}
	if strings.TrimSpace(p.Config["bot_token"]) == "" {
		return p, fmt.Errorf("telegram bot token is required")
	}
	if strings.TrimSpace(p.Config["chat_id"]) == "" {
		return p, fmt.Errorf("telegram chat ID is required")
	}
	return p, nil
}

func validateUpdatedDeliveryProfile(existing, update models.DeliveryProfile) error {
	name := strings.TrimSpace(update.Name)
	if name == "" {
		name = existing.Name
	}
	if name == "" {
		return fmt.Errorf("profile name is required")
	}
	driverType := strings.TrimSpace(update.DriverType)
	if driverType == "" {
		driverType = existing.DriverType
	}
	if driverType != "telegram" {
		return fmt.Errorf("unsupported delivery driver %q", driverType)
	}
	config := mergedDeliveryConfigForValidation(existing.Config, update.Config)
	if strings.TrimSpace(config["bot_token"]) == "" {
		return fmt.Errorf("telegram bot token is required")
	}
	if strings.TrimSpace(config["chat_id"]) == "" {
		return fmt.Errorf("telegram chat ID is required")
	}
	return nil
}

func deliveryProfileByID(engine *core.Engine, id string) (models.DeliveryProfile, bool) {
	for _, profile := range engine.DeliveryProfiles() {
		if profile.ID == id {
			return profile, true
		}
	}
	return models.DeliveryProfile{}, false
}

func mergedDeliveryConfigForValidation(existing, updates map[string]string) map[string]string {
	merged := make(map[string]string, len(existing)+len(updates))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range updates {
		if strings.TrimSpace(value) == "" {
			continue
		}
		merged[key] = value
	}
	return merged
}

func handleDeleteDelivery(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if writeEngineError(w, engine.RemoveDeliveryProfile(id), http.StatusBadRequest, "delete_failed") {
			return
		}
		if !persistOrError(w, engine) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func handleSetDeliveryCommands(engine *core.Engine, enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if writeEngineError(w, engine.SetDeliveryProfileCommands(id, enabled), http.StatusBadRequest, "commands_failed") {
			return
		}
		if !persistOrError(w, engine) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// --- Commands ---

func handleGetCommands(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cmds := engine.CommandLog()
		writeJSON(w, http.StatusOK, map[string]any{"commands": cmds})
	}
}

func handleClearCommands(engine *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		engine.ClearCommandLog()
		if !persistOrError(w, engine) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// --- Helpers ---

func buildAttentionItems(engine *core.Engine, tasks []*models.Task) []map[string]any {
	profiles := engine.DeliveryProfiles()
	profileByID := make(map[string]models.DeliveryProfile, len(profiles))
	profileByAlias := make(map[string]models.DeliveryProfile, len(profiles)*3)
	for _, profile := range profiles {
		profileByID[profile.ID] = profile
		for _, alias := range []string{profile.Name, models.Slugify(profile.Name), strings.ToLower(profile.Name)} {
			alias = strings.TrimSpace(alias)
			if alias != "" {
				profileByAlias[alias] = profile
			}
		}
	}

	var items []map[string]any
	add := func(kind, severity string, task *models.Task, title, detail, action string) {
		item := map[string]any{
			"kind":     kind,
			"severity": severity,
			"title":    title,
			"detail":   detail,
			"action":   action,
		}
		if task != nil {
			item["taskID"] = task.ID
			item["taskName"] = task.DisplayName
		}
		items = append(items, item)
	}

	for _, task := range tasks {
		switch task.EnvironmentSetup.State {
		case "pending":
			add("environment", "warning", task, "Preparing Python environment", task.EnvironmentSetup.Message, "Open task")
		case "failed":
			add("environment", "danger", task, "Environment setup failed", task.EnvironmentSetup.Message, "Open task")
		}

		status := engine.ManifestStatus(task)
		if status.Error != "" {
			add("manifest", "danger", task, "Manifest cannot be inspected", status.Error, "Open task")
		} else if status.Changed {
			add("manifest", "warning", task, "Manifest changed on disk", "Reload this task so CronPlus uses the current package files.", "Reload")
		}

		if task.Enabled && len(engine.NextRunTimes(task, 1)) == 0 {
			add("schedule", "warning", task, "No upcoming scheduled run", "The schedule could not produce a next run time.", "Open task")
		}

		history := engine.RunHistory(task.ID)
		if len(history) > 0 {
			latest := history[0]
			diagnosis := core.DiagnoseRun(task, &latest)
			if diagnosis.Status == "failure" {
				add("run", "danger", task, "Latest run failed", diagnosis.Summary, "Open run")
				items[len(items)-1]["runID"] = latest.ID
				if len(items) >= 8 {
					return items
				}
			} else if diagnosis.Status == "warning" {
				add("run", "warning", task, "Latest run needs review", diagnosis.Summary, "Open run")
				items[len(items)-1]["runID"] = latest.ID
			}
		}

		if task.Manifest == nil {
			continue
		}
		for _, ref := range task.Manifest.Delivery.Profiles {
			profile, ok := profileByID[ref]
			if !ok {
				profile, ok = profileByAlias[ref]
			}
			if !ok {
				add("delivery", "danger", task, "Delivery profile not found", "Task references delivery profile "+ref+".", "Open task")
				continue
			}
			if !profile.Enabled {
				add("delivery", "warning", task, "Delivery profile disabled", profile.Name+" is disabled.", "Open delivery")
			}
			if profile.DriverType == "telegram" && (profile.Config["bot_token"] == "" || profile.Config["chat_id"] == "") {
				add("delivery", "danger", task, "Telegram profile incomplete", profile.Name+" is missing a bot token or chat ID.", "Open delivery")
			}
		}
		if len(items) >= 8 {
			return items[:8]
		}
	}
	return items
}

func deliveryProfileUsage(profile models.DeliveryProfile, tasks []*models.Task) []map[string]string {
	var usedBy []map[string]string
	for _, task := range tasks {
		if task.Manifest == nil {
			continue
		}
		for _, ref := range task.Manifest.Delivery.Profiles {
			if deliveryProfileRefMatches(profile, ref) {
				usedBy = append(usedBy, map[string]string{
					"id":   task.ID,
					"name": task.DisplayName,
				})
				break
			}
		}
	}
	return usedBy
}

func deliveryProfileRefMatches(profile models.DeliveryProfile, ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	if ref == profile.ID || ref == profile.Name || ref == models.Slugify(profile.Name) {
		return true
	}
	return strings.EqualFold(ref, profile.Name)
}

func formatTimes(times []time.Time) []string {
	if len(times) == 0 {
		return nil
	}
	result := make([]string, len(times))
	for i, t := range times {
		result[i] = t.Format(time.RFC3339)
	}
	return result
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func persistOrError(w http.ResponseWriter, engine *core.Engine) bool {
	if err := engine.PersistState(); err != nil {
		log.Printf("[CronPlus] Error: failed to persist state: %v", err)
		writeError(w, http.StatusInternalServerError, "persist_failed", "State change could not be saved: "+err.Error())
		return false
	}
	return true
}

func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// CORSMiddleware adds CORS headers to trusted UI origins only.
func CORSMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if originAllowed(origin, allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}

		if r.Method == http.MethodOptions {
			if origin != "" && !originAllowed(origin, allowedOrigins) {
				http.Error(w, `{"error":"forbidden","message":"Origin is not allowed."}`, http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// StaticHandler serves the embedded web UI files, falling back to index.html for SPA routing.
func StaticHandler(fs http.FileSystem) http.Handler {
	fileServer := http.FileServer(fs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path := r.URL.Path
		// Try to serve the exact file
		if path != "/" && !strings.HasPrefix(path, "/api/") {
			f, err := fs.Open(path)
			if err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback: serve index.html
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
