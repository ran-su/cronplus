package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

// TelegramDriver sends messages via the Telegram Bot API.
type TelegramDriver struct {
	client  *http.Client
	apiBase string
}

// NewTelegramDriver creates a new Telegram delivery driver.
func NewTelegramDriver() *TelegramDriver {
	return &TelegramDriver{
		client:  &http.Client{Timeout: 15 * time.Second},
		apiBase: telegramAPIBaseURL,
	}
}

func (t *TelegramDriver) Type() string { return "telegram" }

const telegramAPIBaseURL = "https://api.telegram.org"

// Send sends a message to the configured Telegram chat.
func (t *TelegramDriver) Send(profile models.DeliveryProfile, message string) error {
	botToken := profile.Config["bot_token"]
	chatID := profile.Config["chat_id"]

	if botToken == "" || chatID == "" {
		return fmt.Errorf("telegram profile %s is missing bot_token or chat_id", profile.Name)
	}

	return t.sendMessage(botToken, chatID, message, nil)
}

// SendReply sends a reply message on the given chat (used by inbound commands).
func (t *TelegramDriver) SendReply(botToken, chatID, message string) error {
	return t.SendReplyWithOptions(botToken, chatID, models.OutboundReply{Text: message})
}

// SendReplyWithOptions sends an inbound-command reply with optional inline actions.
func (t *TelegramDriver) SendReplyWithOptions(botToken, chatID string, reply models.OutboundReply) error {
	return t.sendMessage(botToken, chatID, reply.Text, reply.InlineActions)
}

func (t *TelegramDriver) sendMessage(botToken, chatID, text string, inlineActions [][]models.ReplyAction) error {
	url := fmt.Sprintf("%s/bot%s/sendMessage", t.apiBase, botToken)

	body := map[string]any{
		"chat_id": chatID,
		"text":    truncateTelegramMessage(text),
	}
	if keyboard := telegramInlineKeyboard(inlineActions); len(keyboard) > 0 {
		body["reply_markup"] = map[string]any{
			"inline_keyboard": keyboard,
		}
	}

	return t.postJSON(url, body, "telegram API")
}

// SetCommandMenu configures Telegram's slash-command picker for this bot token.
func (t *TelegramDriver) SetCommandMenu(botToken string) error {
	url := fmt.Sprintf("%s/bot%s/setMyCommands", t.apiBase, botToken)
	body := map[string]any{
		"commands": []map[string]string{
			{"command": "status", "description": "App health summary"},
			{"command": "list", "description": "List tasks"},
			{"command": "help", "description": "Show command help"},
			{"command": "run", "description": "Run a task by slug"},
			{"command": "last", "description": "Show latest task result"},
			{"command": "enable", "description": "Enable a task"},
			{"command": "disable", "description": "Disable a task"},
		},
	}
	return t.postJSON(url, body, "setMyCommands")
}

// AnswerCallbackQuery acknowledges an inline keyboard callback press.
func (t *TelegramDriver) AnswerCallbackQuery(botToken, callbackQueryID string) error {
	url := fmt.Sprintf("%s/bot%s/answerCallbackQuery", t.apiBase, botToken)
	body := map[string]any{
		"callback_query_id": callbackQueryID,
	}
	return t.postJSON(url, body, "answerCallbackQuery")
}

func (t *TelegramDriver) postJSON(url string, body map[string]any, description string) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal %s request: %w", description, err)
	}

	resp, err := t.client.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("%s request failed: %w", description, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("%s returned %d: %s", description, resp.StatusCode, string(respBody))
	}

	return nil
}

const telegramCallbackDataMaxBytes = 64

func telegramInlineKeyboard(rows [][]models.ReplyAction) [][]map[string]string {
	keyboard := make([][]map[string]string, 0, len(rows))
	for _, row := range rows {
		buttonRow := make([]map[string]string, 0, len(row))
		for _, action := range row {
			if action.Label == "" || action.Command == "" {
				continue
			}
			if len([]byte(action.Command)) > telegramCallbackDataMaxBytes {
				continue
			}
			buttonRow = append(buttonRow, map[string]string{
				"text":          action.Label,
				"callback_data": action.Command,
			})
		}
		if len(buttonRow) > 0 {
			keyboard = append(keyboard, buttonRow)
		}
	}
	return keyboard
}

// GetUpdates fetches new messages from the Telegram Bot API (for inbound commands).
func (t *TelegramDriver) GetUpdates(ctx context.Context, botToken string, offset int64, timeoutSec int) ([]TelegramUpdate, error) {
	url := fmt.Sprintf("%s/bot%s/getUpdates?offset=%d&timeout=%d", t.apiBase,
		botToken, offset, timeoutSec)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build getUpdates request: %w", err)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getUpdates failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool             `json:"ok"`
		Result []TelegramUpdate `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode getUpdates response: %w", err)
	}

	if !result.OK {
		return nil, fmt.Errorf("getUpdates returned ok=false")
	}

	return result.Result, nil
}

const telegramMessageMaxRunes = 3900

func truncateTelegramMessage(text string) string {
	runes := []rune(text)
	if len(runes) <= telegramMessageMaxRunes {
		return text
	}

	suffix := "\n[truncated]"
	suffixLen := len([]rune(suffix))
	limit := telegramMessageMaxRunes - suffixLen
	if limit < 0 {
		limit = 0
	}
	return string(runes[:limit]) + suffix
}

// TelegramUpdate represents a Telegram Bot API update.
type TelegramUpdate struct {
	UpdateID      int64                  `json:"update_id"`
	Message       *TelegramMessage       `json:"message"`
	CallbackQuery *TelegramCallbackQuery `json:"callback_query"`
}

// TelegramMessage represents a message in a Telegram update.
type TelegramMessage struct {
	MessageID int64         `json:"message_id"`
	From      *TelegramUser `json:"from"`
	Chat      TelegramChat  `json:"chat"`
	Text      string        `json:"text"`
	Date      int64         `json:"date"`
}

// TelegramUser represents a Telegram user.
type TelegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// TelegramChat represents a Telegram chat.
type TelegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// TelegramCallbackQuery represents a callback from an inline keyboard button.
type TelegramCallbackQuery struct {
	ID      string           `json:"id"`
	From    *TelegramUser    `json:"from"`
	Message *TelegramMessage `json:"message"`
	Data    string           `json:"data"`
}
