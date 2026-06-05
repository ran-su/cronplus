package mcp

import (
	"path/filepath"
	"time"

	"github.com/ran-su/cronplus/internal/core"
	"github.com/ran-su/cronplus/internal/manifest"
)

func validateTaskPackage(dirPath string) map[string]any {
	result := map[string]any{
		"path":    dirPath,
		"status":  "failure",
		"summary": "No importable task package was found.",
	}

	manifestPath, err := manifest.FindManifest(dirPath)
	if err != nil {
		result["issues"] = []manifest.ValidationIssue{{
			Severity: "error",
			Path:     "manifest",
			Message:  err.Error(),
		}}
		return result
	}
	result["manifestPath"] = manifestPath

	loadResult, err := manifest.Load(manifestPath)
	if err != nil {
		result["summary"] = "The manifest could not be read."
		result["issues"] = []manifest.ValidationIssue{{
			Severity: "error",
			Path:     "manifest",
			Message:  err.Error(),
		}}
		return result
	}

	result["issues"] = loadResult.Issues
	if loadResult.HasErrors() {
		result["summary"] = "Manifest validation failed."
		return result
	}

	name := loadResult.Manifest.Script.Name
	if name == "" {
		name = filepath.Base(dirPath)
	}
	result["name"] = name
	result["nextRuns"] = formatValidationTimes(core.NextRunTimesForManifest(loadResult.Manifest, 5, time.Now()))

	if hasValidationWarnings(loadResult.Issues) {
		result["status"] = "warning"
		result["summary"] = "Manifest is valid, with warnings. No environment setup or script run was performed."
		return result
	}

	result["status"] = "success"
	result["summary"] = "Manifest is valid. No environment setup or script run was performed."
	return result
}

func formatValidationTimes(times []time.Time) []string {
	if len(times) == 0 {
		return nil
	}
	out := make([]string, len(times))
	for i, t := range times {
		out[i] = t.Format(time.RFC3339)
	}
	return out
}

func hasValidationWarnings(issues []manifest.ValidationIssue) bool {
	for _, issue := range issues {
		if issue.Severity == "warning" {
			return true
		}
	}
	return false
}
