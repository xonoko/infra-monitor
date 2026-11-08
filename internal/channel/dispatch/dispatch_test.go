package dispatch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/auth"
	"github.com/xonoko/infra-monitor/internal/channel"
	"github.com/xonoko/infra-monitor/internal/channel/dispatch"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/incident"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/testutil/pgtest"
)

// fakeChannel records everything sent.
type fakeChannel struct {
	mu     sync.Mutex
	next   int
	cards  map[string]channel.Card
	posts  []post
	edits  map[string]channel.Message
	typing int
}

type post struct {
	ID     string
	Thread string
	Msg    channel.Message
}

func newFake() *fakeChannel {
	return &fakeChannel{cards: map[string]channel.Card{}, edits: map[string]channel.Message{}}
}

func (f *fakeChannel) Name() string { return "tg" }
func (f *fakeChannel) Type() string { return "telegram" }
func (f *fakeChannel) Capabilities() channel.Capabilities {
	return channel.Capabilities{Buttons: true, URLButtons: true, EditMessages: true, Threads: true, Typing: true, MaxMessageLen: 4096}
}
func (f *fakeChannel) id() string { f.next++; return fmt.Sprint(f.next) }
func (f *fakeChannel) UpsertCard(_ context.Context, t *channel.ThreadRef, c channel.Card) (channel.ThreadRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t == nil {
		id := f.id()
		f.cards[id] = c
		return channel.ThreadRef{ChatID: "chat", ThreadID: id, CardMessageID: id}, nil
	}
	f.cards[t.CardMessageID] = c
	return *t, nil
}
func (f *fakeChannel) Post(_ context.Context, t channel.ThreadRef, m channel.Message) (channel.MessageRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id()
	f.posts = append(f.posts, post{ID: id, Thread: t.ThreadID, Msg: m})
	return channel.MessageRef{ChatID: "chat", MessageID: id}, nil
}
func (f *fakeChannel) Edit(_ context.Context, r channel.MessageRef, m channel.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits[r.MessageID] = m
	return nil
}
func (f *fakeChannel) SetTyping(context.Context, channel.ThreadRef) error {
	f.mu.Lock()
	f.typing++
	f.mu.Unlock()
	return nil
}
func (f *fakeChannel) Receive(context.Context, channel.InboundSink) error { return nil }
func (f *fakeChannel) WebhookHandler(channel.InboundSink) http.Handler    { return nil }

func (f *fakeChannel) snapshot() ([]post, map[string]channel.Card, map[string]channel.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cards := map[string]channel.Card{}
	for k, v := range f.cards {
		cards[k] = v
	}
	edits := map[string]channel.Message{}
	for k, v := range f.edits {
		edits[k] = v
	}
	return append([]post(nil), f.posts...), cards, edits
}

type echo struct{}

func (echo) Type() string        { return "test.echo" }
func (echo) Description() string { return "echo" }
func (echo) Risk() string        { return "low" }
func (echo) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`)
}
func (echo) Validate(context.Context, map[string]any, actions.Target) error { return nil }
func (echo) Summary(p map[string]any, _ actions.Target) string              { return fmt.Sprint("echo ", p["msg"]) }
func (echo) Execute(_ context.Context, p map[string]any, _ actions.Target) (string, error) {
	return "ok", nil
}

type agentStub struct{}

func (agentStub) Name() string  { return "holmes" }
func (agentStub) Model() string { return "investigate" }
func (agentStub) Investigate(context.Context, agent.IncidentContext, func(agent.Progress)) (*agent.InvestigationResult, error) {
	return &agent.InvestigationResult{Summary: "disk full on node-1", Verdict: "actionable", Confidence: 0.9,
		ProposedActions: []agent.ProposedAction{{Type: "test.echo", Params: map[string]any{"msg": "clean"}, Rationale: "free space"}}}, nil
}
func (agentStub) Chat(_ context.Context, r agent.ChatRequest, _ func(agent.Progress)) (*agent.ChatResult, error) {
	return &agent.ChatResult{Reply: "answer to: " + r.Message}, nil
}

func TestDispatcherEndToEnd(t *testing.T) {
	d := pgtest.New(t)
	ctx := context.Background()
	cfg := &config.Config{
		Server:      config.ServerConfig{PublicURL: "https://im.example.com"},
		Sources:     []config.SourceConfig{{Name: "am", Type: "alertmanager", ClusterLabel: "cluster", NamespaceLabel: "namespace"}},
		Correlation: config.CorrelationConfig{GroupBy: []string{"alertname", "cluster", "namespace"}, ReopenWindow: time.Minute, Debounce: time.Second, ResolveGrace: time.Second},
		Agent:       config.AgentConfig{Enabled: true, Timeout: time.Minute, NotifyTimeout: time.Minute, MaxConcurrent: 2, MaxPerHour: 10},
		Actions:     config.ActionsConfig{ProposalTTL: time.Hour, Timeout: time.Minute},
		Channels:    []config.ChannelConfig{{Name: "tg", Type: "telegram"}},
		Auth:        config.AuthConfig{Local: config.LocalAuthConfig{Enabled: true}, SessionTTL: time.Hour},
	}
	reg := actions.NewRegistry(nil, nil)
	require.NoError(t, reg.Register(echo{}))
	svc, err := incident.New(incident.Deps{Config: cfg, DB: d, Log: pgtest.Logger(), Registry: reg, Agent: agentStub{}})
	require.NoError(t, err)
	authSvc := auth.NewService(cfg, d, pgtest.Logger())
	fake := newFake()
	disp := dispatch.New(d, svc, authSvc, nil, pgtest.Logger(), fake)
	q := queue.New(d.Pool, nil, pgtest.Logger(), time.Second)
	svc.RegisterJobs(q)
	disp.RegisterJobs(q)
	drain := func() { _, err := q.Drain(ctx, true); require.NoError(t, err) }

	_, err = svc.IngestSignals(ctx, "am", []domain.Signal{{Fingerprint: "f", Status: "firing",
		Labels:      map[string]string{"alertname": "NodeDiskFull", "cluster": "manager", "namespace": "infra", "severity": "critical"},
		Annotations: map[string]string{"summary": "Disk almost full"}}})
	require.NoError(t, err)
	drain()

	posts, cards, _ := fake.snapshot()
	require.Len(t, cards, 1)
	var card channel.Card
	var cardID string
	for id, c := range cards {
		card, cardID = c, id
	}
	require.Contains(t, card.Title, "Disk almost full")
	require.Equal(t, "disk full on node-1", card.Summary)
	require.Len(t, card.PendingActions, 1)
	require.Len(t, posts, 1, "pending proposal posted with buttons")
	require.Equal(t, channel.KindProposal, posts[0].Msg.Kind)
	require.Len(t, posts[0].Msg.Buttons, 2)
	approve := *posts[0].Msg.Buttons[0].Action
	require.Equal(t, "approve", approve.Op)

	// unlinked user can't approve
	resp := disp.Handle(ctx, channel.InboundEvent{Channel: "tg", Kind: channel.InboundButton, User: channel.ExternalUser{ID: "77"}, ChatID: "chat", Button: &approve})
	require.True(t, resp.Alert)
	require.Contains(t, resp.Text, "/link")

	// link, then approve
	u, err := authSvc.CreateUser(ctx, "lucas@example.com", "Lucas", domain.RoleOperator, "")
	require.NoError(t, err)
	lc, err := authSvc.CreateLinkCode(ctx, u.ID)
	require.NoError(t, err)
	resp = disp.Handle(ctx, channel.InboundEvent{Channel: "tg", Kind: channel.InboundCommand, Command: "link", Args: []string{strings.ToLower(lc.Code)},
		User: channel.ExternalUser{ID: "77", Name: "Lucas"}, ChatID: "private"})
	require.Contains(t, resp.Text, "Linked")
	resp = disp.Handle(ctx, channel.InboundEvent{Channel: "tg", Kind: channel.InboundButton, User: channel.ExternalUser{ID: "77"}, ChatID: "chat", Button: &approve})
	require.Contains(t, resp.Text, "approved")
	drain()

	posts, cards, edits := fake.snapshot()
	require.Empty(t, cards[cardID].PendingActions, "card edited in place")
	edited, ok := edits[posts[0].ID]
	require.True(t, ok, "proposal message edited after the decision")
	require.Contains(t, edited.Footer, "Lucas via telegram")
	require.Empty(t, edited.Buttons)
	require.Contains(t, posts[len(posts)-1].Msg.Body, "succeeded")

	// reply to the card = chat; the user's own message is not echoed, the agent reply is posted
	before := len(posts)
	resp = disp.Handle(ctx, channel.InboundEvent{Channel: "tg", Kind: channel.InboundMessage, User: channel.ExternalUser{ID: "77"},
		ChatID: "chat", ReplyToMessageID: cardID, Text: "is it fixed?"})
	require.True(t, resp.Silent)
	drain()
	posts, _, _ = fake.snapshot()
	require.Len(t, posts, before+1)
	require.Equal(t, "🤖 Agent", posts[before].Msg.Author)
	require.Equal(t, "answer to: is it fixed?", posts[before].Msg.Body)

	// a UI message is mirrored with its origin
	ui := auth.ActorFor(u, "ui", "")
	_, err = svc.PostMessage(ctx, ui, card.IncidentID, "checking from the UI")
	require.NoError(t, err)
	drain()
	posts, _, _ = fake.snapshot()
	var mirrored bool
	for _, p := range posts {
		if p.Msg.Author == "Lucas via ui" && p.Msg.Body == "checking from the UI" {
			mirrored = true
		}
	}
	require.True(t, mirrored)

	// the same delivery twice (poller restart) is handled once
	dup := channel.InboundEvent{Channel: "tg", Kind: channel.InboundMessage, EventID: "upd-42", User: channel.ExternalUser{ID: "77"},
		ChatID: "chat", ReplyToMessageID: cardID, Text: "only once"}
	disp.Handle(ctx, dup)
	resp = disp.Handle(ctx, dup)
	require.True(t, resp.Silent)
	msgs, err := svc.Messages(ctx, card.IncidentID)
	require.NoError(t, err)
	once := 0
	for _, m := range msgs {
		if m.Content == "only once" {
			once++
		}
	}
	require.Equal(t, 1, once)

	// commands
	resp = disp.Handle(ctx, channel.InboundEvent{Channel: "tg", Kind: channel.InboundCommand, Command: "status", ChatID: "chat"})
	require.Contains(t, resp.Text, "1 open incident")
	resp = disp.Handle(ctx, channel.InboundEvent{Channel: "tg", Kind: channel.InboundCommand, Command: "ack", User: channel.ExternalUser{ID: "77"},
		ChatID: "chat", ReplyToMessageID: cardID})
	require.Equal(t, "Acknowledged", resp.Text)
	drain()
	_, cards, _ = fake.snapshot()
	require.Equal(t, "acknowledged", cards[cardID].Status)
}
