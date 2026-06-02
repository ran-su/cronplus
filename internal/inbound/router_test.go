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

func TestRouteIncludesPresetOptions(t *testing.T) {
	router := NewRouter(CommandContext{
		GetTasks: func() []*models.Task {
			return []*models.Task{
				{DisplayName: "Daily Report"},
			}
		},
		NextRunTime: func(task *models.Task) *time.Time {
			return nil
		},
		GetRunHistory: func(taskID string) []models.RunRecord {
			return nil
		},
	})

	reply := router.Route(models.InboundMessage{RawText: "/help"})
	if reply == nil {
		t.Fatal("reply is nil")
	}
	if !presetContains(reply.PresetOptions, "/status") || !presetContains(reply.PresetOptions, "/list") {
		t.Fatalf("preset options = %+v, want status and list buttons", reply.PresetOptions)
	}
	if !presetContains(reply.PresetOptions, "/run daily-report") || !presetContains(reply.PresetOptions, "/last daily-report") {
		t.Fatalf("preset options = %+v, want task run/last buttons", reply.PresetOptions)
	}
}

func presetContains(rows [][]string, want string) bool {
	for _, row := range rows {
		for _, got := range row {
			if got == want {
				return true
			}
		}
	}
	return false
}
