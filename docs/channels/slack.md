# Slack channel (design, not implemented)

This describes how a Slack adapter implements the same `channel.Channel` interface as Telegram
(`internal/channel/channel.go`). The core (routing, identity linking, role checks, mirroring, persistence of
threads and messages, fan-out rate) does not change: a Slack adapter only translates the neutral `Card` /
`Message` model to Block Kit and Slack payloads to `InboundEvent`s.

## Mapping

| Concept | Slack |
| --- | --- |
| Incident card | `chat.postMessage` in the configured channel, edited with `chat.update` |
| Thread | the card's `ts` is the thread root: `ThreadRef{ChatID: <channel id>, ThreadID: <card ts>, CardMessageID: <card ts>}` |
| Timeline entry | `chat.postMessage` with `thread_ts = ThreadID` |
| Buttons | Block Kit `actions` block, one `button` per `channel.Button`; `action_id` and `value` = `ButtonAction.Encode()` (e.g. `ay:42`); `style` `primary` / `danger` map directly; URL buttons use `url` |
| Replies → chat | Events API `message` events with `thread_ts` → `InboundMessage` |
| Button presses | interactivity `block_actions` payloads → `InboundButton` |
| Commands | slash command `/infra-monitor <sub> [args]` → `InboundCommand{Command: sub, Args}` |
| Responses | ephemeral: `response_url` (buttons, slash commands) or `chat.postEphemeral` |
| Markup | `mrkdwn` |

### Rendering (`Markup: "slack-mrkdwn"`)

The channel markdown subset converts as: `**bold**` → `*bold*`, `_italic_` → `_italic_`, `` `code` `` and
```` ``` ```` blocks unchanged, `[text](url)` → `<url|text>`, `# heading` → `*heading*`, lists → `•`.
`&`, `<`, `>` are escaped as `&amp;` `&lt;` `&gt;` (Slack's only required escapes).

Cards are blocks: a `header` block (severity emoji + title, 150 chars max), a `context` block (status,
severity, `#id`), a `section` with `fields` (cluster, namespace, verdict; 10 fields max), a `section` for the
AI summary, a `context` for the notice, and an `actions` block. Limits: 50 blocks per message, 3000 chars per
section text, 40,000 chars for the fallback `text` (always set, used in notifications). Long analyses are split
across section blocks up to a budget, then end with a "Full text in UI" link, like Telegram.

### Receiving

- **Socket Mode (preferred)**: no public URL. `apps.connections.open` with an app-level token (`xapp-…`,
  scope `connections:write`) returns a WebSocket URL; envelopes (`events_api`, `interactive`, `slash_commands`)
  must be acked within 3 s, so the adapter acks first, then calls the sink. `Capabilities.SingletonReceiver =
  true`: the core runs `Receive` under the same Postgres lease as the Telegram poller
  (`channel:<name>:receiver`), so exactly one replica holds the socket. (Slack accepts up to 10 sockets and
  load-balances between them; one is simpler and keeps per-thread ordering.) Reconnect on `disconnect`
  envelopes and socket errors with backoff.
- **HTTP mode**: `WebhookHandler(sink)` serves `/api/v1/channels/<name>/webhook` for the Events API,
  interactivity and slash command URLs (all three can point to it; the handler tells them apart by content:
  JSON `url_verification` / `event_callback`, form field `payload` for interactivity, form field `command` for
  slash commands). Every request is verified with the signing secret: `v0=` HMAC-SHA256 of
  `v0:<X-Slack-Request-Timestamp>:<body>` compared in constant time with `X-Slack-Signature`, and the timestamp
  must be within 5 minutes. Answer 200 within 3 s and dispatch asynchronously (like the Telegram webhook);
  ignore Events API retries (`X-Slack-Retry-Num`) that were already processed (dedup on `event_id`).
  `Capabilities.SingletonReceiver = false`; `Receive` returns nil.

### Inbound mapping

- `message` event in the configured channel with `thread_ts` set and `thread_ts != ts`, no `bot_id`, no
  `subtype` → `InboundMessage{ChatID: channel, MessageID: ts, ThreadID: thread_ts, ReplyToMessageID: thread_ts,
  Text}`. Slack replies point to the thread, not to one message, so `ReplyToMessageID` is the root `ts` (the
  card), which the core already stores as a `ChannelMessage` of kind `card`. Top-level messages are ignored.
- `block_actions` → `InboundButton{Button: DecodeButtonAction(action.value), MessageID: container.message_ts,
  ThreadID: message.thread_ts, ChatID: channel.id}`; the `Response` is sent as an ephemeral message through
  `response_url` (`Alert` has no popup equivalent; ephemeral is the closest).
- Slash command `/infra-monitor ack 42` → `InboundCommand{Command: "ack", Args: ["42"]}`. Slash commands carry
  no thread context, so commands that act on an incident take its id as an argument in Slack (the core already
  accepts `/approve 42`; incident commands should accept an optional id the same way). The `Response` goes into
  the HTTP response body (HTTP mode) or `response_url` (Socket Mode), ephemeral.
- `ExternalUser{ID: user id (U…/W…), Name: profile.display_name or real_name, Username: name}` (one cached
  `users.info` call per new user).

### Outbound behaviour

- `UpsertCard(nil, card)` → `chat.postMessage`; `UpsertCard(&ref, card)` → `chat.update(channel, ts)`;
  `message_not_found` → post a new card (same as Telegram's "message to edit not found").
- `Post` → `chat.postMessage(thread_ts)`, `Edit` → `chat.update` (an empty `Buttons` list drops the actions block).
- `SetTyping` → no-op returning nil: Slack has no bot typing indicator in channels (`Capabilities.Typing = false`).
- Rate limits: `chat.postMessage` is limited to about 1 message per second per channel, `chat.update` is
  Tier 3 (~50/min). The adapter uses a per-channel limiter and honours `429` + `Retry-After`.
- Scopes: `chat:write`, `channels:history` (and `groups:history` for private channels), `commands`,
  `users:read` (+ `users:read.email` for email matching). The bot must be invited to the channel.

### Linking users

- `/infra-monitor link CODE` with the code from the UI profile page, as with Telegram (`ExternalIdentity`
  with `channel_type = "slack"`, `external_id = Slack user id`).
- Optional automatic matching by verified email: look up the Slack user's `profile.email` (`users.info`,
  needs `users:read.email`) and link to the infra-monitor user with the same email. This needs the email to
  reach the core (see below).

## Does the interface fit?

Yes, checked method by method, with **no breaking change**:

| Method / type | Fit |
| --- | --- |
| `Name`, `Type`, `Capabilities` | yes (`Markup: "slack-mrkdwn"`, `MaxMessageLen: 40000`, `Typing: false`, `SingletonReceiver` depends on mode) |
| `UpsertCard` | yes: `ts` is both `ThreadID` and `CardMessageID` |
| `Post`, `Edit` | yes: `MessageRef{ChatID: channel, MessageID: ts}` |
| `SetTyping` | yes, as a no-op |
| `Receive` | yes: Socket Mode under the lease; returns nil in HTTP mode |
| `WebhookHandler` | yes: one handler for events, interactivity and slash commands |
| `Card`, `Message`, `Button`, `ButtonAction` | yes: `Encode()` fits `action_id` (255 chars max) |
| `InboundEvent`, `Response` | yes: `ThreadID` and `ReplyToMessageID` both set to `thread_ts` |

Two things the **core** must keep doing (true today, listed so they stay true):

1. Resolve the incident of an inbound event from `ReplyToMessageID` via `ChannelMessage` (the card is stored
   as a `ChannelMessage` of kind `card`), falling back to `ThreadID` against `ChannelThread.external_thread_id`.
2. Accept an explicit incident id argument for incident commands (`ack 42`, `mute 42 1h`), since Slack slash
   commands have no thread context.

One **optional, additive** change: an `Email string` field on `channel.ExternalUser` (filled only when the
platform provides a verified address) would enable automatic linking by email. Without it, linking by code
works unchanged.
