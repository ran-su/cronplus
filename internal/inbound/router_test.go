package inbound

import (
	"strings"
	"testing"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

func TestRouteAcceptsTelegramBotCommandSuffix(t *testing.T) {
	router := NewRouter(CommandContext{
		GetTasks: func() []*models.Task { return nil },
		NextRunTime: func(task *models.Task) *time.Time {
			return nil
		},
	})

	reply := router.Route(models.InboundMessage{RawText: "/status@CronPlusBot"})
	if reply == nil {
		t.Fatal("reply is nil")
	}
	if strings.Contains(reply.Text, "Unknown command") {
		t.Fatalf("reply = %q, expected status command to be recognized", reply.Text)
	}
}

func TestExtractCommandNormalizesTelegramBotSuffix(t *testing.T) {
	if got := extractCommand("/run@CronPlusBot task-one"); got != "/run" {
		t.Fatalf("extractCommand = %q, want /run", got)
	}
}

func TestRouteIncludesContextualInlineActions(t *testing.T) {
	router := NewRouter(CommandContext{
		GetTasks: func() []*models.Task {
			return []*models.Task{
				{ID: "task-1", DisplayName: "Daily Report", Enabled: true},
			}
		},
		NextRunTime: func(task *models.Task) *time.Time {
			return nil
		},
		GetRunHistory: func(taskID string) []models.RunRecord {
			return []models.RunRecord{{TaskID: taskID, FinishedAt: time.Now(), Outcome: models.RunOutcome{ExitCode: 0}}}
		},
		TriggerRun: func(taskID, trigger string) error {
			return nil
		},
	})

	reply := router.Route(models.InboundMessage{RawText: "/help"})
	if reply == nil {
		t.Fatal("reply is nil")
	}
	if !inlineContains(reply.InlineActions, "/status") || !inlineContains(reply.InlineActions, "/list") {
		t.Fatalf("inline actions = %+v, want status and list buttons", reply.InlineActions)
	}
	if inlineContains(reply.InlineActions, "/help") {
		t.Fatalf("inline actions = %+v, should not include command that produced help", reply.InlineActions)
	}

	reply = router.Route(models.InboundMessage{RawText: "/status"})
	if reply == nil {
		t.Fatal("reply is nil")
	}
	if inlineContains(reply.InlineActions, "/status") {
		t.Fatalf("inline actions = %+v, should not include command that produced status", reply.InlineActions)
	}

	reply = router.Route(models.InboundMessage{RawText: "/list"})
	if reply == nil {
		t.Fatal("reply is nil")
	}
	if !inlineContains(reply.InlineActions, "/run daily-report") || !inlineContains(reply.InlineActions, "/last daily-report") {
		t.Fatalf("inline actions = %+v, want task run/last buttons", reply.InlineActions)
	}

	reply = router.Route(models.InboundMessage{RawText: "/run daily-report"})
	if reply == nil {
		t.Fatal("reply is nil")
	}
	if inlineContains(reply.InlineActions, "/run daily-report") {
		t.Fatalf("inline actions = %+v, should not include command that produced run response", reply.InlineActions)
	}
	if !inlineContains(reply.InlineActions, "/last daily-report") {
		t.Fatalf("inline actions = %+v, want last button after run", reply.InlineActions)
	}

	reply = router.Route(models.InboundMessage{RawText: "/last daily-report"})
	if reply == nil {
		t.Fatal("reply is nil")
	}
	if inlineContains(reply.InlineActions, "/last daily-report") {
		t.Fatalf("inline actions = %+v, should not include command that produced last response", reply.InlineActions)
	}
	if !inlineContains(reply.InlineActions, "/run daily-report") {
		t.Fatalf("inline actions = %+v, want run button after last", reply.InlineActions)
	}
}

func inlineContains(rows [][]models.ReplyAction, want string) bool {
	for _, row := range rows {
		for _, got := range row {
			if got.Command == want {
				return true
			}
		}
	}
	return false
}
