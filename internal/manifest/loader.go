package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ran-su/cronplus/internal/models"
	"gopkg.in/yaml.v3"
)

// ValidationIssue describes a problem found during validation.
type ValidationIssue struct {
	Severity string `json:"severity"` // "error" or "warning"
	Path     string `json:"path"`
	Message  string `json:"message"`
}

// LoadResult is the result of loading and validating a manifest.
type LoadResult struct {
	Manifest *models.ScriptManifest
	Issues   []ValidationIssue
}

func (r *LoadResult) HasErrors() bool {
	for _, issue := range r.Issues {
		if issue.Severity == "error" {
			return true
		}
	}
	return false
}

// FindManifest searches for a .cronplus.yaml file in the given directory.
func FindManifest(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("cannot read directory %s: %w", dir, err)
	}

	var matches []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".cronplus.yaml") || strings.HasSuffix(name, ".cronplus.yml") {
			matches = append(matches, name)
		}
	}
	if len(matches) == 1 {
		return filepath.Join(dir, matches[0]), nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple .cronplus manifests found in %s: %s", dir, strings.Join(matches, ", "))
	}

	return "", fmt.Errorf("no .cronplus.yaml manifest found in %s", dir)
}

// Load reads and validates a manifest file.
func Load(manifestPath string) (*LoadResult, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read manifest %s: %w", manifestPath, err)
	}

	var m models.ScriptManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid YAML in %s: %w", manifestPath, err)
	}

	m.Defaults()

	dir := filepath.Dir(manifestPath)
	issues := validate(&m, dir)

	result := &LoadResult{Issues: issues}
	if !result.HasErrors() {
		result.Manifest = &m
	}
	return result, nil
}

func validate(m *models.ScriptManifest, dir string) []ValidationIssue {
	var issues []ValidationIssue

	// Manifest version
	if m.ManifestVersion < 1 {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "manifest_version",
			Message:  "Must be >= 1.",
		})
	}

	// Script path
	if m.Script.Path == "" {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "script.path",
			Message:  "Required.",
		})
	} else {
		resolved := resolvePath(dir, m.Script.Path)
		info, err := os.Stat(resolved)
		if err != nil {
			message := fmt.Sprintf("File not found: %s", resolved)
			if !os.IsNotExist(err) {
				message = fmt.Sprintf("Cannot inspect file: %s: %v", resolved, err)
			}
			issues = append(issues, ValidationIssue{
				Severity: "error",
				Path:     "script.path",
				Message:  message,
			})
		} else if info.IsDir() || !info.Mode().IsRegular() {
			issues = append(issues, ValidationIssue{
				Severity: "error",
				Path:     "script.path",
				Message:  fmt.Sprintf("Must be a regular file: %s", resolved),
			})
		}
	}

	// Script name
	if m.Script.Name == "" {
		issues = append(issues, ValidationIssue{
			Severity: "warning",
			Path:     "script.name",
			Message:  "No script name specified; will use filename.",
		})
	}

	// Schedule
	if m.Schedule.Type != "cron" {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "schedule.type",
			Message:  "Only cron schedules are supported.",
		})
	}
	if m.Schedule.Expression == "" {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "schedule.expression",
			Message:  "Cron expression is required.",
		})
	} else if err := validateCronExpression(m.Schedule.Expression); err != nil {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "schedule.expression",
			Message:  err.Error(),
		})
	}
	if _, err := time.LoadLocation(m.Schedule.Timezone); err != nil {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "schedule.timezone",
			Message:  fmt.Sprintf("Invalid IANA timezone: %s", m.Schedule.Timezone),
		})
	}
	if m.Schedule.MissedRunPolicy != "skip" {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "schedule.missed_run_policy",
			Message:  "Only skip is supported. Missed runs are not backfilled.",
		})
	}

	// Environment strategy
	validStrategies := map[string]bool{
		"system": true, "managed_venv": true, "venv_path": true,
	}
	if !validStrategies[m.Runtime.Environment.Strategy] {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "runtime.environment.strategy",
			Message:  fmt.Sprintf("Unknown strategy: %q. Must be system, managed_venv, or venv_path.", m.Runtime.Environment.Strategy),
		})
	}
	if m.Runtime.TimeoutSeconds <= 0 {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "runtime.timeout_seconds",
			Message:  "Must be greater than 0.",
		})
	}
	if m.Runtime.MaxOutputKB <= 0 {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "runtime.max_output_kb",
			Message:  "Must be greater than 0.",
		})
	}
	if m.Runtime.ResourceLimits.GracefulKillSeconds <= 0 {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "runtime.resource_limits.graceful_kill_seconds",
			Message:  "Must be greater than 0.",
		})
	}
	if m.Runtime.ResourceLimits.MaxOpenFiles < 0 {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "runtime.resource_limits.max_open_files",
			Message:  "Must be greater than or equal to 0.",
		})
	}
	if m.Runtime.ResourceLimits.MaxProcesses < 0 {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "runtime.resource_limits.max_processes",
			Message:  "Must be greater than or equal to 0.",
		})
	}
	if m.Runtime.ResourceLimits.MaxCPUSeconds < 0 {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "runtime.resource_limits.max_cpu_seconds",
			Message:  "Must be greater than or equal to 0.",
		})
	}
	if m.Runtime.ResourceLimits.MaxMemoryMB < 0 {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "runtime.resource_limits.max_memory_mb",
			Message:  "Must be greater than or equal to 0.",
		})
	}
	if m.Runtime.WorkingDir != "" && m.Runtime.WorkingDir != "." {
		resolved := resolvePath(dir, m.Runtime.WorkingDir)
		info, err := os.Stat(resolved)
		if err != nil {
			message := fmt.Sprintf("Directory not found: %s", resolved)
			if !os.IsNotExist(err) {
				message = fmt.Sprintf("Cannot inspect directory: %s: %v", resolved, err)
			}
			issues = append(issues, ValidationIssue{
				Severity: "error",
				Path:     "runtime.working_directory",
				Message:  message,
			})
		} else if !info.IsDir() {
			issues = append(issues, ValidationIssue{
				Severity: "error",
				Path:     "runtime.working_directory",
				Message:  fmt.Sprintf("Must be a directory: %s", resolved),
			})
		}
	}
	if m.Runtime.Environment.Strategy == "venv_path" && m.Runtime.Environment.VenvPath == "" {
		issues = append(issues, ValidationIssue{
			Severity: "error",
			Path:     "runtime.environment.venv_path",
			Message:  "Required when strategy is venv_path.",
		})
	}
	if m.Runtime.EnvFile != "" {
		if _, err := os.Stat(resolvePath(dir, m.Runtime.EnvFile)); err != nil {
			issues = append(issues, ValidationIssue{
				Severity: "error",
				Path:     "runtime.env_file",
				Message:  fmt.Sprintf("File not found: %s", resolvePath(dir, m.Runtime.EnvFile)),
			})
		}
	}
	for name, envVar := range m.Runtime.Env {
		if envVar.Type != "plain" && envVar.Type != "secret" {
			issues = append(issues, ValidationIssue{
				Severity: "error",
				Path:     fmt.Sprintf("runtime.env.%s.type", name),
				Message:  "Must be plain or secret.",
			})
		}
		if envVar.Type == "secret" && !strings.HasPrefix(envVar.Value, "env://") {
			issues = append(issues, ValidationIssue{
				Severity: "warning",
				Path:     fmt.Sprintf("runtime.env.%s.value", name),
				Message:  "Secret values currently support env://NAME references. Other secret resolvers are ignored.",
			})
		}
	}

	validSendOn := map[string]bool{
		"success": true,
		"failure": true,
		"warning": true,
		"skipped": true,
	}
	for i, condition := range m.Delivery.SendOn {
		if !validSendOn[models.NormalizeRunStatus(condition)] {
			issues = append(issues, ValidationIssue{
				Severity: "error",
				Path:     fmt.Sprintf("delivery.send_on[%d]", i),
				Message:  fmt.Sprintf("Unknown condition: %q.", condition),
			})
		}
	}

	// Inline delivery profiles
	seenInlineProfileIDs := make(map[string]int)
	for i, p := range m.Delivery.InlineProfiles {
		path := fmt.Sprintf("delivery.inline_profiles[%d]", i)
		if p.ID == "" {
			issues = append(issues, ValidationIssue{Severity: "error", Path: path + ".id", Message: "Required."})
		} else if first, ok := seenInlineProfileIDs[p.ID]; ok {
			issues = append(issues, ValidationIssue{
				Severity: "error",
				Path:     path + ".id",
				Message:  fmt.Sprintf("Duplicate inline profile id %q; first used at delivery.inline_profiles[%d].id.", p.ID, first),
			})
		} else {
			seenInlineProfileIDs[p.ID] = i
		}
		if p.Driver == "" {
			issues = append(issues, ValidationIssue{Severity: "error", Path: path + ".driver", Message: "Required."})
		}
	}

	return issues
}

func validateCronExpression(expr string) error {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return fmt.Errorf("cron expression must have 5 fields, got %d", len(parts))
	}
	checks := []struct {
		name string
		raw  string
		min  int
		max  int
	}{
		{"minute", parts[0], 0, 59},
		{"hour", parts[1], 0, 23},
		{"day-of-month", parts[2], 1, 31},
		{"month", parts[3], 1, 12},
		{"day-of-week", parts[4], 0, 7},
	}
	for _, check := range checks {
		if err := validateCronField(check.raw, check.min, check.max); err != nil {
			return fmt.Errorf("%s field: %w", check.name, err)
		}
	}
	return nil
}

func validateCronField(raw string, min, max int) error {
	if raw == "*" {
		return nil
	}
	if strings.HasPrefix(raw, "*/") {
		step, err := strconv.Atoi(raw[2:])
		if err != nil || step <= 0 {
			return fmt.Errorf("invalid step %q", raw)
		}
		if step > max-min+1 {
			return fmt.Errorf("step %q exceeds field range", raw)
		}
		return nil
	}

	for _, seg := range strings.Split(raw, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return fmt.Errorf("empty segment")
		}
		if idx := strings.Index(seg, "-"); idx >= 0 {
			lo, err1 := strconv.Atoi(seg[:idx])
			hi, err2 := strconv.Atoi(seg[idx+1:])
			if err1 != nil || err2 != nil || lo > hi {
				return fmt.Errorf("invalid range %q", seg)
			}
			if lo < min || hi > max {
				return fmt.Errorf("range %q out of bounds (%d-%d)", seg, min, max)
			}
			continue
		}
		v, err := strconv.Atoi(seg)
		if err != nil {
			return fmt.Errorf("invalid value %q", seg)
		}
		if v < min || v > max {
			return fmt.Errorf("value %q out of bounds (%d-%d)", seg, min, max)
		}
	}
	return nil
}

func resolvePath(base, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(base, rel)
}

// ResolveScriptPath returns the absolute path to the script file.
func ResolveScriptPath(manifestDir string, m *models.ScriptManifest) string {
	return resolvePath(manifestDir, m.Script.Path)
}

// ResolveWorkingDir returns the absolute working directory for the script.
func ResolveWorkingDir(manifestDir string, m *models.ScriptManifest) string {
	if m.Runtime.WorkingDir == "" || m.Runtime.WorkingDir == "." {
		return manifestDir
	}
	return resolvePath(manifestDir, m.Runtime.WorkingDir)
}
