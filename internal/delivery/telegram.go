package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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

// DeleteCommandMenu clears Telegram's slash-command picker for this bot token.
func (t *TelegramDriver) DeleteCommandMenu(botToken string) error {
	url := fmt.Sprintf("%s/bot%s/deleteMyCommands", t.apiBase, botToken)
	return t.postJSON(url, map[string]any{}, "deleteMyCommands")
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

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d: %s", description, resp.StatusCode, telegramResponseMessage(respBody))
	}

	var result telegramAPIResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("%s returned invalid JSON: %w", description, err)
	}
	if !result.OK {
		return fmt.Errorf("%s returned ok=false: %s", description, result.message())
	}

	return nil
}

type telegramAPIResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code,omitempty"`
	Description string `json:"description,omitempty"`
}

func (r telegramAPIResponse) message() string {
	if r.Description != "" {
		if r.ErrorCode != 0 {
			return fmt.Sprintf("%d %s", r.ErrorCode, r.Description)
		}
		return r.Description
	}
	if r.ErrorCode != 0 {
		return fmt.Sprintf("error_code=%d", r.ErrorCode)
	}
	return "no description"
}

func telegramResponseMessage(body []byte) string {
	var result telegramAPIResponse
	if err := json.Unmarshal(body, &result); err == nil && (result.Description != "" || result.ErrorCode != 0) {
		return result.message()
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "empty response"
	}
	return text
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
	endpoint, err := url.Parse(fmt.Sprintf("%s/bot%s/getUpdates", t.apiBase, botToken))
	if err != nil {
		return nil, fmt.Errorf("failed to build getUpdates URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("offset", fmt.Sprintf("%d", offset))
	query.Set("timeout", fmt.Sprintf("%d", timeoutSec))
	query.Set("allowed_updates", `["message","callback_query"]`)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build getUpdates request: %w", err)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getUpdates failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("getUpdates returned %d: %s", resp.StatusCode, telegramResponseMessage(respBody))
	}

	var result struct {
		telegramAPIResponse
		Result []TelegramUpdate `json:"result"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode getUpdates response: %w", err)
	}

	if !result.OK {
		return nil, fmt.Errorf("getUpdates returned ok=false: %s", result.message())
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
