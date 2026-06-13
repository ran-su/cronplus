package api

import (
	"errors"
	"net/http"

	"github.com/ran-su/cronplus/internal/core"
)

// writeEngineError maps core engine errors to HTTP responses.
// Returns true when err was non-nil and a response was written.
func writeEngineError(w http.ResponseWriter, err error, fallbackStatus int, fallbackCode string) bool {
	if err == nil {
		return false
	}

	switch {
	case errors.Is(err, core.ErrTaskNotFound):
		writeError(w, http.StatusNotFound, "task_not_found", err.Error())
	case errors.Is(err, core.ErrTaskAlreadyRunning):
		writeError(w, http.StatusConflict, "task_already_running", err.Error())
	case errors.Is(err, core.ErrMaxConcurrentRuns):
		writeError(w, http.StatusConflict, "max_concurrent_runs", err.Error())
	case errors.Is(err, core.ErrTaskNoManifest):
		writeError(w, http.StatusBadRequest, "task_no_manifest", err.Error())
	case errors.Is(err, core.ErrEnvironmentSetupPending):
		writeError(w, http.StatusConflict, "environment_setup_pending", err.Error())
	case errors.Is(err, core.ErrEnvironmentSetupFailed):
		writeError(w, http.StatusBadRequest, "environment_setup_failed", err.Error())
	case errors.Is(err, core.ErrEnvironmentNotRebuildable):
		writeError(w, http.StatusBadRequest, "environment_not_rebuildable", err.Error())
	case errors.Is(err, core.ErrDeliveryProfileNotFound):
		writeError(w, http.StatusNotFound, "profile_not_found", err.Error())
	default:
		var manifestErr *core.ManifestValidationError
		if errors.As(err, &manifestErr) {
			writeError(w, http.StatusBadRequest, "manifest_validation_failed", err.Error())
			return true
		}
		writeError(w, fallbackStatus, fallbackCode, err.Error())
	}
	return true
}
