// Package telegram implements channel.Channel for Telegram groups using the Bot API.
//
// One incident = one root message (the card) edited in place; timeline entries are
// posted as replies to the card. Replies and button presses come back through
// long polling (singleton, run under a Postgres lease by the core) or a webhook.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"

	"github.com/xonoko/infra-monitor/internal/channel"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

const (
	defaultAPIURL = "https://api.telegram.org"
	maxRetries    = 3
)

// Channel is a Telegram channel instance.
type Channel struct {
	cfg       config.ChannelConfig
	publicURL string
	log       *slog.Logger
	bot       *bot.Bot
	apiURL    string

	httpClient *http.Client // traced client for Bot API calls
	pollClient *http.Client // long-poll client (untraced: a span every 30s is noise)

	global     *rate.Limiter
	mu         sync.Mutex
	perChat    map[int64]*rate.Limiter
	perChatLim rate.Limit
	perChatBur int

	pollTimeout     time.Duration
	conflictBackoff time.Duration
	errorBackoff    time.Duration
	maxJitter       time.Duration

	webhookWorkers int
	webhookOnce    sync.Once
	webhookQueue   chan webhookJob
}

// Option customizes a Channel (mostly for tests).
type Option func(*Channel)

// WithHTTPClient replaces the traced HTTP client used for Bot API calls.
func WithHTTPClient(c *http.Client) Option { return func(ch *Channel) { ch.httpClient = c } }

// WithPollClient replaces the long-poll HTTP client.
func WithPollClient(c *http.Client) Option { return func(ch *Channel) { ch.pollClient = c } }

// WithPollTimeout sets the getUpdates long-poll timeout.
func WithPollTimeout(d time.Duration) Option { return func(ch *Channel) { ch.pollTimeout = d } }

// WithBackoff sets the waits after a 409 Conflict and after other polling errors.
func WithBackoff(conflict, errs time.Duration) Option {
	return func(ch *Channel) { ch.conflictBackoff, ch.errorBackoff = conflict, errs }
}

// WithMaxJitter sets the random extra wait added to retry_after.
func WithMaxJitter(d time.Duration) Option { return func(ch *Channel) { ch.maxJitter = d } }

// WithGlobalRate sets the global send rate (default 25/s).
func WithGlobalRate(perSecond float64, burst int) Option {
	return func(ch *Channel) { ch.global = rate.NewLimiter(rate.Limit(perSecond), burst) }
}

// WithWebhookWorkers sets the number of goroutines dispatching webhook updates.
func WithWebhookWorkers(n int) Option { return func(ch *Channel) { ch.webhookWorkers = n } }

// New creates a Telegram channel. It does not contact Telegram.
func New(cfg config.ChannelConfig, publicURL string, log *slog.Logger, opts ...Option) (*Channel, error) {
	if strings.TrimSpace(cfg.BotToken) == "" {
		return nil, fmt.Errorf("telegram channel %q: bot token is required", cfg.Name)
	}
	if cfg.ChatID == 0 {
		return nil, fmt.Errorf("telegram channel %q: chat_id is required", cfg.Name)
	}
	if cfg.Mode == "" {
		cfg.Mode = "polling"
	}
	mpm := cfg.MessagesPerMinute
	if mpm <= 0 {
		mpm = 18
	}
	ch := &Channel{
		cfg:             cfg,
		publicURL:       strings.TrimRight(publicURL, "/"),
		log:             log.With("channel", cfg.Name, "channel_type", "telegram"),
		apiURL:          strings.TrimRight(cfg.APIURL, "/"),
		global:          rate.NewLimiter(25, 25),
		perChat:         map[int64]*rate.Limiter{},
		perChatLim:      rate.Limit(float64(mpm) / 60.0),
		perChatBur:      3,
		pollTimeout:     30 * time.Second,
		conflictBackoff: 30 * time.Second,
		errorBackoff:    5 * time.Second,
		maxJitter:       500 * time.Millisecond,
		webhookWorkers:  4,
	}
	if ch.apiURL == "" {
		ch.apiURL = defaultAPIURL
	}
	for _, o := range opts {
		o(ch)
	}
	if ch.httpClient == nil {
		ch.httpClient = telemetry.HTTPClient(30 * time.Second)
	}
	if ch.pollClient == nil {
		ch.pollClient = &http.Client{Timeout: ch.pollTimeout + 15*time.Second}
	}
	b, err := bot.New(cfg.BotToken,
		bot.WithSkipGetMe(),
		bot.WithServerURL(ch.apiURL),
		bot.WithHTTPClient(ch.pollTimeout, ch.httpClient),
		bot.WithErrorsHandler(func(err error) { ch.log.Warn("telegram client error", "error", ch.redact(err)) }),
		bot.WithDebugHandler(func(string, ...any) {}),
	)
	if err != nil {
		return nil, fmt.Errorf("telegram channel %q: %w", cfg.Name, ch.redact(err))
	}
	ch.bot = b
	return ch, nil
}

func (c *Channel) Name() string { return c.cfg.Name }
func (c *Channel) Type() string { return "telegram" }

func (c *Channel) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		Buttons:           true,
		URLButtons:        true,
		EditMessages:      true,
		Threads:           true,
		Typing:            true,
		MaxMessageLen:     MaxMessageLen,
		Markup:            "telegram-html",
		SingletonReceiver: c.cfg.Mode == "polling",
	}
}

// redactedError hides the bot token while keeping errors.Is/As working.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

func (c *Channel) redact(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if c.cfg.BotToken != "" && strings.Contains(msg, c.cfg.BotToken) {
		return &redactedError{msg: strings.ReplaceAll(msg, c.cfg.BotToken, "***"), err: err}
	}
	return err
}

func (c *Channel) chatLimiter(chatID int64) *rate.Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.perChat[chatID]
	if !ok {
		l = rate.NewLimiter(c.perChatLim, c.perChatBur)
		c.perChat[chatID] = l
	}
	return l
}

// call runs one Bot API method with a span, rate limiting (when limited) and 429 retries.
func (c *Channel) call(ctx context.Context, method string, chatID int64, limited bool, fn func(ctx context.Context) error) error {
	ctx, span := telemetry.Tracer().Start(ctx, "telegram."+method, trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("channel.name", c.cfg.Name), attribute.Int64("telegram.chat_id", chatID)))
	defer span.End()
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if limited {
			if err = c.chatLimiter(chatID).Wait(ctx); err != nil {
				break
			}
		}
		if err = c.global.Wait(ctx); err != nil {
			break
		}
		err = fn(ctx)
		var tooMany *bot.TooManyRequestsError
		if err == nil || !errors.As(err, &tooMany) || attempt == maxRetries {
			break
		}
		wait := time.Duration(tooMany.RetryAfter) * time.Second
		if c.maxJitter > 0 {
			wait += time.Duration(rand.Int64N(int64(c.maxJitter))) //nolint:gosec // jitter only
		}
		span.AddEvent("retry_after", trace.WithAttributes(attribute.Int("retry_after", tooMany.RetryAfter)))
		c.log.WarnContext(ctx, "telegram rate limited, retrying", "method", method, "retry_after", tooMany.RetryAfter)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			err = ctx.Err()
			attempt = maxRetries
		case <-t.C:
		}
	}
	var migrated *bot.MigrateError
	if errors.As(err, &migrated) {
		c.log.ErrorContext(ctx, "telegram group was upgraded to a supergroup: update chat_id in the config",
			"old_chat_id", chatID, "new_chat_id", migrated.MigrateToChatID)
	}
	err = c.redact(err)
	telemetry.RecordError(span, err)
	return err
}

func (c *Channel) chatID(s string) int64 {
	if s == "" {
		return c.cfg.ChatID
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return c.cfg.ChatID
	}
	return id
}

var noPreview = &models.LinkPreviewOptions{IsDisabled: bot.True()}

func (c *Channel) send(ctx context.Context, chatID int64, text string, replyTo int, kb *models.InlineKeyboardMarkup) (*models.Message, error) {
	p := &bot.SendMessageParams{ChatID: chatID, Text: text, ParseMode: models.ParseModeHTML, LinkPreviewOptions: noPreview}
	if replyTo > 0 {
		p.ReplyParameters = &models.ReplyParameters{MessageID: replyTo, AllowSendingWithoutReply: true}
	}
	if kb != nil && len(kb.InlineKeyboard) > 0 {
		p.ReplyMarkup = kb
	}
	var msg *models.Message
	err := c.call(ctx, "sendMessage", chatID, true, func(ctx context.Context) error {
		var err error
		msg, err = c.bot.SendMessage(ctx, p)
		return err
	})
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, errors.New("telegram: empty sendMessage result")
	}
	return msg, nil
}

func (c *Channel) edit(ctx context.Context, chatID int64, messageID int, text string, kb *models.InlineKeyboardMarkup) error {
	if kb == nil {
		kb = Keyboard(nil)
	}
	p := &bot.EditMessageTextParams{ChatID: chatID, MessageID: messageID, Text: text, ParseMode: models.ParseModeHTML,
		LinkPreviewOptions: noPreview, ReplyMarkup: kb}
	err := c.call(ctx, "editMessageText", chatID, true, func(ctx context.Context) error {
		_, err := c.bot.EditMessageText(ctx, p)
		return err
	})
	if isNotModified(err) {
		return nil
	}
	return err
}

func isNotModified(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "message is not modified")
}

func isMessageGone(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "message to edit not found") || strings.Contains(s, "message_id_invalid")
}

// UpsertCard creates the incident card or edits it in place.
func (c *Channel) UpsertCard(ctx context.Context, thread *channel.ThreadRef, card channel.Card) (channel.ThreadRef, error) {
	text := RenderCard(card)
	kb := Keyboard(card.Buttons)
	if thread != nil && thread.CardMessageID != "" {
		chatID := c.chatID(thread.ChatID)
		mid, err := strconv.Atoi(thread.CardMessageID)
		if err == nil {
			err = c.edit(ctx, chatID, mid, text, kb)
			if err == nil {
				return *thread, nil
			}
			if !isMessageGone(err) {
				return *thread, err
			}
			c.log.WarnContext(ctx, "incident card is gone, sending a new one", "incident_id", card.IncidentID)
		}
	}
	chatID := c.cfg.ChatID
	if thread != nil && thread.ChatID != "" {
		chatID = c.chatID(thread.ChatID)
	}
	msg, err := c.send(ctx, chatID, text, 0, kb)
	if err != nil {
		return channel.ThreadRef{}, err
	}
	id := strconv.Itoa(msg.ID)
	return channel.ThreadRef{ChatID: strconv.FormatInt(msg.Chat.ID, 10), ThreadID: id, CardMessageID: id}, nil
}

// Post replies to the incident thread.
func (c *Channel) Post(ctx context.Context, thread channel.ThreadRef, msg channel.Message) (channel.MessageRef, error) {
	chatID := c.chatID(thread.ChatID)
	replyTo, _ := strconv.Atoi(thread.ThreadID)
	m, err := c.send(ctx, chatID, RenderMessage(msg), replyTo, Keyboard(msg.Buttons))
	if err != nil {
		return channel.MessageRef{}, err
	}
	return channel.MessageRef{ChatID: strconv.FormatInt(m.Chat.ID, 10), MessageID: strconv.Itoa(m.ID)}, nil
}

// Edit replaces a posted message; an empty Buttons list removes the keyboard.
func (c *Channel) Edit(ctx context.Context, ref channel.MessageRef, msg channel.Message) error {
	mid, err := strconv.Atoi(ref.MessageID)
	if err != nil {
		return fmt.Errorf("telegram: invalid message id %q", ref.MessageID)
	}
	return c.edit(ctx, c.chatID(ref.ChatID), mid, RenderMessage(msg), Keyboard(msg.Buttons))
}

// SetTyping shows "typing…" in the chat (best effort, lasts ~5s on Telegram's side).
func (c *Channel) SetTyping(ctx context.Context, thread channel.ThreadRef) error {
	chatID := c.chatID(thread.ChatID)
	return c.call(ctx, "sendChatAction", chatID, false, func(ctx context.Context) error {
		_, err := c.bot.SendChatAction(ctx, &bot.SendChatActionParams{ChatID: chatID, Action: models.ChatActionTyping})
		return err
	})
}

// reply sends a plain response (markdown subset) to a user's message.
func (c *Channel) reply(ctx context.Context, chatID int64, replyTo int, md string) {
	if strings.TrimSpace(md) == "" {
		return
	}
	text := fit(RenderMarkdown(md), "")
	if _, err := c.send(ctx, chatID, text, replyTo, nil); err != nil {
		c.log.WarnContext(ctx, "telegram reply failed", "error", err)
	}
}

func (c *Channel) answerCallback(ctx context.Context, id string, resp channel.Response) {
	text := ""
	if !resp.Silent {
		text = plainText(resp.Text, 200)
	}
	err := c.call(ctx, "answerCallbackQuery", 0, false, func(ctx context.Context) error {
		_, err := c.bot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: id, Text: text, ShowAlert: resp.Alert && text != ""})
		return err
	})
	if err != nil {
		c.log.WarnContext(ctx, "answerCallbackQuery failed", "error", err)
	}
}

// plainText strips the markdown markers (callback answers are plain text) and caps the rune count.
func plainText(md string, max int) string {
	r := strings.NewReplacer("**", "", "`", "", "__", "")
	s := strings.TrimSpace(r.Replace(md))
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max-1]) + "…"
	}
	return s
}

// WebhookURL is where Telegram delivers updates in webhook mode.
func (c *Channel) WebhookURL() string {
	return c.publicURL + "/api/v1/channels/" + c.cfg.Name + "/webhook"
}

var allowedUpdates = []string{"message", "callback_query"}

// EnsureWebhook registers the webhook with Telegram (webhook mode). The core calls it once under a lease.
func (c *Channel) EnsureWebhook(ctx context.Context) error {
	return c.call(ctx, "setWebhook", 0, false, func(ctx context.Context) error {
		_, err := c.bot.SetWebhook(ctx, &bot.SetWebhookParams{URL: c.WebhookURL(), SecretToken: c.cfg.WebhookSecret, AllowedUpdates: allowedUpdates})
		return err
	})
}

func (c *Channel) deleteWebhook(ctx context.Context) error {
	return c.call(ctx, "deleteWebhook", 0, false, func(ctx context.Context) error {
		_, err := c.bot.DeleteWebhook(ctx, &bot.DeleteWebhookParams{})
		return err
	})
}

var _ channel.Channel = (*Channel)(nil)
