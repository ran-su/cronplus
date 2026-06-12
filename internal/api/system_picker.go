package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

var errDirectoryPickerUnavailable = errors.New("system directory picker is not available on this platform")

type directoryPickerResult struct {
	Path     string `json:"path,omitempty"`
	Canceled bool   `json:"canceled,omitempty"`
}

type directoryPicker func(context.Context) (directoryPickerResult, error)

func handlePickDirectory(picker directoryPicker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if picker == nil {
			picker = pickDirectory
		}

		result, err := picker(r.Context())
		if err != nil {
			if errors.Is(err, errDirectoryPickerUnavailable) {
				writeError(w, http.StatusNotImplemented, "picker_unavailable", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "picker_failed", err.Error())
			return
		}

		if !result.Canceled {
			result.Path = strings.TrimSpace(result.Path)
			if result.Path == "" {
				writeError(w, http.StatusInternalServerError, "picker_failed", "System directory picker did not return a path.")
				return
			}
		}

		writeJSON(w, http.StatusOK, result)
	}
}
