package models

import "testing"

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
