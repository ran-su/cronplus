package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ran-su/cronplus/internal/models"
)

type capturedTelegramRequest struct {
	Method string
	Path   string
	Query  string
	Body   map[string]any
}

type captureTransport struct {
	status   int
	response string
	calls    []capturedTelegramRequest
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]any
	if req.Body != nil {
		defer req.Body.Close()
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil && err != io.EOF {
			return nil, err
		}
	}
	c.calls = append(c.calls, capturedTelegramRequest{
		Method: req.Method,
		Path:   req.URL.Path,
		Query:  req.URL.RawQuery,
		Body:   body,
	})

	return &http.Response{
		StatusCode: c.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(c.response)),
		Request:    req,
	}, nil
}

func testTelegramDriver(response string) (*TelegramDriver, *captureTransport) {
	transport := &captureTransport{status: http.StatusOK, response: response}
	driver := NewTelegramDriver()
	driver.apiBase = "https://telegram.test"
	driver.client = &http.Client{Transport: transport}
	return driver, transport
}

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

func TestSendReplyWithOptionsIncludesReplyKeyboard(t *testing.T) {
	driver, transport := testTelegramDriver(`{"ok":true}`)

	err := driver.SendReplyWithOptions("token", "123", models.OutboundReply{
		Text: "Choose an option",
		PresetOptions: [][]string{
			{"/status", "/list"},
			{"/run daily-report", "/last daily-report"},
		},
	})
	if err != nil {
		t.Fatalf("SendReplyWithOptions: %v", err)
	}

	call := transport.calls[0]
	if call.Path != "/bottoken/sendMessage" {
		t.Fatalf("request path = %q, want /bottoken/sendMessage", call.Path)
	}
	replyMarkup, ok := call.Body["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("reply_markup missing from body: %+v", call.Body)
	}
	if replyMarkup["resize_keyboard"] != true || replyMarkup["is_persistent"] != true {
		t.Fatalf("reply markup flags = %+v, want persistent resized keyboard", replyMarkup)
	}
	keyboard := replyMarkup["keyboard"].([]any)
	firstRow := keyboard[0].([]any)
	firstButton := firstRow[0].(map[string]any)
	if firstButton["text"] != "/status" {
		t.Fatalf("first button = %+v, want /status", firstButton)
	}
}

func TestGetUpdatesUsesConfiguredAPIBase(t *testing.T) {
	driver, transport := testTelegramDriver(`{"ok":true,"result":[{"update_id":42,"message":{"chat":{"id":123,"type":"private"},"text":"/status","date":1717286400}}]}`)

	updates, err := driver.GetUpdates(context.Background(), "token", 7, 2)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	call := transport.calls[0]
	if call.Path != "/bottoken/getUpdates" {
		t.Fatalf("request path = %q, want /bottoken/getUpdates", call.Path)
	}
	if call.Query != "offset=7&timeout=2" {
		t.Fatalf("query = %q, want offset=7&timeout=2", call.Query)
	}
	if len(updates) != 1 || updates[0].UpdateID != 42 {
		t.Fatalf("updates = %+v, want one update", updates)
	}
}

func TestSetCommandMenuPostsTelegramCommands(t *testing.T) {
	driver, transport := testTelegramDriver(`{"ok":true}`)

	if err := driver.SetCommandMenu("token"); err != nil {
		t.Fatalf("SetCommandMenu: %v", err)
	}

	call := transport.calls[0]
	if call.Path != "/bottoken/setMyCommands" {
		t.Fatalf("request path = %q, want /bottoken/setMyCommands", call.Path)
	}
	commands := call.Body["commands"].([]any)
	if len(commands) < 3 {
		t.Fatalf("commands = %+v, want command menu entries", commands)
	}
	first := commands[0].(map[string]any)
	if first["command"] != "status" {
		t.Fatalf("first command = %+v, want status", first)
	}
}
