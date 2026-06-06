package models

import (
	"strings"
	"time"
)

// Task is an imported task package managed by the daemon.
type Task struct {
	ID              string          `json:"id"`
	PackageDir      string          `json:"packageDir"`
	ManifestPath    string          `json:"manifestPath"`
	Manifest        *ScriptManifest `json:"manifest,omitempty"`
	Enabled         bool            `json:"enabled"`
	CreatedAt       time.Time       `json:"createdAt"`
	LastReloadedAt  time.Time       `json:"lastReloadedAt"`
	ManifestHash    string          `json:"manifestHash,omitempty"`
	ManifestModTime time.Time       `json:"manifestModTime,omitempty"`
	DisplayName       string                  `json:"displayName"`
	ScheduleText      string                  `json:"scheduleText"`
	EnvironmentSetup  EnvironmentSetupStatus  `json:"environmentSetup"`
}

// EnvironmentSetupStatus tracks background Python environment preparation for a task.
type EnvironmentSetupStatus struct {
	State      string    `json:"state"` // ready, pending, failed, not_required
	Message    string    `json:"message,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

type ManifestStatus struct {
	LoadedHash        string    `json:"loadedHash,omitempty"`
	CurrentHash       string    `json:"currentHash,omitempty"`
	Changed           bool      `json:"changed"`
	LastReloadedAt    time.Time `json:"lastReloadedAt"`
	LoadedModifiedAt  time.Time `json:"loadedModifiedAt,omitempty"`
	CurrentModifiedAt time.Time `json:"currentModifiedAt,omitempty"`
	Error             string    `json:"error,omitempty"`
}

type TaskTimeline struct {
	NextRunAt           *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt           *time.Time `json:"lastRunAt,omitempty"`
	LastSuccessAt       *time.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt       *time.Time `json:"lastFailureAt,omitempty"`
	AverageDurationMs   int64      `json:"averageDurationMs"`
	ConsecutiveFailures int        `json:"consecutiveFailures"`
	TotalRuns           int        `json:"totalRuns"`
}

// Slug returns a URL-safe identifier derived from the display name.
func (t *Task) Slug() string {
	return Slugify(t.DisplayName)
}

// Slugify converts a display name to a URL-safe slug.
func Slugify(name string) string {
	s := strings.ToLower(name)
	var b strings.Builder
	prevDash := false
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			prevDash = false
		} else if c == ' ' || c == '-' || c == '_' {
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	result := b.String()
	return strings.TrimRight(result, "-")
}
