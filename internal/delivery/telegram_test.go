package delivery

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateTelegramMessageCapsLongText(t *testing.T) {
	msg := strings.Repeat("x", telegramMessageMaxRunes+100)
	got := truncateTelegramMessage(msg)
	if len([]rune(got)) != telegramMessageMaxRunes {
		t.Fatalf("truncated length = %d, want %d", len([]rune(got)), telegramMessageMaxRunes)
	}
	if !strings.HasSuffix(got, "\n[truncated]") {
		t.Fatalf("truncated message should include suffix")
	}
}

func TestTruncateTelegramMessageIsRuneSafe(t *testing.T) {
	msg := strings.Repeat("界", telegramMessageMaxRunes+1)
	got := truncateTelegramMessage(msg)
	if !utf8.ValidString(got) {
		t.Fatal("truncated message is not valid UTF-8")
	}
	if len([]rune(got)) != telegramMessageMaxRunes {
		t.Fatalf("truncated length = %d, want %d", len([]rune(got)), telegramMessageMaxRunes)
	}
}
