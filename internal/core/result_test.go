package core

import (
	"strings"
	"testing"
)

func TestParseResult_ValidJSON(t *testing.T) {
	stdout := "Starting task...\nProcessing items\nCRONPLUS_RESULT={\"status\":\"success\",\"summary\":\"3 items found\"}\n"
	result := ParseResult(stdout, "CRONPLUS_RESULT=")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
	if result.Summary != "3 items found" {
		t.Errorf("summary = %q, want %q", result.Summary, "3 items found")
	}
}

func TestParseResult_NoPrefix(t *testing.T) {
	stdout := "Just some logs\nNo result line here\n"
	result := ParseResult(stdout, "CRONPLUS_RESULT=")
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}

func TestParseResult_InvalidJSON(t *testing.T) {
	stdout := "CRONPLUS_RESULT=not-valid-json\n"
	result := ParseResult(stdout, "CRONPLUS_RESULT=")
	if result != nil {
		t.Errorf("expected nil for invalid JSON, got %+v", result)
	}
}

func TestParseResult_CustomPrefix(t *testing.T) {
	stdout := "MY_PREFIX={\"status\":\"warning\",\"summary\":\"check needed\"}\n"
	result := ParseResult(stdout, "MY_PREFIX=")
	if result == nil {
		t.Fatal("expected non-nil result with custom prefix")
	}
	if result.Status != "warning" {
		t.Errorf("status = %q, want %q", result.Status, "warning")
	}
}

func TestParseResult_MultipleLines(t *testing.T) {
	stdout := "CRONPLUS_RESULT={\"status\":\"failed\",\"summary\":\"old\"}\nMore logs\nCRONPLUS_RESULT={\"status\":\"success\",\"summary\":\"latest\"}\n"
	result := ParseResult(stdout, "CRONPLUS_RESULT=")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Summary != "latest" {
		t.Errorf("should pick last matching line, got summary=%q", result.Summary)
	}
}

func TestParseResult_NormalizesStatusAlias(t *testing.T) {
	stdout := "CRONPLUS_RESULT={\"status\":\"failed\",\"summary\":\"old alias\"}\n"
	result := ParseResult(stdout, "CRONPLUS_RESULT=")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != "failure" {
		t.Errorf("status = %q, want failure", result.Status)
	}
}

func TestParseResult_InvalidStatusBecomesFailure(t *testing.T) {
	stdout := "CRONPLUS_RESULT={\"status\":\"sucess\",\"summary\":\"typo\"}\n"
	result := ParseResult(stdout, "CRONPLUS_RESULT=")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != "failure" {
		t.Errorf("status = %q, want failure", result.Status)
	}
	if !strings.Contains(result.Summary, "Invalid structured result status") {
		t.Errorf("summary = %q, want invalid status diagnostic", result.Summary)
	}
}

func TestParseResult_EmptyPrefix(t *testing.T) {
	stdout := "CRONPLUS_RESULT={\"status\":\"success\",\"summary\":\"ok\"}\n"
	result := ParseResult(stdout, "")
	if result == nil {
		t.Fatal("empty prefix should default to CRONPLUS_RESULT=")
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
}

func TestParseResult_WithDeliverable(t *testing.T) {
	stdout := `CRONPLUS_RESULT={"status":"success","summary":"done","deliverable":{"kind":"text","title":"Alert","body":"hello","format":"plain"}}` + "\n"
	result := ParseResult(stdout, "CRONPLUS_RESULT=")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Deliverable == nil {
		t.Fatal("expected non-nil deliverable")
	}
	if result.Deliverable.Body != "hello" {
		t.Errorf("deliverable.body = %q, want %q", result.Deliverable.Body, "hello")
	}
}
