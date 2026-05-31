package models

import (
	"strings"
	"time"
)

// RunOutcome is the raw result of executing a script.
type RunOutcome struct {
	ExitCode     int            `json:"exitCode"`
	Stdout       string         `json:"stdout"`
	Stderr       string         `json:"stderr"`
	ParsedResult *ParsedResult  `json:"parsedResult,omitempty"`
	TimedOut     bool           `json:"timedOut"`
	DurationMs   int64          `json:"durationMs"`
	Diagnostics  RunDiagnostics `json:"diagnostics"`
}

type RunDiagnostics struct {
	PythonExecutable      string                `json:"pythonExecutable"`
	ScriptPath            string                `json:"scriptPath"`
	WorkingDirectory      string                `json:"workingDirectory"`
	EnvironmentStrategy   string                `json:"environmentStrategy"`
	RequirementsFile      string                `json:"requirementsFile,omitempty"`
	EnvFile               string                `json:"envFile,omitempty"`
	TimeoutSeconds        int                   `json:"timeoutSeconds"`
	MaxOutputKB           int                   `json:"maxOutputKB"`
	StructuredResultFound bool                  `json:"structuredResultFound"`
	RootPID               int                   `json:"rootPID,omitempty"`
	ProcessGroupID        int                   `json:"processGroupID,omitempty"`
	RunDirectory          string                `json:"runDirectory,omitempty"`
	IsolatedRun           bool                  `json:"isolatedRun"`
	StdoutBytes           int64                 `json:"stdoutBytes"`
	StderrBytes           int64                 `json:"stderrBytes"`
	StdoutTruncated       bool                  `json:"stdoutTruncated"`
	StderrTruncated       bool                  `json:"stderrTruncated"`
	OutputBytesDiscarded  int64                 `json:"outputBytesDiscarded"`
	LimitMode             string                `json:"limitMode,omitempty"`
	Cleanup               RunCleanupDiagnostics `json:"cleanup"`
}

type RunCleanupDiagnostics struct {
	ProcessGroupTerminated   bool   `json:"processGroupTerminated"`
	ProcessGroupForceKilled  bool   `json:"processGroupForceKilled"`
	DetachedProcessesKilled  int    `json:"detachedProcessesKilled"`
	RunDirectoryRemoved      bool   `json:"runDirectoryRemoved"`
	RunDirectoryCleanupError string `json:"runDirectoryCleanupError,omitempty"`
	OrphanScanError          string `json:"orphanScanError,omitempty"`
}

type ActiveRunInfo struct {
	TaskID         string    `json:"taskID"`
	RunID          string    `json:"runID"`
	RootPID        int       `json:"rootPID"`
	ProcessGroupID int       `json:"processGroupID"`
	RunDirectory   string    `json:"runDirectory"`
	StartedAt      time.Time `json:"startedAt"`
}

// ParsedResult is the structured data extracted from CRONPLUS_RESULT=<json>.
type ParsedResult struct {
	Status      string       `json:"status"`
	Summary     string       `json:"summary"`
	Deliverable *Deliverable `json:"deliverable,omitempty"`
	Data        any          `json:"data,omitempty"`
}

// Deliverable is the structured payload for delivery channels.
type Deliverable struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Format string `json:"format"`
}

// RunRecord is a persisted record of a completed run.
type RunRecord struct {
	ID              string           `json:"id"`
	TaskID          string           `json:"taskID"`
	Trigger         string           `json:"trigger"` // "schedule", "manual", "command"
	StartedAt       time.Time        `json:"startedAt"`
	FinishedAt      time.Time        `json:"finishedAt"`
	Outcome         RunOutcome       `json:"outcome"`
	DeliveryResults []DeliveryResult `json:"deliveryResults,omitempty"`
}

// DeliveryResult records the outcome of a delivery attempt.
type DeliveryResult struct {
	ProfileID   string `json:"profileID"`
	ProfileName string `json:"profileName"`
	Status      string `json:"status"` // "success", "failed", "skipped"
	Error       string `json:"error,omitempty"`
}

// NormalizeRunStatus maps accepted run-status aliases to the canonical schema values.
func NormalizeRunStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed":
		return "failure"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

// IsValidRunStatus reports whether status is one of the canonical task result states.
func IsValidRunStatus(status string) bool {
	switch NormalizeRunStatus(status) {
	case "success", "failure", "warning", "skipped":
		return true
	default:
		return false
	}
}

// RunStatusFromOutcome returns the canonical run status for a completed script run.
func RunStatusFromOutcome(outcome RunOutcome) string {
	status := "failure"
	if outcome.ExitCode == 0 {
		status = "success"
	}
	if outcome.ParsedResult != nil && outcome.ParsedResult.Status != "" {
		parsedStatus := NormalizeRunStatus(outcome.ParsedResult.Status)
		if IsValidRunStatus(parsedStatus) {
			status = parsedStatus
		}
	}
	return status
}
