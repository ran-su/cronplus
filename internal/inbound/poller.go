package inbound

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ran-su/cronplus/internal/delivery"
	"github.com/ran-su/cronplus/internal/models"
)

// Poller listens for inbound messages via Telegram long polling.
type Poller struct {
	telegram  *delivery.TelegramDriver
	router    *Router
	profiles  func() []models.DeliveryProfile
	onCommand func(models.CommandRecord)

	mu                 sync.Mutex
	cancel             context.CancelFunc
	running            bool
	clearedCommandMenu map[string]bool
	commandMenuLastTry map[string]time.Time
	wg                 sync.WaitGroup
}

// NewPoller creates a new Telegram inbound poller.
func NewPoller(
	telegram *delivery.TelegramDriver,
	router *Router,
	profilesFn func() []models.DeliveryProfile,
	onCommand func(models.CommandRecord),
) *Poller {
	return &Poller{
		telegram:           telegram,
		router:             router,
		profiles:           profilesFn,
		onCommand:          onCommand,
		clearedCommandMenu: make(map[string]bool),
		commandMenuLastTry: make(map[string]time.Time),
	}
}

// Start begins polling for inbound messages. Call Stop() to terminate.
func (p *Poller) Start() {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.wg.Add(1)
	p.mu.Unlock()

	go func() {
		defer p.wg.Done()
		p.run(ctx)
	}()
}

// Stop terminates the polling loop.
func (p *Poller) Stop() {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	cancel := p.cancel
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	p.wg.Wait()

	p.mu.Lock()
	p.cancel = nil
	p.running = false
	p.mu.Unlock()
}

// IsRunning returns whether the poller is active.
func (p *Poller) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// Restart re-evaluates profiles and restarts polling if needed.
func (p *Poller) Restart() {
	p.Stop()
	// Check if any profile has inbound commands enabled
	for _, profile := range p.profiles() {
		if profile.DriverType == "telegram" && profile.InboundCommandsEnabled && profile.Enabled {
			p.Start()
			return
		}
	}
}

func (p *Poller) run(ctx context.Context) {
	log.Println("[CronPlus] Inbound command poller starting...")

	// Rate limiter: track commands per chat per minute
	rateLimiter := newRateLimiter(10, time.Minute)

	for {
		select {
		case <-ctx.Done():
			log.Println("[CronPlus] Inbound command poller stopped.")
			return
		default:
		}

		profilesByToken := activeTelegramProfilesByToken(p.profiles())
		for botToken, profiles := range profilesByToken {
			p.pollToken(ctx, botToken, profiles, rateLimiter)
		}

		// Sleep between poll cycles
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// offsets stores the last processed update_id per bot token
var offsets = struct {
	sync.Mutex
	m map[string]int64
}{m: make(map[string]int64)}

func activeTelegramProfilesByToken(profiles []models.DeliveryProfile) map[string][]models.DeliveryProfile {
	byToken := make(map[string][]models.DeliveryProfile)
	for _, profile := range profiles {
		if profile.DriverType != "telegram" || !profile.InboundCommandsEnabled || !profile.Enabled {
			continue
		}
		botToken := profile.Config["bot_token"]
		if botToken == "" {
			continue
		}
		byToken[botToken] = append(byToken[botToken], profile)
	}
	return byToken
}

func (p *Poller) pollToken(ctx context.Context, botToken string, profiles []models.DeliveryProfile, rl *rateLimiter) {
	p.ensureCommandMenuRemoved(botToken)

	offsets.Lock()
	offset := offsets.m[botToken]
	offsets.Unlock()

	updates, err := p.telegram.GetUpdates(ctx, botToken, offset, 2)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("[CronPlus] Telegram poll error: %v", err)
		return
	}

	for _, update := range updates {
		// Track offset
		offsets.Lock()
		if update.UpdateID >= offsets.m[botToken] {
			offsets.m[botToken] = update.UpdateID + 1
		}
		offsets.Unlock()

		if update.Message != nil && update.Message.Text != "" {
			chatIDStr := strconv.FormatInt(update.Message.Chat.ID, 10)
			if !profileAuthorizedForChat(profiles, chatIDStr) {
				continue
			}
			p.handleMessageUpdate(ctx, botToken, update.Message, chatIDStr, rl)
			continue
		}

		if update.CallbackQuery != nil && update.CallbackQuery.Message != nil && update.CallbackQuery.Data != "" {
			chatIDStr := strconv.FormatInt(update.CallbackQuery.Message.Chat.ID, 10)
			if !profileAuthorizedForChat(profiles, chatIDStr) {
				if err := p.telegram.AnswerCallbackQuery(botToken, update.CallbackQuery.ID); err != nil {
					log.Printf("[CronPlus] Warning: failed to answer unauthorized Telegram callback: %v", err)
				}
				continue
			}
			p.handleCallbackUpdate(ctx, botToken, update.CallbackQuery, chatIDStr, rl)
		}
	}
}

func profileAuthorizedForChat(profiles []models.DeliveryProfile, chatID string) bool {
	for _, profile := range profiles {
		if profileAllowsChat(profile, chatID) {
			return true
		}
	}
	return false
}

func profileAllowsChat(profile models.DeliveryProfile, chatID string) bool {
	chatID = strings.TrimSpace(chatID)
	if len(profile.AuthorizedChatIDs) > 0 {
		for _, id := range profile.AuthorizedChatIDs {
			if strings.TrimSpace(id) == chatID {
				return true
			}
		}
		return false
	}
	return strings.TrimSpace(profile.Config["chat_id"]) == chatID
}

func (p *Poller) handleMessageUpdate(ctx context.Context, botToken string, message *delivery.TelegramMessage, chatIDStr string, rl *rateLimiter) {
	senderID := ""
	if message.From != nil {
		senderID = strconv.FormatInt(message.From.ID, 10)
	}
	p.handleCommand(ctx, botToken, chatIDStr, message.Text, senderID, time.Unix(message.Date, 0), rl)
}

func (p *Poller) handleCallbackUpdate(ctx context.Context, botToken string, callback *delivery.TelegramCallbackQuery, chatIDStr string, rl *rateLimiter) {
	if err := p.telegram.AnswerCallbackQuery(botToken, callback.ID); err != nil {
		log.Printf("[CronPlus] Warning: failed to answer Telegram callback: %v", err)
	}

	senderID := ""
	if callback.From != nil {
		senderID = strconv.FormatInt(callback.From.ID, 10)
	}
	receivedAt := time.Now()
	if callback.Message != nil && callback.Message.Date > 0 {
		receivedAt = time.Unix(callback.Message.Date, 0)
	}
	p.handleCommand(ctx, botToken, chatIDStr, callback.Data, senderID, receivedAt, rl)
}

func (p *Poller) handleCommand(ctx context.Context, botToken, chatIDStr, rawText, senderID string, receivedAt time.Time, rl *rateLimiter) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	// Rate limiting
	if !rl.allow(chatIDStr) {
		if err := p.telegram.SendReply(botToken, chatIDStr,
			"⚠️ Rate limited. Max 10 commands per minute."); err != nil {
			log.Printf("[CronPlus] Warning: failed to send Telegram rate-limit reply: %v", err)
		}
		return
	}

	// Route the command
	msg := models.InboundMessage{
		ChannelType: "telegram",
		SenderID:    senderID,
		ChatID:      chatIDStr,
		RawText:     rawText,
		ReceivedAt:  receivedAt,
	}

	reply := p.router.Route(msg)

	// Log the command
	record := models.CommandRecord{
		ID:             fmt.Sprintf("%d", time.Now().UnixNano()),
		ChannelType:    "telegram",
		ChatID:         chatIDStr,
		CommandText:    rawText,
		MatchedCommand: extractCommand(rawText),
		ReceivedAt:     msg.ReceivedAt,
	}

	if reply != nil {
		record.ReplyText = reply.Text
		if err := p.telegram.SendReplyWithOptions(botToken, chatIDStr, *reply); err != nil {
			record.Error = "reply send failed: " + err.Error()
			log.Printf("[CronPlus] Warning: failed to send Telegram command reply: %v", err)
		}
	}

	if p.onCommand != nil {
		p.onCommand(record)
	}
}

const commandMenuRetryInterval = 30 * time.Minute

func (p *Poller) ensureCommandMenuRemoved(botToken string) {
	now := time.Now()

	p.mu.Lock()
	if p.clearedCommandMenu[botToken] {
		p.mu.Unlock()
		return
	}
	if lastTry, ok := p.commandMenuLastTry[botToken]; ok && now.Sub(lastTry) < commandMenuRetryInterval {
		p.mu.Unlock()
		return
	}
	p.commandMenuLastTry[botToken] = now
	p.mu.Unlock()

	if err := p.telegram.DeleteCommandMenu(botToken); err != nil {
		log.Printf("[CronPlus] Warning: failed to clear Telegram command menu: %v", err)
		return
	}

	p.mu.Lock()
	p.clearedCommandMenu[botToken] = true
	p.mu.Unlock()
}

func extractCommand(text string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return ""
	}
	return normalizeCommand(parts[0])
}

// --- Rate Limiter ---

type rateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	counters map[string][]time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		limit:    limit,
		window:   window,
		counters: make(map[string][]time.Time),
	}
}

func (r *rateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	// Remove expired entries
	times := r.counters[key]
	valid := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= r.limit {
		r.counters[key] = valid
		return false
	}

	r.counters[key] = append(valid, now)
	return true
}
