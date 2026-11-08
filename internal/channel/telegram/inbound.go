package telegram

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/xonoko/infra-monitor/internal/channel"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// privateCommands are the only commands accepted in a private chat with the bot.
var privateCommands = map[string]bool{"link": true, "start": true, "help": true}

// Receive long-polls getUpdates until ctx is done (polling mode). In webhook mode it returns nil.
func (c *Channel) Receive(ctx context.Context, sink channel.InboundSink) error {
	if c.cfg.Mode != "polling" {
		return nil
	}
	// getUpdates fails while a webhook is set.
	if err := c.deleteWebhook(ctx); err != nil && ctx.Err() == nil {
		c.log.WarnContext(ctx, "deleteWebhook failed before polling", "error", err)
	}
	c.log.InfoContext(ctx, "telegram polling started")
	var offset int64
	backoff := c.errorBackoff
	for ctx.Err() == nil {
		updates, err := c.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			wait := backoff
			var pe *pollError
			switch {
			case errors.As(err, &pe) && pe.code == http.StatusConflict:
				wait = c.conflictBackoff
				c.log.WarnContext(ctx, "telegram getUpdates conflict: another poller or a webhook uses this bot token", "retry_in", wait)
			case errors.As(err, &pe) && pe.code == http.StatusTooManyRequests && pe.retryAfter > 0:
				wait = time.Duration(pe.retryAfter) * time.Second
			default:
				c.log.WarnContext(ctx, "telegram getUpdates failed", "error", err, "retry_in", wait)
				if backoff < 60*time.Second {
					backoff *= 2
				}
			}
			sleep(ctx, wait)
			continue
		}
		backoff = c.errorBackoff
		for i := range updates {
			if updates[i].ID >= offset {
				offset = updates[i].ID + 1
			}
			c.dispatch(ctx, &updates[i], sink)
		}
	}
	c.log.Info("telegram polling stopped")
	return nil
}

type pollError struct {
	code       int
	desc       string
	retryAfter int
}

func (e *pollError) Error() string { return fmt.Sprintf("telegram getUpdates: %d %s", e.code, e.desc) }

func (c *Channel) getUpdates(ctx context.Context, offset int64) ([]models.Update, error) {
	body, _ := json.Marshal(map[string]any{
		"offset":          offset,
		"timeout":         int(c.pollTimeout.Seconds()),
		"allowed_updates": allowedUpdates,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/bot"+c.cfg.BotToken+"/getUpdates", bytes.NewReader(body))
	if err != nil {
		return nil, c.redact(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.pollClient.Do(req)
	if err != nil {
		return nil, c.redact(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, c.redact(err)
	}
	var r struct {
		OK          bool            `json:"ok"`
		Result      []models.Update `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("telegram getUpdates: decode (status %d): %w", resp.StatusCode, err)
	}
	if !r.OK {
		code := r.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return nil, &pollError{code: code, desc: r.Description, retryAfter: r.Parameters.RetryAfter}
	}
	return r.Result, nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

type webhookJob struct {
	ctx  context.Context
	upd  *models.Update
	sink channel.InboundSink
}

// WebhookHandler verifies Telegram's secret token header and dispatches updates asynchronously.
// It returns nil in polling mode.
func (c *Channel) WebhookHandler(sink channel.InboundSink) http.Handler {
	if c.cfg.Mode != "webhook" {
		return nil
	}
	c.webhookOnce.Do(func() {
		c.webhookQueue = make(chan webhookJob, 256)
		for i := 0; i < max(1, c.webhookWorkers); i++ {
			go func() {
				for j := range c.webhookQueue {
					ctx, cancel := context.WithTimeout(j.ctx, 2*time.Minute)
					c.dispatch(ctx, j.upd, j.sink)
					cancel()
				}
			}()
		}
	})
	secret := []byte(c.cfg.WebhookSecret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		got := []byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"))
		if len(secret) == 0 || subtle.ConstantTimeCompare(got, secret) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var upd models.Update
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&upd); err != nil {
			http.Error(w, "bad update", http.StatusBadRequest)
			return
		}
		select {
		case c.webhookQueue <- webhookJob{ctx: context.WithoutCancel(r.Context()), upd: &upd, sink: sink}:
			w.WriteHeader(http.StatusOK)
		default:
			// Telegram retries non-2xx deliveries later.
			c.log.Warn("telegram webhook queue full, asking Telegram to retry")
			http.Error(w, "busy", http.StatusServiceUnavailable)
		}
	})
}

// dispatch maps one update to an InboundEvent, calls the sink and answers the user.
func (c *Channel) dispatch(ctx context.Context, upd *models.Update, sink channel.InboundSink) {
	ctx, span := telemetry.Tracer().Start(ctx, "telegram.update", trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("channel.name", c.cfg.Name), attribute.Int64("telegram.update_id", upd.ID)))
	defer span.End()
	defer func() {
		if p := recover(); p != nil {
			c.log.ErrorContext(ctx, "panic handling telegram update", "panic", p)
		}
	}()
	switch {
	case upd.CallbackQuery != nil:
		c.handleCallback(ctx, upd.CallbackQuery, sink)
	case upd.Message != nil:
		c.handleMessage(ctx, upd.Message, sink)
	}
}

func externalUser(u *models.User) channel.ExternalUser {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		name = u.Username
	}
	return channel.ExternalUser{ID: strconv.FormatInt(u.ID, 10), Name: name, Username: u.Username}
}

// MapMessage converts a Telegram message to an inbound event. ok is false when it must be ignored.
func (c *Channel) MapMessage(m *models.Message) (channel.InboundEvent, bool) {
	if m == nil || m.From == nil || m.From.IsBot {
		return channel.InboundEvent{}, false
	}
	private := m.Chat.Type == models.ChatTypePrivate
	if !private && m.Chat.ID != c.cfg.ChatID {
		return channel.InboundEvent{}, false
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return channel.InboundEvent{}, false
	}
	ev := channel.InboundEvent{
		Channel:   c.cfg.Name,
		User:      externalUser(m.From),
		ChatID:    strconv.FormatInt(m.Chat.ID, 10),
		MessageID: strconv.Itoa(m.ID),
		Text:      text,
	}
	if m.ReplyToMessage != nil {
		ev.ReplyToMessageID = strconv.Itoa(m.ReplyToMessage.ID)
	}
	if m.MessageThreadID != 0 {
		ev.ThreadID = strconv.Itoa(m.MessageThreadID)
	}
	if strings.HasPrefix(text, "/") {
		cmd, args := parseCommand(text)
		if cmd == "" || (private && !privateCommands[cmd]) {
			return channel.InboundEvent{}, false
		}
		ev.Kind = channel.InboundCommand
		ev.Command, ev.Args = cmd, args
		return ev, true
	}
	if private || m.ReplyToMessage == nil {
		return channel.InboundEvent{}, false
	}
	ev.Kind = channel.InboundMessage
	return ev, true
}

// parseCommand splits "/mute@infra_bot 1h" into ("mute", ["1h"]).
func parseCommand(text string) (string, []string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", nil
	}
	cmd := strings.TrimPrefix(fields[0], "/")
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	return strings.ToLower(cmd), fields[1:]
}

func (c *Channel) handleMessage(ctx context.Context, m *models.Message, sink channel.InboundSink) {
	ev, ok := c.MapMessage(m)
	if !ok {
		return
	}
	resp := sink.Handle(ctx, ev)
	if resp.Silent || strings.TrimSpace(resp.Text) == "" {
		return
	}
	c.reply(ctx, m.Chat.ID, m.ID, resp.Text)
}

func (c *Channel) handleCallback(ctx context.Context, cq *models.CallbackQuery, sink channel.InboundSink) {
	if cq.From.IsBot {
		c.answerCallback(ctx, cq.ID, channel.Response{Silent: true})
		return
	}
	msg := cq.Message.Message
	chatID := c.cfg.ChatID
	if msg != nil {
		chatID = msg.Chat.ID
		if msg.Chat.ID != c.cfg.ChatID {
			c.answerCallback(ctx, cq.ID, channel.Response{Silent: true})
			return
		}
	}
	action, err := channel.DecodeButtonAction(cq.Data)
	if err != nil {
		c.answerCallback(ctx, cq.ID, channel.Response{Text: "This button is no longer supported.", Alert: true})
		return
	}
	user := cq.From
	ev := channel.InboundEvent{
		Channel: c.cfg.Name,
		Kind:    channel.InboundButton,
		User:    externalUser(&user),
		ChatID:  strconv.FormatInt(chatID, 10),
		Button:  &action,
	}
	if msg != nil {
		ev.MessageID = strconv.Itoa(msg.ID)
		if msg.ReplyToMessage != nil {
			ev.ReplyToMessageID = strconv.Itoa(msg.ReplyToMessage.ID)
		}
	} else if im := cq.Message.InaccessibleMessage; im != nil {
		if im.Chat.ID != c.cfg.ChatID {
			c.answerCallback(ctx, cq.ID, channel.Response{Silent: true})
			return
		}
		ev.MessageID = strconv.Itoa(im.MessageID)
	}
	c.answerCallback(ctx, cq.ID, sink.Handle(ctx, ev))
}
