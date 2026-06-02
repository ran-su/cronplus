package delivery

import (
	"fmt"
	"testing"

	"github.com/ran-su/cronplus/internal/models"
)

// mockDriver is a test double for delivery.Driver.
type mockDriver struct {
	sentMessages []string
	failOnSend   bool
}

func (m *mockDriver) Type() string { return "mock" }
func (m *mockDriver) Send(profile models.DeliveryProfile, message string) error {
	if m.failOnSend {
		return fmt.Errorf("mock send failure")
	}
	m.sentMessages = append(m.sentMessages, message)
	return nil
}

func makeTask(profileIDs []string, sendOn []string) *models.Task {
	return &models.Task{
		ID:          "task1",
		DisplayName: "Test Task",
		Manifest: &models.ScriptManifest{
			Delivery: models.DeliverySection{
				Profiles: profileIDs,
				SendOn:   sendOn,
			},
		},
	}
}

func makeRun(exitCode int, status, summary string) *models.RunRecord {
	record := &models.RunRecord{
		ID:     "run1",
		TaskID: "task1",
		Outcome: models.RunOutcome{
			ExitCode:   exitCode,
			DurationMs: 1500,
		},
	}
	if status != "" {
		record.Outcome.ParsedResult = &models.ParsedResult{
			Status:  status,
			Summary: summary,
		}
	}
	return record
}

func TestDeliver_MatchesSendOn(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Mock", DriverType: "mock", Enabled: true},
	}

	// Task sends on "success" only
	task := makeTask([]string{"p1"}, []string{"success"})

	// Success run — should send
	run := makeRun(0, "success", "done")
	results := svc.Deliver(task, run, profiles)
	if len(results) != 1 || results[0].Status != "success" {
		t.Errorf("expected delivery success, got %+v", results)
	}

	// Failure run — should NOT send (not in send_on)
	mock.sentMessages = nil
	run2 := makeRun(1, "failed", "error")
	results2 := svc.Deliver(task, run2, profiles)
	if len(results2) != 1 || results2[0].Status != "skipped" {
		t.Errorf("expected skipped delivery for failed status, got %+v", results2)
	}
	if len(mock.sentMessages) != 0 {
		t.Fatalf("expected no messages, got %q", mock.sentMessages)
	}
}

func TestDeliver_SkipsDisabledProfile(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Disabled", DriverType: "mock", Enabled: false},
	}

	task := makeTask([]string{"p1"}, []string{"success"})
	run := makeRun(0, "success", "done")
	results := svc.Deliver(task, run, profiles)
	if len(results) != 1 || results[0].Status != "skipped" {
		t.Errorf("expected skipped for disabled profile, got %+v", results)
	}
}

func TestDeliver_TreatsFailedAsFailureAlias(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Mock", DriverType: "mock", Enabled: true},
	}

	task := makeTask([]string{"p1"}, []string{"failure"})
	run := makeRun(1, "failed", "error")
	results := svc.Deliver(task, run, profiles)
	if len(results) != 1 || results[0].Status != "success" {
		t.Errorf("expected failed alias to match failure send_on, got %+v", results)
	}
}

func TestDeliver_UnknownDriver(t *testing.T) {
	svc := NewService() // no drivers registered

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Unknown", DriverType: "webhook", Enabled: true},
	}

	task := makeTask([]string{"p1"}, []string{"success"})
	run := makeRun(0, "success", "done")
	results := svc.Deliver(task, run, profiles)
	if len(results) != 1 || results[0].Status != "failed" {
		t.Errorf("expected failed for unknown driver, got %+v", results)
	}
}

func TestBuildMessage_DocumentedTemplateKeys(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Test", DriverType: "mock", Enabled: true},
	}

	task := makeTask([]string{"p1"}, []string{"success"})
	task.Manifest.Delivery.MessageTemplate = "[{{.TaskName}}] {{.Status}} {{.Summary}}"
	run := makeRun(0, "success", "All good")
	svc.Deliver(task, run, profiles)

	if len(mock.sentMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mock.sentMessages))
	}
	if got, want := mock.sentMessages[0], "[Test Task] success All good"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestBuildMessage_ArbitraryResultTemplate(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Test", DriverType: "mock", Enabled: true},
	}

	task := makeTask([]string{"p1"}, []string{"success"})
	task.Manifest.Delivery.MessageTemplate = "{{payload.body}}"
	run := makeRun(0, "success", "All good")
	run.Outcome.ParsedResult.Fields = map[string]any{
		"payload": map[string]any{"body": "deliver this body"},
	}
	svc.Deliver(task, run, profiles)

	if len(mock.sentMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mock.sentMessages))
	}
	if got, want := mock.sentMessages[0], "deliver this body"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestBuildMessage_ShortTemplateUsesParsedResultJSON(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Test", DriverType: "mock", Enabled: true},
	}

	task := makeTask([]string{"p1"}, []string{"success"})
	task.Manifest.Delivery.MessageTemplate = "{{deliverable.body}}"
	run := makeRun(0, "success", "All good")
	run.Outcome.ParsedResult.Fields = map[string]any{
		"deliverable": map[string]any{"body": "deliver this body"},
	}
	svc.Deliver(task, run, profiles)

	if len(mock.sentMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mock.sentMessages))
	}
	if got, want := mock.sentMessages[0], "deliver this body"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestBuildMessage_DataTemplate(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Test", DriverType: "mock", Enabled: true},
	}

	task := makeTask([]string{"p1"}, []string{"success"})
	task.Manifest.Delivery.MessageTemplate = "Price: ${{data.price}}"
	run := makeRun(0, "success", "All good")
	run.Outcome.ParsedResult.Data = map[string]any{"price": 19.99}
	svc.Deliver(task, run, profiles)

	if len(mock.sentMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mock.sentMessages))
	}
	if got, want := mock.sentMessages[0], "Price: $19.99"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestDeliver_SkipsEmptyRenderedTemplate(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Test", DriverType: "mock", Enabled: true},
	}

	task := makeTask([]string{"p1"}, []string{"success"})
	task.Manifest.Delivery.MessageTemplate = "{{payload.body}}"
	run := makeRun(0, "success", "All good")
	run.Outcome.ParsedResult.Fields = map[string]any{
		"payload": map[string]any{"body": ""},
	}
	results := svc.Deliver(task, run, profiles)

	if len(results) != 1 || results[0].Status != "skipped" {
		t.Fatalf("expected skipped delivery result for empty rendered template, got %+v", results)
	}
	if len(mock.sentMessages) != 0 {
		t.Fatalf("expected no messages, got %q", mock.sentMessages)
	}
}

func TestDeliver_TemplateRenderErrorDoesNotSendDefaultMessage(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Test", DriverType: "mock", Enabled: true},
	}

	task := makeTask([]string{"p1"}, []string{"success"})
	task.Manifest.Delivery.MessageTemplate = "{{data.price}}"
	run := makeRun(0, "success", "All good")
	results := svc.Deliver(task, run, profiles)

	if len(results) != 1 || results[0].Status != "skipped" {
		t.Fatalf("expected skipped delivery result for template render error, got %+v", results)
	}
	if len(mock.sentMessages) != 0 {
		t.Fatalf("expected no messages, got %q", mock.sentMessages)
	}
}

func TestDeliver_ProfileNotFound(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	task := makeTask([]string{"nonexistent"}, []string{"success"})
	run := makeRun(0, "success", "done")
	results := svc.Deliver(task, run, []models.DeliveryProfile{})
	if len(results) != 1 || results[0].Status != "failed" {
		t.Errorf("expected failed for missing profile, got %+v", results)
	}
}

func TestDeliver_MatchesProfileByUniqueNameSlug(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "opaque-id", Name: "My Telegram", DriverType: "mock", Enabled: true},
	}

	task := makeTask([]string{"my-telegram"}, []string{"success"})
	run := makeRun(0, "success", "done")
	results := svc.Deliver(task, run, profiles)
	if len(results) != 1 || results[0].Status != "success" || results[0].ProfileID != "opaque-id" {
		t.Errorf("expected delivery through unique name slug alias, got %+v", results)
	}
}

func TestDeliver_DriverFailure(t *testing.T) {
	mock := &mockDriver{failOnSend: true}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Failing", DriverType: "mock", Enabled: true},
	}

	task := makeTask([]string{"p1"}, []string{"success"})
	run := makeRun(0, "success", "done")
	results := svc.Deliver(task, run, profiles)
	if len(results) != 1 || results[0].Status != "failed" {
		t.Errorf("expected failed for driver error, got %+v", results)
	}
}

func TestBuildMessage_DefaultTemplate(t *testing.T) {
	mock := &mockDriver{}
	svc := NewService(mock)

	profiles := []models.DeliveryProfile{
		{ID: "p1", Name: "Test", DriverType: "mock", Enabled: true},
	}

	task := makeTask([]string{"p1"}, []string{"success"})
	run := makeRun(0, "success", "All good")
	svc.Deliver(task, run, profiles)

	if len(mock.sentMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mock.sentMessages))
	}
	msg := mock.sentMessages[0]
	if len(msg) == 0 {
		t.Error("message should not be empty")
	}
}
