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
