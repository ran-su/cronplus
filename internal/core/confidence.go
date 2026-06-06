package core

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ran-su/cronplus/internal/manifest"
	"github.com/ran-su/cronplus/internal/models"
)

type TaskPackageCheck struct {
	Path         string                     `json:"path"`
	ManifestPath string                     `json:"manifestPath,omitempty"`
	Name         string                     `json:"name,omitempty"`
	Status       string                     `json:"status"`
	Summary      string                     `json:"summary"`
	Issues       []manifest.ValidationIssue `json:"issues,omitempty"`
	Environment  CheckStep                  `json:"environment"`
	Run          *TaskRunCheck              `json:"run,omitempty"`
	NextRuns     []time.Time                `json:"nextRuns,omitempty"`
}

type CheckStep struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type TaskRunCheck struct {
	Status      string       `json:"status"`
	Summary     string       `json:"summary"`
	ExitCode    int          `json:"exitCode"`
	TimedOut    bool         `json:"timedOut"`
	DurationMs  int64        `json:"durationMs"`
	Diagnostics RunDiagnosis `json:"diagnostics"`
	StdoutTail  string       `json:"stdoutTail,omitempty"`
	StderrTail  string       `json:"stderrTail,omitempty"`
}

type RunDiagnosis struct {
	Status  string   `json:"status"`
	Summary string   `json:"summary"`
	Causes  []string `json:"causes,omitempty"`
	Actions []string `json:"actions,omitempty"`
}

func CheckTaskPackage(dirPath string) TaskPackageCheck {
	result := TaskPackageCheck{
		Path:   dirPath,
		Status: "failure",
		Environment: CheckStep{
			Status:  "pending",
			Message: "Environment was not checked.",
		},
	}

	manifestPath, err := manifest.FindManifest(dirPath)
	if err != nil {
		result.Summary = "No importable task package was found."
		result.Issues = append(result.Issues, manifest.ValidationIssue{
			Severity: "error",
			Path:     "manifest",
			Message:  err.Error(),
		})
		return result
	}
	result.ManifestPath = manifestPath

	loadResult, err := manifest.Load(manifestPath)
	if err != nil {
		result.Summary = "The manifest could not be read."
		result.Issues = append(result.Issues, manifest.ValidationIssue{
			Severity: "error",
			Path:     "manifest",
			Message:  err.Error(),
		})
		return result
	}
	result.Issues = append(result.Issues, loadResult.Issues...)
	if loadResult.HasErrors() {
		result.Summary = "Manifest validation failed."
		return result
	}

	m := loadResult.Manifest
	result.Name = m.Script.Name
	if result.Name == "" {
		result.Name = filepath.Base(dirPath)
	}
	result.NextRuns = NextRunTimesForManifest(m, 5, time.Now())

	manifestDir := filepath.Dir(manifestPath)
	if err := EnsureEnvironment(m, manifestDir); err != nil {
		result.Summary = "Environment setup failed."
		result.Environment = CheckStep{
			Status:  "failure",
			Message: err.Error(),
		}
		return result
	}
	result.Environment = CheckStep{
		Status:  "success",
		Message: "Environment is ready.",
	}

	outcome := RunScript(m, manifestDir)
	diagnosis := DiagnoseOutcome(*outcome, nil, m.ResultContract.ExpectStructuredResult)
	runStatus := diagnosis.Status
	result.Run = &TaskRunCheck{
		Status:      runStatus,
		Summary:     diagnosis.Summary,
		ExitCode:    outcome.ExitCode,
		TimedOut:    outcome.TimedOut,
		DurationMs:  outcome.DurationMs,
		Diagnostics: diagnosis,
		StdoutTail:  tailText(outcome.Stdout, 1400),
		StderrTail:  tailText(outcome.Stderr, 1400),
	}

	if runStatus == "failure" {
		result.Status = "failure"
		result.Summary = "Check run failed."
		return result
	}

	if hasWarningIssue(result.Issues) || runStatus == "warning" {
		result.Status = "warning"
		result.Summary = "Package is importable, with warnings."
		return result
	}

	result.Status = "success"
	result.Summary = "Package is ready to import."
	return result
}

func DiagnoseRun(task *models.Task, run *models.RunRecord) RunDiagnosis {
	if run == nil {
		return RunDiagnosis{Status: "failure", Summary: "Run record was not found."}
	}
	expectStructured := false
	if task != nil && task.Manifest != nil {
		expectStructured = task.Manifest.ResultContract.ExpectStructuredResult
	}
	return DiagnoseOutcome(run.Outcome, run.DeliveryResults, expectStructured)
}

func DiagnoseOutcome(outcome models.RunOutcome, deliveryResults []models.DeliveryResult, expectStructured bool) RunDiagnosis {
	status := models.RunStatusFromOutcome(outcome)
	diagnosis := RunDiagnosis{Status: status}

	if outcome.TimedOut {
		diagnosis.Status = "failure"
		diagnosis.Summary = fmt.Sprintf("The script timed out after %d seconds.", outcome.Diagnostics.TimeoutSeconds)
		diagnosis.Causes = append(diagnosis.Causes, "The process did not exit before the configured timeout.")
		diagnosis.Actions = append(diagnosis.Actions, "Increase runtime.timeout_seconds or make the script finish faster.")
		return diagnosis
	}

	if expectStructured && !outcome.Diagnostics.StructuredResultFound {
		diagnosis.Status = "failure"
		diagnosis.Summary = "The script finished, but did not print the required structured result."
		diagnosis.Causes = append(diagnosis.Causes, "result_contract.expect_structured_result is enabled.")
		diagnosis.Actions = append(diagnosis.Actions, "Print a CRONPLUS_RESULT=<json> line before the script exits.")
		return diagnosis
	}

	if outcome.ParsedResult != nil {
		if outcome.ParsedResult.Summary != "" {
			diagnosis.Summary = outcome.ParsedResult.Summary
		} else {
			diagnosis.Summary = fmt.Sprintf("The script reported %s.", status)
		}
		if status == "failure" {
			diagnosis.Actions = append(diagnosis.Actions, "Review the structured result and script logs for the reported failure.")
		}
	} else {
		if outcome.ExitCode != 0 {
			diagnosis.Status = "failure"
			diagnosis.Summary = fmt.Sprintf("The script exited with code %d.", outcome.ExitCode)
			if tail := firstUsefulLine(outcome.Stderr); tail != "" {
				diagnosis.Causes = append(diagnosis.Causes, tail)
			}
			diagnosis.Actions = append(diagnosis.Actions, "Open STDERR and fix the script error, missing file, or dependency problem.")
			return diagnosis
		}
		diagnosis.Summary = "The script completed successfully."
		if !outcome.Diagnostics.StructuredResultFound {
			diagnosis.Causes = append(diagnosis.Causes, "No structured result was printed, so CronPlus used the exit code.")
			diagnosis.Actions = append(diagnosis.Actions, "Add CRONPLUS_RESULT=<json> output if this task should provide a clearer summary.")
		}
	}

	if outcome.Diagnostics.OutputBytesDiscarded > 0 {
		if diagnosis.Status == "success" {
			diagnosis.Status = "warning"
		}
		diagnosis.Causes = append(diagnosis.Causes, fmt.Sprintf("Output exceeded the cap; %d bytes were discarded.", outcome.Diagnostics.OutputBytesDiscarded))
		diagnosis.Actions = append(diagnosis.Actions, "Reduce script output or increase runtime.max_output_kb.")
	}

	failedDeliveries := 0
	skippedDeliveries := 0
	for _, result := range deliveryResults {
		switch strings.ToLower(result.Status) {
		case "failed", "failure":
			failedDeliveries++
			if result.Error != "" {
				diagnosis.Causes = append(diagnosis.Causes, fmt.Sprintf("%s delivery failed: %s", result.ProfileName, result.Error))
			}
		case "skipped":
			skippedDeliveries++
		}
	}
	if failedDeliveries > 0 {
		if diagnosis.Status == "success" {
			diagnosis.Status = "warning"
		}
		if diagnosis.Summary == "" || diagnosis.Summary == "The script completed successfully." {
			diagnosis.Summary = "The script finished, but delivery failed."
		}
		diagnosis.Actions = append(diagnosis.Actions, "Test the delivery profile and verify its token, chat ID, and enabled state.")
	} else if skippedDeliveries > 0 && diagnosis.Status == "success" {
		diagnosis.Causes = append(diagnosis.Causes, "Delivery was skipped by the task's send_on/profile settings.")
	}

	if diagnosis.Summary == "" {
		diagnosis.Summary = fmt.Sprintf("The run finished with status %s.", diagnosis.Status)
	}
	return diagnosis
}

func NextRunTimesForManifest(m *models.ScriptManifest, count int, after time.Time) []time.Time {
	if m == nil || count <= 0 {
		return nil
	}
	expr, err := ParseCron(m.Schedule.Expression)
	if err != nil {
		return nil
	}
	loc, err := time.LoadLocation(m.Schedule.Timezone)
	if err != nil {
		loc = time.UTC
	}
	runs := make([]time.Time, 0, count)
	cursor := after
	for len(runs) < count {
		next := expr.NextRun(cursor, loc)
		if next == nil {
			break
		}
		runs = append(runs, *next)
		cursor = next.Add(time.Second)
	}
	return runs
}

func hasWarningIssue(issues []manifest.ValidationIssue) bool {
	for _, issue := range issues {
		if issue.Severity == "warning" {
			return true
		}
	}
	return false
}

func firstUsefulLine(text string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return line
	}
	return ""
}

func tailText(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[len(runes)-maxRunes:])
}
