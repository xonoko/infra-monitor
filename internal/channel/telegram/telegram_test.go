package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/channel"
)

func TestCapabilities(t *testing.T) {
	f := newFakeAPI(t)
	require.True(t, newTestChannel(t, f, "polling").Capabilities().SingletonReceiver)
	caps := newTestChannel(t, f, "webhook").Capabilities()
	require.False(t, caps.SingletonReceiver)
	require.True(t, caps.Buttons && caps.EditMessages && caps.Threads && caps.Typing)
	require.Equal(t, 4096, caps.MaxMessageLen)
}

func TestNewValidates(t *testing.T) {
	cfg := testConfig("polling")
	cfg.BotToken = ""
	_, err := New(cfg, "", nil)
	require.Error(t, err)
}

func TestCardCreateEditAndRecreate(t *testing.T) {
	f := newFakeAPI(t)
	ch := newTestChannel(t, f, "polling")
	ctx := context.Background()
	card := channel.Card{IncidentID: 1, Title: "Disk full", Status: "open", Severity: "warning",
		Buttons: []channel.Button{{Label: "Ack", Action: &channel.ButtonAction{Op: "ack", IncidentID: 1}}}}

	ref, err := ch.UpsertCard(ctx, nil, card)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatInt(testChat, 10), ref.ChatID)
	require.Equal(t, "100", ref.CardMessageID)
	require.Equal(t, ref.CardMessageID, ref.ThreadID)
	sends := f.callsTo("sendMessage")
	require.Len(t, sends, 1)
	require.Equal(t, "HTML", sends[0].Fields["parse_mode"])
	require.Contains(t, sends[0].Fields["text"], "<b>Disk full</b>")
	require.Contains(t, sends[0].Fields["reply_markup"], `"callback_data":"ia:1"`)

	card.Status = "acknowledged"
	ref2, err := ch.UpsertCard(ctx, &ref, card)
	require.NoError(t, err)
	require.Equal(t, ref, ref2)
	edits := f.callsTo("editMessageText")
	require.Len(t, edits, 1)
	require.Equal(t, "100", edits[0].Fields["message_id"])
	require.Contains(t, edits[0].Fields["text"], "acknowledged")

	// "message is not modified" is success
	f.fail("editMessageText", fakeErr{code: 400, desc: "Bad Request: message is not modified"})
	_, err = ch.UpsertCard(ctx, &ref, card)
	require.NoError(t, err)

	// deleted card -> a new one is sent
	f.fail("editMessageText", fakeErr{code: 400, desc: "Bad Request: message to edit not found"})
	ref3, err := ch.UpsertCard(ctx, &ref, card)
	require.NoError(t, err)
	require.NotEqual(t, ref.CardMessageID, ref3.CardMessageID)
	require.Len(t, f.callsTo("sendMessage"), 2)

	// other errors surface
	f.fail("editMessageText", fakeErr{code: 403, desc: "Forbidden: bot was kicked"})
	_, err = ch.UpsertCard(ctx, &ref3, card)
	require.Error(t, err)
}

func TestPostEditAndTyping(t *testing.T) {
	f := newFakeAPI(t)
	ch := newTestChannel(t, f, "polling")
	ctx := context.Background()
	thread := channel.ThreadRef{ChatID: strconv.FormatInt(testChat, 10), ThreadID: "55", CardMessageID: "55"}
	ref, err := ch.Post(ctx, thread, channel.Message{Kind: channel.KindProposal, Author: "Agent", Body: "Restart?",
		Buttons: []channel.Button{
			{Label: "Approve", Style: channel.StylePrimary, Action: &channel.ButtonAction{Op: "approve", ActionID: 9}},
			{Label: "Reject", Style: channel.StyleDanger, Action: &channel.ButtonAction{Op: "reject", ActionID: 9}},
		}})
	require.NoError(t, err)
	send := f.callsTo("sendMessage")[0]
	var rp models.ReplyParameters
	require.NoError(t, json.Unmarshal([]byte(send.Fields["reply_parameters"]), &rp))
	require.Equal(t, 55, rp.MessageID)
	require.True(t, rp.AllowSendingWithoutReply)
	require.Contains(t, send.Fields["reply_markup"], `"callback_data":"ay:9"`)
	require.Contains(t, send.Fields["text"], "<b>Agent</b>\nRestart?")

	require.NoError(t, ch.Edit(ctx, ref, channel.Message{Body: "Approved by Lucas via UI"}))
	edit := f.callsTo("editMessageText")[0]
	require.Equal(t, ref.MessageID, edit.Fields["message_id"])
	require.JSONEq(t, `{"inline_keyboard":[]}`, edit.Fields["reply_markup"], "buttons removed")

	require.NoError(t, ch.SetTyping(ctx, thread))
	require.Equal(t, "typing", f.callsTo("sendChatAction")[0].Fields["action"])

	require.Error(t, ch.Edit(ctx, channel.MessageRef{MessageID: "x"}, channel.Message{}))
}

func TestRetryAfter429(t *testing.T) {
	f := newFakeAPI(t)
	ch := newTestChannel(t, f, "polling")
	f.fail("sendMessage", fakeErr{code: 429, desc: "Too Many Requests: retry after 1", retryAfter: 1})
	start := time.Now()
	_, err := ch.Post(context.Background(), channel.ThreadRef{}, channel.Message{Body: "hi"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), time.Second)
	require.Len(t, f.callsTo("sendMessage"), 2)

	// gives up after maxRetries and honors ctx
	for i := 0; i < maxRetries+1; i++ {
		f.fail("sendMessage", fakeErr{code: 429, desc: "Too Many Requests", retryAfter: 30})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = ch.Post(ctx, channel.ThreadRef{}, channel.Message{Body: "hi"})
	require.Error(t, err)
}

func TestPerChatRateLimit(t *testing.T) {
	f := newFakeAPI(t)
	cfg := testConfig("polling")
	_ = cfg
	ch := newTestChannel(t, f, "polling")
	ch.perChatLim, ch.perChatBur = 4, 2 // 4 msg/s, burst 2
	start := time.Now()
	for i := 0; i < 4; i++ {
		_, err := ch.Post(context.Background(), channel.ThreadRef{}, channel.Message{Body: "x"})
		require.NoError(t, err)
	}
	// 2 immediate + 2 at 250ms intervals
	require.GreaterOrEqual(t, time.Since(start), 450*time.Millisecond)
}

func TestTokenIsRedacted(t *testing.T) {
	cfg := testConfig("polling")
	cfg.APIURL = "http://127.0.0.1:1" // nothing listens
	ch, err := New(cfg, "", nopLogger(), WithBackoff(time.Millisecond, time.Millisecond))
	require.NoError(t, err)
	_, err = ch.Post(context.Background(), channel.ThreadRef{}, channel.Message{Body: "x"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "SECRET-token-value")
	_, err = ch.getUpdates(context.Background(), 0)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "SECRET-token-value")

	wrapped := ch.redact(errors.New("GET https://api.telegram.org/bot" + testToken + "/x failed"))
	require.Equal(t, "GET https://api.telegram.org/bot***/x failed", wrapped.Error())
}

func groupMsg(id int, text string, replyTo int) *models.Message {
	m := &models.Message{ID: id, Date: int(time.Now().Unix()), Text: text, Chat: models.Chat{ID: testChat, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 77, FirstName: "Lucas", LastName: "G", Username: "lucasg"}}
	if replyTo > 0 {
		m.ReplyToMessage = &models.Message{ID: replyTo, Chat: m.Chat}
	}
	return m
}

func TestMapMessage(t *testing.T) {
	f := newFakeAPI(t)
	ch := newTestChannel(t, f, "polling")

	ev, ok := ch.MapMessage(groupMsg(1, "/mute@infra_bot 1h", 100))
	require.True(t, ok)
	require.Equal(t, channel.InboundCommand, ev.Kind)
	require.Equal(t, "mute", ev.Command)
	require.Equal(t, []string{"1h"}, ev.Args)
	require.Equal(t, "100", ev.ReplyToMessageID)
	require.Equal(t, channel.ExternalUser{ID: "77", Name: "Lucas G", Username: "lucasg"}, ev.User)
	require.Equal(t, "infra", ev.Channel)

	ev, ok = ch.MapMessage(groupMsg(2, "why is it failing?", 100))
	require.True(t, ok)
	require.Equal(t, channel.InboundMessage, ev.Kind)
	require.Equal(t, "why is it failing?", ev.Text)

	_, ok = ch.MapMessage(groupMsg(3, "random chatter", 0))
	require.False(t, ok, "non-reply free text is ignored")

	other := groupMsg(4, "/status", 0)
	other.Chat.ID = -1
	_, ok = ch.MapMessage(other)
	require.False(t, ok, "other groups are ignored")

	botMsg := groupMsg(5, "/status", 0)
	botMsg.From.IsBot = true
	_, ok = ch.MapMessage(botMsg)
	require.False(t, ok)

	private := func(text string) *models.Message {
		m := groupMsg(6, text, 0)
		m.Chat = models.Chat{ID: 77, Type: models.ChatTypePrivate}
		return m
	}
	ev, ok = ch.MapMessage(private("/link ABC123"))
	require.True(t, ok)
	require.Equal(t, "link", ev.Command)
	require.Equal(t, []string{"ABC123"}, ev.Args)
	require.Equal(t, "77", ev.ChatID)
	_, ok = ch.MapMessage(private("/approve 9"))
	require.False(t, ok, "only /link /start /help in private chats")
	_, ok = ch.MapMessage(private("hello"))
	require.False(t, ok)
}

func TestPollingDispatchesAndAnswers(t *testing.T) {
	f := newFakeAPI(t)
	ch := newTestChannel(t, f, "polling")
	sink := &recordingSink{resp: channel.Response{Text: "Approved **#9**", Alert: true}}

	cbMsg := groupMsg(200, "proposal", 100)
	f.queueUpdates(
		models.Update{ID: 10, Message: groupMsg(1, "/status", 0)},
		models.Update{ID: 11, CallbackQuery: &models.CallbackQuery{ID: "cb1", From: models.User{ID: 77, FirstName: "Lucas"},
			Data: "ay:9", Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage, Message: cbMsg}}},
		models.Update{ID: 12, CallbackQuery: &models.CallbackQuery{ID: "cb2", From: models.User{ID: 77}, Data: "garbage",
			Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage, Message: cbMsg}}},
		models.Update{ID: 13, Message: groupMsg(2, "chatter", 0)},
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- ch.Receive(ctx, sink) }()
	require.Eventually(t, func() bool { return len(f.callsTo("answerCallbackQuery")) == 2 }, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return len(f.callsTo("getUpdates")) >= 2 }, 5*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	require.Len(t, f.callsTo("deleteWebhook"), 1, "webhook removed before polling")
	evs := sink.got()
	require.Len(t, evs, 2)
	require.Equal(t, channel.InboundCommand, evs[0].Kind)
	require.Equal(t, channel.InboundButton, evs[1].Kind)
	require.Equal(t, &channel.ButtonAction{Op: "approve", ActionID: 9}, evs[1].Button)
	require.Equal(t, "200", evs[1].MessageID)
	require.Equal(t, "100", evs[1].ReplyToMessageID)

	answers := f.callsTo("answerCallbackQuery")
	require.Equal(t, "cb1", answers[0].Fields["callback_query_id"])
	require.Equal(t, "Approved #9", answers[0].Fields["text"])
	require.Equal(t, "true", answers[0].Fields["show_alert"])
	require.Equal(t, "cb2", answers[1].Fields["callback_query_id"])

	// the command got a reply to the user's message
	replies := f.callsTo("sendMessage")
	require.Len(t, replies, 1)
	require.Contains(t, replies[0].Fields["reply_parameters"], `"message_id":1`)
	require.Contains(t, replies[0].Fields["text"], "Approved <b>#9</b>")

	// offsets advance
	gu := f.callsTo("getUpdates")
	require.Equal(t, "14", gu[len(gu)-1].Fields["offset"])
	require.Contains(t, gu[0].Fields["allowed_updates"], "callback_query")
}

func TestPollingConflictBacksOff(t *testing.T) {
	f := newFakeAPI(t)
	ch := newTestChannel(t, f, "polling", WithBackoff(300*time.Millisecond, 10*time.Millisecond))
	f.fail("getUpdates", fakeErr{code: 409, desc: "Conflict: terminated by other getUpdates request"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- ch.Receive(ctx, &recordingSink{}) }()
	time.Sleep(150 * time.Millisecond)
	require.Len(t, f.callsTo("getUpdates"), 1, "waits conflictBackoff before retrying")
	require.Eventually(t, func() bool { return len(f.callsTo("getUpdates")) >= 2 }, 3*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

func TestSilentResponseDoesNotReply(t *testing.T) {
	f := newFakeAPI(t)
	ch := newTestChannel(t, f, "polling")
	ch.dispatch(context.Background(), &models.Update{ID: 1, Message: groupMsg(1, "look at logs", 100)}, &recordingSink{resp: channel.Response{Silent: true}})
	require.Empty(t, f.callsTo("sendMessage"))
}

func TestWebhookHandler(t *testing.T) {
	f := newFakeAPI(t)
	require.Nil(t, newTestChannel(t, f, "polling").WebhookHandler(&recordingSink{}))

	ch := newTestChannel(t, f, "webhook")
	sink := &recordingSink{resp: channel.Response{Silent: true}}
	h := ch.WebhookHandler(sink)
	require.NotNil(t, h)
	require.NoError(t, ch.Receive(context.Background(), sink), "webhook mode: Receive returns immediately")

	body, _ := json.Marshal(models.Update{ID: 1, Message: groupMsg(1, "/ack", 100)})
	post := func(secret string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/infra/webhook", strings.NewReader(string(body)))
		if secret != "" {
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	require.Equal(t, http.StatusUnauthorized, post(""))
	require.Equal(t, http.StatusUnauthorized, post("wrong"))
	require.Equal(t, http.StatusOK, post("hook-secret"))
	require.Eventually(t, func() bool { return len(sink.got()) == 1 }, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, "ack", sink.got()[0].Command)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	require.NoError(t, ch.EnsureWebhook(context.Background()))
	sw := f.callsTo("setWebhook")[0]
	require.Equal(t, "https://infra-monitor.example.com/api/v1/channels/infra/webhook", sw.Fields["url"])
	require.Equal(t, "hook-secret", sw.Fields["secret_token"])
}
