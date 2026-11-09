// Package dispatch connects the core to every channel.Channel: it renders incidents into
// cards and thread messages (job channel_sync), persists the external thread/message ids,
// mirrors activity across channels without echo, and turns inbound events (replies,
// buttons, commands) into the same service calls the UI uses.
package dispatch

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/xonoko/infra-monitor/internal/auth"
	"github.com/xonoko/infra-monitor/internal/channel"
	"github.com/xonoko/infra-monitor/internal/db"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/action"
	"github.com/xonoko/infra-monitor/internal/db/ent/channelmessage"
	"github.com/xonoko/infra-monitor/internal/db/ent/channelthread"
	"github.com/xonoko/infra-monitor/internal/db/ent/inboundevent"
	"github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/db/ent/signal"
	"github.com/xonoko/infra-monitor/internal/db/ent/timelineevent"
	"github.com/xonoko/infra-monitor/internal/domain"
	incsvc "github.com/xonoko/infra-monitor/internal/incident"
	"github.com/xonoko/infra-monitor/internal/lease"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

type Dispatcher struct {
	ent      *ent.Client
	svc      *incsvc.Service
	auth     *auth.Service
	leases   *lease.Manager
	log      *slog.Logger
	channels map[string]channel.Channel
	order    []string

	typingMu sync.Mutex
	typingAt map[string]time.Time

	statusMu sync.Mutex
	status   map[string]*Status
}

// Status is the runtime state of a channel on this replica.
type Status struct {
	Name, Type, Mode string
	Receiving        bool
	Capabilities     map[string]bool
	LastError        string
	LastErrorAt      *time.Time
	LastSentAt       *time.Time
}

func New(d *db.DB, svc *incsvc.Service, authSvc *auth.Service, leases *lease.Manager, log *slog.Logger, chans ...channel.Channel) *Dispatcher {
	ds := &Dispatcher{ent: d.Ent, svc: svc, auth: authSvc, leases: leases, log: log,
		channels: map[string]channel.Channel{}, typingAt: map[string]time.Time{}, status: map[string]*Status{}}
	for _, c := range chans {
		ds.channels[c.Name()] = c
		ds.order = append(ds.order, c.Name())
		cp := c.Capabilities()
		ds.status[c.Name()] = &Status{Name: c.Name(), Type: c.Type(), Capabilities: map[string]bool{
			"buttons": cp.Buttons, "edit_messages": cp.EditMessages, "threads": cp.Threads, "typing": cp.Typing, "singleton_receiver": cp.SingletonReceiver,
		}}
	}
	svc.Typing = ds.typing
	return ds
}

// Channel returns a channel by name.
func (d *Dispatcher) Channel(name string) (channel.Channel, bool) {
	c, ok := d.channels[name]
	return c, ok
}

// Statuses returns a snapshot of channel states on this replica.
func (d *Dispatcher) Statuses() []Status {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	out := make([]Status, 0, len(d.order))
	for _, n := range d.order {
		s := *d.status[n]
		s.Receiving = d.leases == nil || !d.channels[n].Capabilities().SingletonReceiver || d.leases.Held(receiverLease(n))
		out = append(out, s)
	}
	return out
}

func (d *Dispatcher) recordErr(name string, err error) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	if st := d.status[name]; st != nil {
		now := time.Now()
		if err != nil {
			st.LastError, st.LastErrorAt = err.Error(), &now
		} else {
			st.LastSentAt = &now
		}
	}
}

func receiverLease(name string) string { return "channel:" + name + ":receiver" }

// RegisterJobs registers channel_sync.
func (d *Dispatcher) RegisterJobs(q *queue.Queue) {
	q.Register(incsvc.JobChannelSync, func(ctx context.Context, j *queue.Job) error {
		var p incsvc.ChannelSyncPayload
		if err := j.Decode(&p); err != nil {
			return queue.Permanent(err)
		}
		return d.Sync(ctx, p.IncidentID, p.Channel)
	}, queue.HandlerOpts{Concurrency: 4, Timeout: 2 * time.Minute})
}

// Run starts every receiver (singleton receivers under a lease) until ctx is done.
func (d *Dispatcher) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, name := range d.order {
		ch := d.channels[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w, ok := ch.(interface{ EnsureWebhook(context.Context) error }); ok && ch.WebhookHandler(d) != nil && !ch.Capabilities().SingletonReceiver {
				d.leases.Run(ctx, "channel:"+name+":webhook", func(lctx context.Context) error {
					if err := w.EnsureWebhook(lctx); err != nil {
						d.log.Error("channel webhook registration failed", "channel", name, "error", err)
						d.recordErr(name, err)
						return err
					}
					d.log.Info("channel webhook registered", "channel", name)
					<-lctx.Done()
					return nil
				})
				return
			}
			run := func(lctx context.Context) error {
				err := ch.Receive(lctx, d)
				if err != nil && lctx.Err() == nil {
					d.log.Error("channel receiver stopped", "channel", name, "error", err)
					d.recordErr(name, err)
				}
				return err
			}
			if ch.Capabilities().SingletonReceiver && d.leases != nil {
				d.leases.Run(ctx, receiverLease(name), run)
				return
			}
			for ctx.Err() == nil {
				_ = run(ctx)
				select {
				case <-ctx.Done():
				case <-time.After(10 * time.Second):
				}
			}
		}()
	}
	wg.Wait()
	return nil
}

// WebhookHandler returns the HTTP handler for a channel's inbound webhook (nil if none).
func (d *Dispatcher) WebhookHandler(name string) http.Handler {
	ch, ok := d.channels[name]
	if !ok {
		return nil
	}
	return ch.WebhookHandler(d)
}

// typing shows "typing..." in every thread of the incident (throttled).
func (d *Dispatcher) typing(ctx context.Context, incidentID int) {
	key := strconv.Itoa(incidentID)
	d.typingMu.Lock()
	if time.Since(d.typingAt[key]) < 4*time.Second {
		d.typingMu.Unlock()
		return
	}
	d.typingAt[key] = time.Now()
	d.typingMu.Unlock()
	threads, err := d.ent.ChannelThread.Query().Where(channelthread.IncidentID(incidentID)).All(ctx)
	if err != nil {
		return
	}
	for _, t := range threads {
		if ch, ok := d.channels[t.Channel]; ok && ch.Capabilities().Typing {
			_ = ch.SetTyping(ctx, channel.ThreadRef{ChatID: t.ExternalChatID, ThreadID: t.ExternalThreadID, CardMessageID: t.CardMessageID})
		}
	}
}

// ---- outbound ----

// Sync renders the incident into the channel: creates/edits the card and posts new timeline entries.
func (d *Dispatcher) Sync(ctx context.Context, incidentID int, name string) error {
	ch, ok := d.channels[name]
	if !ok {
		return queue.Permanent(fmt.Errorf("unknown channel %q", name))
	}
	ctx, span := telemetry.Tracer().Start(ctx, "channel.sync")
	defer span.End()
	span.SetAttributes(attribute.String("channel", name), attribute.Int("incident.id", incidentID))
	err := d.sync(ctx, ch, incidentID)
	telemetry.RecordError(span, err)
	d.recordErr(name, err)
	return err
}

func (d *Dispatcher) sync(ctx context.Context, ch channel.Channel, incidentID int) error {
	inc, err := d.ent.Incident.Query().Where(incident.ID(incidentID)).WithAssignee().Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	caps := ch.Capabilities()
	card, err := d.card(ctx, inc, caps)
	if err != nil {
		return err
	}
	hash := cardHash(card)
	thread, err := d.ent.ChannelThread.Query().Where(channelthread.Channel(ch.Name()), channelthread.IncidentID(inc.ID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	if thread == nil {
		ref, err := ch.UpsertCard(ctx, nil, card)
		if err != nil {
			return fmt.Errorf("create card: %w", err)
		}
		// Events so far are summarized by the card: start the cursor at the latest one.
		last, err := d.ent.TimelineEvent.Query().Where(timelineevent.IncidentID(inc.ID)).Aggregate(ent.Max(timelineevent.FieldID)).Int(ctx)
		if err != nil {
			last = 0
		}
		thread, err = d.ent.ChannelThread.Create().SetChannel(ch.Name()).SetIncidentID(inc.ID).
			SetExternalChatID(ref.ChatID).SetExternalThreadID(ref.ThreadID).SetCardMessageID(ref.CardMessageID).
			SetCardHash(hash).SetLastEventID(last).Save(ctx)
		if err != nil {
			return err
		}
		if err := d.saveMessage(ctx, thread, ref.ChatID, ref.CardMessageID, channelmessage.KindCard, nil, nil); err != nil {
			return err
		}
		pending, err := d.ent.Action.Query().Where(action.IncidentID(inc.ID), action.StatusEQ(action.StatusProposed)).Order(ent.Asc(action.FieldID)).All(ctx)
		if err != nil {
			return err
		}
		for _, a := range pending {
			if err := d.postProposal(ctx, ch, thread, a, nil); err != nil {
				return err
			}
		}
		_ = d.notifySent(ctx, inc, ch)
	} else if thread.CardHash != hash {
		ref := channel.ThreadRef{ChatID: thread.ExternalChatID, ThreadID: thread.ExternalThreadID, CardMessageID: thread.CardMessageID}
		nref, err := ch.UpsertCard(ctx, &ref, card)
		if err != nil {
			return fmt.Errorf("update card: %w", err)
		}
		upd := d.ent.ChannelThread.UpdateOne(thread).SetCardHash(hash)
		if nref.CardMessageID != thread.CardMessageID && nref.CardMessageID != "" {
			upd.SetCardMessageID(nref.CardMessageID).SetExternalThreadID(nref.ThreadID)
			if err := d.saveMessage(ctx, thread, nref.ChatID, nref.CardMessageID, channelmessage.KindCard, nil, nil); err != nil {
				return err
			}
		}
		if thread, err = upd.Save(ctx); err != nil {
			return err
		}
	}
	return d.postEvents(ctx, ch, inc, thread)
}

func (d *Dispatcher) card(ctx context.Context, inc *ent.Incident, caps channel.Capabilities) (channel.Card, error) {
	sigs, err := d.ent.Signal.Query().Where(signal.IncidentID(inc.ID)).Select(signal.FieldStatus).All(ctx)
	if err != nil {
		return channel.Card{}, err
	}
	firing := 0
	for _, s := range sigs {
		if s.Status == signal.StatusFiring {
			firing++
		}
	}
	pending, err := d.ent.Action.Query().Where(action.IncidentID(inc.ID), action.StatusEQ(action.StatusProposed)).All(ctx)
	if err != nil {
		return channel.Card{}, err
	}
	return buildCard(cardInput{inc: inc, firing: firing, signals: len(sigs), pending: pending,
		agentEnabled: d.svc.AgentEnabled(), url: d.svc.IncidentURL(inc.ID), now: d.svc.Now()}, caps), nil
}

func (d *Dispatcher) notifySent(ctx context.Context, inc *ent.Incident, ch channel.Channel) error {
	_, err := d.ent.TimelineEvent.Create().SetIncidentID(inc.ID).SetType(domain.EventNotificationSent).
		SetActorType(timelineevent.ActorTypeSystem).SetActorName("infra-monitor").SetChannel(ch.Type()).SetChannelName(ch.Name()).
		SetPayload(map[string]any{"channel": ch.Name()}).SetTraceID(telemetry.TraceID(ctx)).Save(ctx)
	return err
}

func (d *Dispatcher) saveMessage(ctx context.Context, t *ent.ChannelThread, chatID, msgID string, kind channelmessage.Kind, eventID, actionID *int) error {
	return d.ent.ChannelMessage.Create().SetThreadID(t.ID).SetChannel(t.Channel).SetExternalChatID(chatID).
		SetExternalMessageID(msgID).SetKind(kind).SetNillableTimelineEventID(eventID).SetNillableActionID(actionID).
		OnConflictColumns(channelmessage.FieldChannel, channelmessage.FieldExternalChatID, channelmessage.FieldExternalMessageID).
		UpdateNewValues().Exec(ctx)
}

func threadRef(t *ent.ChannelThread) channel.ThreadRef {
	return channel.ThreadRef{ChatID: t.ExternalChatID, ThreadID: t.ExternalThreadID, CardMessageID: t.CardMessageID}
}

func (d *Dispatcher) post(ctx context.Context, ch channel.Channel, t *ent.ChannelThread, msg channel.Message, eventID *int) error {
	ref, err := ch.Post(ctx, threadRef(t), msg)
	if err != nil {
		return err
	}
	return d.saveMessage(ctx, t, ref.ChatID, ref.MessageID, channelmessage.KindEntry, eventID, nil)
}

func (d *Dispatcher) postProposal(ctx context.Context, ch channel.Channel, t *ent.ChannelThread, a *ent.Action, eventID *int) error {
	if p := incsvc.PreparedOf(a); p != nil && len(p.Payload) > payloadInlineMax {
		if fs, ok := ch.(channel.FileSender); ok {
			name, content := attachment(a, p)
			if _, err := fs.SendFile(ctx, threadRef(t), name, content, fmt.Sprintf("Full payload of proposed action #%d (hash %s)", a.ID, hashPrefix(a.PayloadHash))); err != nil {
				d.log.WarnContext(ctx, "attach payload failed", "action_id", a.ID, "error", err)
			}
		}
	}
	ref, err := ch.Post(ctx, threadRef(t), proposalMessage(a, ch.Capabilities(), d.svc.IncidentURL(a.IncidentID)))
	if err != nil {
		return err
	}
	return d.saveMessage(ctx, t, ref.ChatID, ref.MessageID, channelmessage.KindProposal, eventID, &a.ID)
}

// updateProposal edits the proposal message(s) of an action to show its new state.
func (d *Dispatcher) updateProposal(ctx context.Context, ch channel.Channel, t *ent.ChannelThread, actionID int, ev *ent.TimelineEvent) error {
	a, err := d.ent.Action.Get(ctx, actionID)
	if err != nil {
		return nil
	}
	msgs, err := d.ent.ChannelMessage.Query().Where(channelmessage.ThreadID(t.ID), channelmessage.ActionID(actionID), channelmessage.KindEQ(channelmessage.KindProposal)).All(ctx)
	if err != nil {
		return err
	}
	if len(msgs) == 0 || !ch.Capabilities().EditMessages {
		if ev == nil {
			return nil
		}
		return d.post(ctx, ch, t, channel.Message{Kind: channel.KindEntry, Body: fmt.Sprintf("Action #%d: %s", a.ID, actionStatusLine(a))}, &ev.ID)
	}
	m := proposalMessage(a, ch.Capabilities(), d.svc.IncidentURL(a.IncidentID))
	for _, cm := range msgs {
		if err := ch.Edit(ctx, channel.MessageRef{ChatID: cm.ExternalChatID, MessageID: cm.ExternalMessageID}, m); err != nil {
			d.log.WarnContext(ctx, "edit proposal failed", "channel", ch.Name(), "action_id", actionID, "error", err)
		}
	}
	return nil
}

// postEvents delivers timeline events after the thread cursor.
func (d *Dispatcher) postEvents(ctx context.Context, ch channel.Channel, inc *ent.Incident, t *ent.ChannelThread) error {
	evs, err := d.ent.TimelineEvent.Query().Where(timelineevent.IncidentID(inc.ID), timelineevent.IDGT(t.LastEventID)).
		Order(ent.Asc(timelineevent.FieldID)).Limit(100).All(ctx)
	if err != nil {
		return err
	}
	for _, ev := range evs {
		if err := d.deliver(ctx, ch, inc, t, ev); err != nil {
			return err
		}
		if err := d.ent.ChannelThread.UpdateOne(t).SetLastEventID(ev.ID).Exec(ctx); err != nil {
			return err
		}
		t.LastEventID = ev.ID
	}
	return nil
}

func (d *Dispatcher) deliver(ctx context.Context, ch channel.Channel, inc *ent.Incident, t *ent.ChannelThread, ev *ent.TimelineEvent) error {
	sameChannel := ev.ChannelName != "" && ev.ChannelName == ch.Name()
	url := d.svc.IncidentURL(inc.ID)
	evID := ev.ID
	p := ev.Payload
	switch ev.Type {
	case domain.EventMessage:
		if sameChannel {
			return nil // the user wrote it here: don't echo
		}
		role := str(p, "role")
		author := originLabel(ev)
		switch role {
		case "agent":
			author = "🤖 Agent"
		case "system":
			author = "infra-monitor"
		}
		content := str(p, "content")
		if mid := intVal(p, "message_id"); mid > 0 {
			if m, err := d.ent.Message.Get(ctx, mid); err == nil {
				content = m.Content
			}
		}
		return d.post(ctx, ch, t, channel.Message{Kind: channel.KindEntry, Author: author, Body: content, URL: url}, &evID)
	case domain.EventActionProposed:
		a, err := d.ent.Action.Get(ctx, intVal(p, "action_id"))
		if err != nil || a.Status != action.StatusProposed {
			return nil
		}
		return d.postProposal(ctx, ch, t, a, &evID)
	case domain.EventIncidentMerged:
		if from := intVal(p, "from_incident"); from > 0 {
			return d.post(ctx, ch, t, channel.Message{Kind: channel.KindEntry,
				Body: fmt.Sprintf("🔗 Grouped here: %s (#%d), same problem: %s", str(p, "alert"), from, str(p, "reason"))}, &evID)
		}
		return nil
	case domain.EventActionChecked:
		if held, _ := p["held"].(bool); held {
			if err := d.updateProposal(ctx, ch, t, intVal(p, "action_id"), nil); err != nil {
				return err
			}
			return d.post(ctx, ch, t, channel.Message{Kind: channel.KindEntry, Body: fmt.Sprintf("⏸ Action #%d not run: %s", intVal(p, "action_id"), str(p, "precheck"))}, &evID)
		}
		return nil
	case domain.EventActionApproved, domain.EventActionRejected, domain.EventActionStarted, domain.EventActionExpired:
		if id := intVal(p, "action_id"); id > 0 {
			return d.updateProposal(ctx, ch, t, id, nil)
		}
		return nil
	case domain.EventActionSucceeded, domain.EventActionFailed:
		id := intVal(p, "action_id")
		if str(p, "type") == "agent.reinvestigate" && ev.Type == domain.EventActionSucceeded {
			return nil // the new investigation result is posted instead
		}
		if err := d.updateProposal(ctx, ch, t, id, nil); err != nil {
			return err
		}
		icon, word := "✅", "succeeded"
		body := str(p, "output")
		if ev.Type == domain.EventActionFailed {
			icon, word, body = "❌", "failed", str(p, "error")
		}
		msg := fmt.Sprintf("%s Action #%d %s: `%s`", icon, id, word, str(p, "summary"))
		if body != "" {
			msg += "\n```\n" + body + "\n```"
		}
		return d.post(ctx, ch, t, channel.Message{Kind: channel.KindEntry, Body: msg, URL: url}, &evID)
	case domain.EventIncidentReopened:
		// Recurrence: one short line in the existing thread (the card is edited back to open).
		return d.post(ctx, ch, t, channel.Message{Kind: channel.KindEntry, Body: fmt.Sprintf("🔁 Firing again · recurrence #%d · previous analysis reused", intVal(p, "reopen_count"))}, &evID)
	case domain.EventInvestigationCompleted:
		// One line: the card carries the summary, the UI the full analysis.
		body := fmt.Sprintf("🔎 **%s** (%.0f%%) · %s", str(p, "verdict"), toFloat(p["confidence"])*100, sentences(str(p, "summary"), 280))
		return d.post(ctx, ch, t, channel.Message{Kind: channel.KindEntry, Body: body, URL: url}, &evID)
	case domain.EventInvestigationFailed:
		return d.post(ctx, ch, t, channel.Message{Kind: channel.KindEntry, Body: "⚠️ AI investigation unavailable: " + str(p, "error")}, &evID)
	case domain.EventStatusChanged:
		if ev.ActorType != timelineevent.ActorTypeUser || sameChannel {
			return nil // the card shows it
		}
		return d.post(ctx, ch, t, channel.Message{Kind: channel.KindEntry, Body: fmt.Sprintf("%s → **%s** (%s)", originLabel(ev), str(p, "to"), str(p, "reason"))}, &evID)
	case domain.EventMuteCreated:
		if ev.ActorType != timelineevent.ActorTypeUser || sameChannel {
			return nil
		}
		return d.post(ctx, ch, t, channel.Message{Kind: channel.KindEntry, Body: fmt.Sprintf("🔕 Muted by %s until %s", originLabel(ev), str(p, "ends_at"))}, &evID)
	}
	return nil
}

func toFloat(v any) float64 {
	switch f := v.(type) {
	case float64:
		return f
	case int:
		return float64(f)
	}
	return 0
}

// ---- inbound ----

var _ channel.InboundSink = (*Dispatcher)(nil)

// Handle implements channel.InboundSink. It must return quickly: slow work is queued.
func (d *Dispatcher) Handle(ctx context.Context, ev channel.InboundEvent) channel.Response {
	ctx, span := telemetry.Tracer().Start(ctx, "channel.inbound")
	defer span.End()
	span.SetAttributes(attribute.String("channel", ev.Channel), attribute.String("kind", string(ev.Kind)), attribute.String("command", ev.Command))
	ch, ok := d.channels[ev.Channel]
	if !ok {
		return channel.Response{Silent: true}
	}
	if ev.EventID != "" {
		n, err := d.ent.InboundEvent.Create().SetChannel(ev.Channel).SetEventID(ev.EventID).
			OnConflictColumns(inboundevent.FieldChannel, inboundevent.FieldEventID).DoNothing().ID(ctx)
		if err != nil && !errors.Is(err, stdsql.ErrNoRows) {
			d.log.WarnContext(ctx, "inbound dedup failed, processing anyway", "error", err)
		} else if n == 0 {
			d.log.InfoContext(ctx, "duplicate inbound event ignored", "channel", ev.Channel, "event_id", ev.EventID)
			return channel.Response{Silent: true}
		}
	}
	resp, err := d.handle(ctx, ch, ev)
	d.log.InfoContext(ctx, "inbound event", "channel", ev.Channel, "kind", ev.Kind, "command", ev.Command, "reply_to", ev.ReplyToMessageID,
		"external_user", ev.User.ID, "error", err, "silent", resp.Silent)
	if err != nil {
		telemetry.RecordError(span, err)
		d.log.WarnContext(ctx, "inbound event failed", "channel", ev.Channel, "kind", ev.Kind, "error", err)
		return channel.Response{Text: userError(err), Alert: ev.Kind == channel.InboundButton}
	}
	return resp
}

func userError(err error) string {
	switch {
	case errors.Is(err, incsvc.ErrForbidden):
		return "⛔ Your role does not allow this."
	case errors.Is(err, incsvc.ErrNotFound):
		return "Not found."
	case errors.Is(err, incsvc.ErrConflict), errors.Is(err, incsvc.ErrInvalid):
		return err.Error()
	case errors.Is(err, auth.ErrInvalidCode):
		return "That link code is invalid or expired. Generate a new one in your infra-monitor profile."
	}
	return "Something went wrong: " + err.Error()
}

func (d *Dispatcher) linkHelp() string {
	return "Your account is not linked. Open " + d.svc.PublicURL() + "/profile, generate a link code and send me `/link CODE` (in a private chat with me is best)."
}

// actor resolves the external user; nil when unlinked.
func (d *Dispatcher) actor(ctx context.Context, ch channel.Channel, ev channel.InboundEvent) (*domain.Actor, error) {
	u, err := d.auth.ResolveIdentity(ctx, ch.Type(), ev.User.ID)
	if err != nil || u == nil {
		return nil, err
	}
	if u.Disabled {
		return nil, auth.ErrDisabled
	}
	a := auth.ActorFor(u, ch.Type(), ch.Name())
	return &a, nil
}

// incidentFor finds the incident a reply belongs to.
func (d *Dispatcher) incidentFor(ctx context.Context, ch channel.Channel, ev channel.InboundEvent) (int, bool) {
	for _, mid := range []string{ev.ReplyToMessageID, ev.ThreadID} {
		if mid == "" {
			continue
		}
		cm, err := d.ent.ChannelMessage.Query().Where(channelmessage.Channel(ch.Name()), channelmessage.ExternalChatID(ev.ChatID),
			channelmessage.ExternalMessageID(mid)).WithThread().Only(ctx)
		if err == nil && cm.Edges.Thread != nil {
			return cm.Edges.Thread.IncidentID, true
		}
		t, err := d.ent.ChannelThread.Query().Where(channelthread.Channel(ch.Name()), channelthread.ExternalChatID(ev.ChatID),
			channelthread.ExternalThreadID(mid)).Only(ctx)
		if err == nil {
			return t.IncidentID, true
		}
	}
	return 0, false
}

func (d *Dispatcher) handle(ctx context.Context, ch channel.Channel, ev channel.InboundEvent) (channel.Response, error) {
	switch ev.Kind {
	case channel.InboundButton:
		if ev.Button == nil {
			return channel.Response{Silent: true}, nil
		}
		actor, err := d.actor(ctx, ch, ev)
		if err != nil {
			return channel.Response{}, err
		}
		if actor == nil {
			return channel.Response{Text: "Link your account first: send /link CODE (code from your infra-monitor profile).", Alert: true}, nil
		}
		return d.button(ctx, *actor, *ev.Button)
	case channel.InboundCommand:
		return d.command(ctx, ch, ev)
	case channel.InboundMessage:
		incID, ok := d.incidentFor(ctx, ch, ev)
		if !ok {
			d.log.InfoContext(ctx, "reply does not match an incident thread", "channel", ev.Channel, "chat_id", ev.ChatID, "reply_to", ev.ReplyToMessageID)
			return channel.Response{Silent: true}, nil
		}
		actor, err := d.actor(ctx, ch, ev)
		if err != nil {
			return channel.Response{}, err
		}
		if actor == nil {
			return channel.Response{Text: d.linkHelp()}, nil
		}
		if _, err := d.svc.PostMessage(ctx, *actor, incID, ev.Text); err != nil {
			return channel.Response{}, err
		}
		if !d.svc.AgentEnabled() {
			return channel.Response{Text: "Noted on incident #" + strconv.Itoa(incID) + " (no AI agent configured)."}, nil
		}
		d.typing(ctx, incID)
		return channel.Response{Silent: true}, nil
	}
	return channel.Response{Silent: true}, nil
}

func (d *Dispatcher) button(ctx context.Context, actor domain.Actor, b channel.ButtonAction) (channel.Response, error) {
	var err error
	msg := ""
	switch b.Op {
	case "approve":
		_, err = d.svc.ApproveWithHash(ctx, actor, b.ActionID, b.Arg)
		msg = fmt.Sprintf("Action #%d approved", b.ActionID)
	case "reject":
		_, err = d.svc.Reject(ctx, actor, b.ActionID, "rejected via chat")
		msg = fmt.Sprintf("Action #%d rejected", b.ActionID)
	case "ack":
		_, err = d.svc.Ack(ctx, actor, b.IncidentID)
		msg = "Acknowledged"
	case "resolve":
		_, err = d.svc.Resolve(ctx, actor, b.IncidentID, "resolved via chat")
		msg = "Resolved"
	case "reinvestigate":
		deep := b.Arg == "deep"
		_, err = d.svc.ReinvestigateWith(ctx, actor, b.IncidentID, deep)
		msg = "Investigation queued"
		if deep {
			msg = "Deep investigation queued"
		}
	case "mute":
		dur, perr := time.ParseDuration(firstNonEmpty(b.Arg, "1h"))
		if perr != nil {
			return channel.Response{}, fmt.Errorf("%w: bad duration", incsvc.ErrInvalid)
		}
		_, err = d.svc.MuteIncident(ctx, actor, b.IncidentID, dur, "muted via chat")
		msg = "Muted for " + dur.String()
	default:
		return channel.Response{Text: "Unknown button", Alert: true}, nil
	}
	if err != nil {
		return channel.Response{}, err
	}
	return channel.Response{Text: msg}, nil
}

// CleanupInbound deletes dedup records older than two days (scheduler).
func (d *Dispatcher) CleanupInbound(ctx context.Context) error {
	_, err := d.ent.InboundEvent.Delete().Where(inboundevent.CreatedAtLT(time.Now().Add(-48 * time.Hour))).Exec(ctx)
	return err
}
