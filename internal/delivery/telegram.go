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
	client *http.Client
}

// NewTelegramDriver creates a new Telegram delivery driver.
func NewTelegramDriver() *TelegramDriver {
	return &TelegramDriver{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (t *TelegramDriver) Type() string { return "telegram" }

// Send sends a message to the configured Telegram chat.
func (t *TelegramDriver) Send(profile models.DeliveryProfile, message string) error {
	botToken := profile.Config["bot_token"]
	chatID := profile.Config["chat_id"]

	if botToken == "" || chatID == "" {
		return fmt.Errorf("telegram profile %s is missing bot_token or chat_id", profile.Name)
	}

	return t.sendMessage(botToken, chatID, message)
}

// SendReply sends a reply message on the given chat (used by inbound commands).
func (t *TelegramDriver) SendReply(botToken, chatID, message string) error {
	return t.sendMessage(botToken, chatID, message)
}

func (t *TelegramDriver) sendMessage(botToken, chatID, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)

	body := map[string]any{
		"chat_id": chatID,
		"text":    truncateTelegramMessage(text),
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	resp, err := t.client.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("telegram API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("telegram API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// GetUpdates fetches new messages from the Telegram Bot API (for inbound commands).
func (t *TelegramDriver) GetUpdates(ctx context.Context, botToken string, offset int64, timeoutSec int) ([]TelegramUpdate, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=%d",
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
	UpdateID int64            `json:"update_id"`
	Message  *TelegramMessage `json:"message"`
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
