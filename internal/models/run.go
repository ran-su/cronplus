package models

import (
	"encoding/json"
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
	StdoutRetentionPruned bool                  `json:"stdoutRetentionPruned,omitempty"`
	StderrRetentionPruned bool                  `json:"stderrRetentionPruned,omitempty"`
	OutputBytesPruned     int64                 `json:"outputBytesPruned,omitempty"`
	LimitMode             string                `json:"limitMode,omitempty"`
	Canceled              bool                  `json:"canceled,omitempty"`
	CancelReason          string                `json:"cancelReason,omitempty"`
	CancelRequestedAt     *time.Time            `json:"cancelRequestedAt,omitempty"`
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
	TaskID              string     `json:"taskID"`
	TaskName            string     `json:"taskName,omitempty"`
	TaskSlug            string     `json:"taskSlug,omitempty"`
	RunID               string     `json:"runID"`
	Trigger             string     `json:"trigger,omitempty"`
	RootPID             int        `json:"rootPID"`
	ProcessGroupID      int        `json:"processGroupID"`
	RunDirectory        string     `json:"runDirectory"`
	PythonExecutable    string     `json:"pythonExecutable,omitempty"`
	ScriptPath          string     `json:"scriptPath,omitempty"`
	WorkingDirectory    string     `json:"workingDirectory,omitempty"`
	EnvironmentStrategy string     `json:"environmentStrategy,omitempty"`
	TimeoutSeconds      int        `json:"timeoutSeconds,omitempty"`
	MaxOutputKB         int        `json:"maxOutputKB,omitempty"`
	StartedAt           time.Time  `json:"startedAt"`
	ElapsedMs           int64      `json:"elapsedMs,omitempty"`
	CancelRequested     bool       `json:"cancelRequested,omitempty"`
	CancelReason        string     `json:"cancelReason,omitempty"`
	CancelRequestedAt   *time.Time `json:"cancelRequestedAt,omitempty"`
	StdoutTail          string     `json:"stdoutTail,omitempty"`
	StderrTail          string     `json:"stderrTail,omitempty"`
}

// ParsedResult is the structured data extracted from CRONPLUS_RESULT=<json>.
type ParsedResult struct {
	Status      string         `json:"status"`
	Summary     string         `json:"summary"`
	Deliverable *Deliverable   `json:"deliverable,omitempty"`
	Data        any            `json:"data,omitempty"`
	Fields      map[string]any `json:"-"`
}

func (p *ParsedResult) UnmarshalJSON(b []byte) error {
	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	var known struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(b, &known); err != nil {
		return err
	}
	p.Status = known.Status
	if summary, ok := fields["summary"].(string); ok {
		p.Summary = summary
	}
	if deliverable, ok := decodeDeliverableField(fields["deliverable"]); ok {
		p.Deliverable = deliverable
	}
	p.Data = fields["data"]
	p.Fields = fields
	return nil
}

func decodeDeliverableField(value any) (*Deliverable, bool) {
	if value == nil {
		return nil, false
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var deliverable Deliverable
	if err := json.Unmarshal(b, &deliverable); err != nil {
		return nil, false
	}
	return &deliverable, true
}

func (p ParsedResult) MarshalJSON() ([]byte, error) {
	fields := make(map[string]any, len(p.Fields)+4)
	for k, v := range p.Fields {
		fields[k] = v
	}
	if p.Status != "" {
		fields["status"] = p.Status
	}
	if p.Summary != "" {
		fields["summary"] = p.Summary
	}
	if _, ok := fields["deliverable"]; !ok && p.Deliverable != nil {
		fields["deliverable"] = p.Deliverable
	}
	if _, ok := fields["data"]; !ok && p.Data != nil {
		fields["data"] = p.Data
	}
	return json.Marshal(fields)
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
	if outcome.TimedOut || outcome.Diagnostics.Canceled {
		return "failure"
	}
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
