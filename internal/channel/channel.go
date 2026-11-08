// Package channel defines the single interface every communication system (Telegram, Slack,
// email, ...) implements, plus the neutral message model the core renders into.
//
// Adapters only translate: the core builds Cards and Messages, resolves users and roles,
// persists threads/messages, rate-limits fan-out and mirrors activity across channels.
package channel

import (
	"context"
	"net/http"
)

// Capabilities describe what an adapter supports so the core can degrade gracefully.
type Capabilities struct {
	Buttons       bool   // inline buttons (callbacks)
	URLButtons    bool   // link buttons
	EditMessages  bool   // edit previously sent messages
	Threads       bool   // replies grouped under a root message
	Typing        bool   // "typing..." indicator
	MaxMessageLen int    // characters, after rendering
	Markup        string // "telegram-html" | "slack-mrkdwn" | "plain"
	// SingletonReceiver: Receive must run on exactly one replica (e.g. Telegram getUpdates,
	// Slack Socket Mode). The core wraps Receive in a Postgres lease.
	SingletonReceiver bool
}

// Field is a short labeled value on a card (cluster, namespace, duration...).
type Field struct {
	Name  string
	Value string
}

type ButtonStyle string

const (
	StyleDefault ButtonStyle = ""
	StylePrimary ButtonStyle = "primary"
	StyleDanger  ButtonStyle = "danger"
)

// Button is either a callback (Action set) or a link (URL set).
type Button struct {
	Label  string
	Style  ButtonStyle
	Action *ButtonAction
	URL    string
}

// Card is the incident summary, edited in place when the incident changes.
type Card struct {
	IncidentID int
	Title      string
	Status     string // open | acknowledged | resolved | closed
	Severity   string
	Verdict    string // actionable | noise | unknown
	Muted      bool
	Fields     []Field
	// Summary is markdown (AI summary). Notice is a short highlighted line, e.g. "AI investigation unavailable".
	Summary string
	Notice  string
	// PendingActions lists proposals waiting for a decision (each also gets its own proposal message).
	PendingActions []string
	Buttons        []Button
	URL            string // deep link to the UI incident page
}

type MessageKind string

const (
	KindEntry    MessageKind = "entry"    // timeline entry (agent reply, mirrored chat, status change)
	KindProposal MessageKind = "proposal" // action proposal with approve/reject buttons
)

// Message is a timeline entry posted as a reply in the incident thread.
// Body uses a small markdown subset: **bold**, _italic_, `code`, ```code blocks```, [text](url), "- " lists, "# " headings.
type Message struct {
	Kind    MessageKind
	Author  string // e.g. "Agent", "Lucas via UI"
	Body    string
	Footer  string
	Buttons []Button
	URL     string // where the full content lives when Body is truncated
}

// ThreadRef locates the incident conversation in the external system.
type ThreadRef struct {
	ChatID        string
	ThreadID      string // root message id / thread_ts
	CardMessageID string
}

// MessageRef locates one message.
type MessageRef struct {
	ChatID    string
	MessageID string
}

// Channel is implemented by every communication system.
type Channel interface {
	Name() string // instance name from config
	Type() string // "telegram", "slack", ...
	Capabilities() Capabilities

	// UpsertCard creates the incident card (thread == nil) or edits it in place.
	UpsertCard(ctx context.Context, thread *ThreadRef, card Card) (ThreadRef, error)
	// Post adds a message to the incident thread.
	Post(ctx context.Context, thread ThreadRef, msg Message) (MessageRef, error)
	// Edit replaces a message previously returned by Post (no-op error if unsupported).
	Edit(ctx context.Context, ref MessageRef, msg Message) error
	// SetTyping shows activity in the thread (best effort).
	SetTyping(ctx context.Context, thread ThreadRef) error

	// Receive delivers inbound events to sink until ctx is done (poller / socket).
	// Return nil immediately when the channel is send-only or uses WebhookHandler.
	Receive(ctx context.Context, sink InboundSink) error
	// WebhookHandler returns the handler mounted at /api/v1/channels/{name}/webhook,
	// or nil when the channel does not receive over HTTP.
	WebhookHandler(sink InboundSink) http.Handler
}

// InboundSink is implemented by the core.
type InboundSink interface {
	Handle(ctx context.Context, ev InboundEvent) Response
}

type InboundKind string

const (
	InboundMessage InboundKind = "message" // free text (a reply in an incident thread = chat with the agent)
	InboundButton  InboundKind = "button"  // button press
	InboundCommand InboundKind = "command" // /status, /ack, /mute 1h, /link CODE, /approve 42 ...
)

// ExternalUser identifies the sender in the external system.
type ExternalUser struct {
	ID       string
	Name     string
	Username string
}

type InboundEvent struct {
	Channel string // instance name
	Kind    InboundKind
	User    ExternalUser
	ChatID  string
	// MessageID of the inbound message; ReplyToMessageID is the message it replies to (used to find the incident).
	MessageID        string
	ReplyToMessageID string
	ThreadID         string
	Text             string
	Button           *ButtonAction
	Command          string   // lowercase, without slash or bot suffix
	Args             []string // command arguments
}

// Response is shown to the sender: as a callback answer for buttons, or as a reply otherwise.
type Response struct {
	Text  string // markdown subset
	Alert bool   // show as a popup (buttons)
	// Silent: nothing to say (the action result will appear through the normal fan-out).
	Silent bool
}
