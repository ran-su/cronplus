package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
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

func TestSendReplyWithOptionsIncludesInlineKeyboard(t *testing.T) {
	driver, transport := testTelegramDriver(`{"ok":true}`)

	err := driver.SendReplyWithOptions("token", "123", models.OutboundReply{
		Text: "Choose an option",
		InlineActions: [][]models.ReplyAction{
			{
				{Label: "Status", Command: "/status"},
				{Label: "Tasks", Command: "/list"},
			},
			{
				{Label: "Run daily-report", Command: "/run daily-report"},
				{Label: "Last", Command: "/last daily-report"},
			},
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
	if _, ok := replyMarkup["keyboard"]; ok {
		t.Fatalf("reply markup includes persistent keyboard: %+v", replyMarkup)
	}
	keyboard := replyMarkup["inline_keyboard"].([]any)
	firstRow := keyboard[0].([]any)
	firstButton := firstRow[0].(map[string]any)
	if firstButton["text"] != "Status" {
		t.Fatalf("first button = %+v, want Status label", firstButton)
	}
	if firstButton["callback_data"] != "/status" {
		t.Fatalf("first button = %+v, want /status callback", firstButton)
	}
}

func TestAnswerCallbackQueryPostsAcknowledgement(t *testing.T) {
	driver, transport := testTelegramDriver(`{"ok":true}`)

	if err := driver.AnswerCallbackQuery("token", "callback-123"); err != nil {
		t.Fatalf("AnswerCallbackQuery: %v", err)
	}

	call := transport.calls[0]
	if call.Path != "/bottoken/answerCallbackQuery" {
		t.Fatalf("request path = %q, want /bottoken/answerCallbackQuery", call.Path)
	}
	if call.Body["callback_query_id"] != "callback-123" {
		t.Fatalf("body = %+v, want callback id", call.Body)
	}
}

func TestSendReplyKeyboardRemovalRemovesPersistentKeyboard(t *testing.T) {
	driver, transport := testTelegramDriver(`{"ok":true}`)

	if err := driver.SendReplyKeyboardRemoval("token", "123", "Removing old shortcuts"); err != nil {
		t.Fatalf("SendReplyKeyboardRemoval: %v", err)
	}

	call := transport.calls[0]
	if call.Path != "/bottoken/sendMessage" {
		t.Fatalf("request path = %q, want /bottoken/sendMessage", call.Path)
	}
	replyMarkup, ok := call.Body["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("reply_markup missing from body: %+v", call.Body)
	}
	if replyMarkup["remove_keyboard"] != true {
		t.Fatalf("reply markup = %+v, want remove_keyboard=true", replyMarkup)
	}
	if _, ok := replyMarkup["inline_keyboard"]; ok {
		t.Fatalf("reply markup includes inline keyboard during removal: %+v", replyMarkup)
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
	query, err := url.ParseQuery(call.Query)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if query.Get("offset") != "7" || query.Get("timeout") != "2" {
		t.Fatalf("query = %q, want offset=7&timeout=2", call.Query)
	}
	if query.Get("allowed_updates") != `["message","callback_query"]` {
		t.Fatalf("allowed_updates = %q, want message and callback_query", query.Get("allowed_updates"))
	}
	if len(updates) != 1 || updates[0].UpdateID != 42 {
		t.Fatalf("updates = %+v, want one update", updates)
	}
}

func TestGetUpdatesDecodesCallbackQuery(t *testing.T) {
	driver, _ := testTelegramDriver(`{"ok":true,"result":[{"update_id":43,"callback_query":{"id":"cb-1","from":{"id":99},"message":{"chat":{"id":123,"type":"private"},"date":1717286400},"data":"/last daily-report"}}]}`)

	updates, err := driver.GetUpdates(context.Background(), "token", 0, 2)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(updates) != 1 || updates[0].CallbackQuery == nil {
		t.Fatalf("updates = %+v, want callback query", updates)
	}
	callback := updates[0].CallbackQuery
	if callback.ID != "cb-1" || callback.Data != "/last daily-report" || callback.Message.Chat.ID != 123 {
		t.Fatalf("callback = %+v, want decoded callback query", callback)
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
