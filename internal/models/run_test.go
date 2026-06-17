package models

import (
	"encoding/json"
	"testing"
)

func TestRunStatusFromOutcomeIgnoresInvalidParsedStatus(t *testing.T) {
	status := RunStatusFromOutcome(RunOutcome{
		ExitCode:     0,
		ParsedResult: &ParsedResult{Status: "sucess"},
	})
	if status != "success" {
		t.Fatalf("status = %q, want success from exit code", status)
	}
}

func TestRunStatusFromOutcomeUsesValidParsedStatus(t *testing.T) {
	status := RunStatusFromOutcome(RunOutcome{
		ExitCode:     0,
		ParsedResult: &ParsedResult{Status: "failed"},
	})
	if status != "failure" {
		t.Fatalf("status = %q, want failure", status)
	}
}

func TestRunStatusFromOutcomeUsesValidParsedStatusOverExitCode(t *testing.T) {
	status := RunStatusFromOutcome(RunOutcome{
		ExitCode:     1,
		ParsedResult: &ParsedResult{Status: "success"},
	})
	if status != "success" {
		t.Fatalf("status = %q, want success from parsed result", status)
	}
}

func TestRunStatusFromOutcomeTreatsCanceledOrTimedOutAsFailure(t *testing.T) {
	tests := []struct {
		name    string
		outcome RunOutcome
	}{
		{
			name: "canceled",
			outcome: RunOutcome{
				ExitCode:     0,
				ParsedResult: &ParsedResult{Status: "success"},
				Diagnostics:  RunDiagnostics{Canceled: true},
			},
		},
		{
			name: "timed out",
			outcome: RunOutcome{
				ExitCode:     0,
				ParsedResult: &ParsedResult{Status: "success"},
				TimedOut:     true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := RunStatusFromOutcome(tt.outcome)
			if status != "failure" {
				t.Fatalf("status = %q, want failure", status)
			}
		})
	}
}

func TestParsedResultJSONPreservesArbitraryFields(t *testing.T) {
	var result ParsedResult
	if err := json.Unmarshal([]byte(`{"status":"success","message":"hello","deliverable":{"body":"nested","extra":"kept"}}`), &result); err != nil {
		t.Fatal(err)
	}
	if got, want := result.Fields["message"], "hello"; got != want {
		t.Fatalf("fields.message = %q, want %q", got, want)
	}

	out, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(out, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if got, want := roundTrip["message"], "hello"; got != want {
		t.Fatalf("roundTrip.message = %q, want %q", got, want)
	}
	deliverable, ok := roundTrip["deliverable"].(map[string]any)
	if !ok {
		t.Fatalf("roundTrip.deliverable = %T, want map[string]any", roundTrip["deliverable"])
	}
	if got, want := deliverable["extra"], "kept"; got != want {
		t.Fatalf("roundTrip.deliverable.extra = %q, want %q", got, want)
	}
}

func TestParsedResultJSONDoesNotTypeCheckTaskFields(t *testing.T) {
	var result ParsedResult
	if err := json.Unmarshal([]byte(`{"status":"success","summary":{"text":"ok"},"deliverable":"send this"}`), &result); err != nil {
		t.Fatal(err)
	}
	if result.Summary != "" {
		t.Fatalf("Summary = %q, want empty typed helper for non-string summary", result.Summary)
	}
	if result.Deliverable != nil {
		t.Fatalf("Deliverable = %+v, want nil typed helper for non-object deliverable", result.Deliverable)
	}
	if got, want := result.Fields["deliverable"], "send this"; got != want {
		t.Fatalf("fields.deliverable = %q, want %q", got, want)
	}

	summary, ok := result.Fields["summary"].(map[string]any)
	if !ok {
		t.Fatalf("fields.summary = %T, want map[string]any", result.Fields["summary"])
	}
	if got, want := summary["text"], "ok"; got != want {
		t.Fatalf("fields.summary.text = %q, want %q", got, want)
	}
}
