package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/xonoko/infra-monitor/internal/channel"
	"github.com/xonoko/infra-monitor/internal/config"
)

const testToken = "123456:SECRET-token-value"
const testChat int64 = -5054234212

type call struct {
	Method string
	Fields map[string]string
}

// fakeAPI is a minimal Telegram Bot API.
type fakeAPI struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	calls    []call
	nextID   int
	updates  []models.Update
	failNext map[string][]fakeErr // method -> queued errors
}

type fakeErr struct {
	code       int
	desc       string
	retryAfter int
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, nextID: 100, failNext: map[string][]fakeErr{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) fail(method string, e fakeErr) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext[method] = append(f.failNext[method], e)
}

func (f *fakeAPI) queueUpdates(u ...models.Update) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, u...)
}

func (f *fakeAPI) callsTo(method string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	prefix := "/bot" + testToken + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":404,"description":"Not Found"}`))
		return
	}
	method := strings.TrimPrefix(r.URL.Path, prefix)
	fields := map[string]string{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			for k, v := range r.MultipartForm.Value {
				fields[k] = v[0]
			}
		}
	} else if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		for k, v := range m {
			switch vv := v.(type) {
			case string:
				fields[k] = vv
			default:
				j, _ := json.Marshal(vv)
				fields[k] = string(j)
			}
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, call{Method: method, Fields: fields})
	var fe *fakeErr
	if q := f.failNext[method]; len(q) > 0 {
		fe = &q[0]
		f.failNext[method] = q[1:]
	}
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if fe != nil {
		w.WriteHeader(fe.code)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": false, "error_code": fe.code, "description": fe.desc,
			"parameters": map[string]any{"retry_after": fe.retryAfter},
		})
		return
	}
	switch method {
	case "sendMessage", "editMessageText":
		f.mu.Lock()
		id := f.nextID
		if method == "sendMessage" {
			f.nextID++
		} else {
			fmt.Sscanf(fields["message_id"], "%d", &id)
		}
		f.mu.Unlock()
		var chatID int64
		fmt.Sscanf(fields["chat_id"], "%d", &chatID)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{
			"message_id": id, "date": time.Now().Unix(), "chat": map[string]any{"id": chatID, "type": "supergroup"}, "text": fields["text"],
		}})
	case "getUpdates":
		f.mu.Lock()
		ups := f.updates
		f.updates = nil
		f.mu.Unlock()
		if len(ups) == 0 {
			time.Sleep(20 * time.Millisecond)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": ups})
	default:
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}
}

func testConfig(mode string) config.ChannelConfig {
	return config.ChannelConfig{
		Name: "infra", Type: "telegram", BotToken: testToken, ChatID: testChat, Mode: mode,
		WebhookSecret: "hook-secret", MessagesPerMinute: 6000,
	}
}

func newTestChannel(t *testing.T, f *fakeAPI, mode string, opts ...Option) *Channel {
	cfg := testConfig(mode)
	cfg.APIURL = f.srv.URL
	opts = append([]Option{
		WithHTTPClient(f.srv.Client()), WithPollClient(f.srv.Client()), WithPollTimeout(time.Second),
		WithBackoff(50*time.Millisecond, 20*time.Millisecond), WithMaxJitter(time.Millisecond),
	}, opts...)
	ch, err := New(cfg, "https://infra-monitor.example.com/", slog.New(slog.NewTextHandler(io.Discard, nil)), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// recordingSink records events and returns a scripted response.
type recordingSink struct {
	mu     sync.Mutex
	events []channel.InboundEvent
	resp   channel.Response
}

func (s *recordingSink) Handle(_ context.Context, ev channel.InboundEvent) channel.Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return s.resp
}

func (s *recordingSink) got() []channel.InboundEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]channel.InboundEvent(nil), s.events...)
}

func nopLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
