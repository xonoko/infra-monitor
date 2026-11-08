# Telegram channel

One incident = one message in the group (the **card**), edited in place when the incident changes.
Everything else about the incident (AI analysis, action proposals, decisions, messages typed in the UI) is
posted as **replies to the card**. Replying to the card or to any message in its thread is a chat with the
agent about that incident.

## Setup

1. **Create a bot** with [@BotFather](https://t.me/BotFather): `/newbot`, pick a name and a username
   ending in `bot`. Keep the token secret (store it in Vault / a Kubernetes secret, key `telegram-bot-token`).
   Use a dedicated bot: a bot token can only have **one** consumer of `getUpdates` (a second poller gets
   `409 Conflict`), so never share it with another application.
2. Optional, in BotFather: `/setcommands` with
   ```
   status - Open incidents
   ack - Acknowledge the incident (reply to its card)
   resolve - Resolve the incident (reply to its card)
   mute - Mute the incident, e.g. /mute 1h (reply to its card)
   reinvestigate - Run the AI investigation again (reply to its card)
   approve - Approve an action: /approve 42
   reject - Reject an action: /reject 42
   link - Link your Telegram account: /link CODE
   help - Help
   ```
3. **Add the bot to the group.** Privacy mode can stay **on**: the bot still receives replies to its own
   messages, button presses and commands addressed to it. If plain `/status` (without `@yourbot`) is not
   delivered in your group, use `/status@yourbot` or make the bot a group admin.
4. **Find the chat id**: send a message in the group, then open
   `https://api.telegram.org/bot<TOKEN>/getUpdates` (stop infra-monitor first, or it will consume the update)
   and read `message.chat.id`. Groups have negative ids; supergroups start with `-100`.
   If Telegram upgrades the group to a supergroup (this happens when some admin features are enabled),
   the id changes: infra-monitor logs `telegram group was upgraded to a supergroup` with `new_chat_id`.
5. Configure the channel:

```yaml
channels:
  - name: infra            # instance name, used in routing and URLs
    type: telegram
    bot_token_file: /etc/infra-monitor/secrets/telegram-bot-token
    chat_id: -5054234212
    mode: polling          # or webhook
    # webhook_secret_file: /etc/infra-monitor/secrets/telegram-webhook-secret
    messages_per_minute: 18 # Telegram allows ~20 messages/minute per group
routing:
  - channels: [infra]
    verdicts: [actionable]
  - channels: [infra]
    severities: [critical]
```

## Polling vs webhook

| | polling (default) | webhook |
| --- | --- | --- |
| Needs a public URL | no | yes: `https://<public_url>/api/v1/channels/<name>/webhook` |
| Receivers | exactly one replica (held by a Postgres advisory lock, automatic failover) | any replica |
| Setup | none; the webhook is deleted at start | infra-monitor calls `setWebhook` with the secret token at start |

Webhook requests must carry `X-Telegram-Bot-Api-Secret-Token` equal to `webhook_secret`; others get 401.
Updates are processed asynchronously so Telegram always gets a fast answer.

## Linking accounts

Anyone in the group can read cards. To chat with the agent, acknowledge, mute or approve actions, a Telegram
user must be **linked** to an infra-monitor user with the operator role (or higher):

1. In the UI, open your profile and click **Link Telegram**: you get a short code (valid 10 minutes).
2. Send `/link CODE` to the bot, in the group or in a private chat with the bot (private is better).

Unlinked users who press a button or reply get a short message explaining how to link. Roles are checked on
every button press and message.

## Behaviour

- **Card**: severity (🔴 critical, 🟠 warning, 🔵 info, ⚪ other), status (🔥 open, 👀 acknowledged, ✅ resolved,
  🔕 muted), cluster/namespace, verdict, AI summary, pending actions, and buttons: Ack, Mute 1h, Reinvestigate,
  Open in UI. "Open in UI" is only shown when `server.public_url` is a public http(s) URL (Telegram rejects
  localhost links).
- **Proposals** are posted as replies with Approve / Reject buttons; after a decision the message is edited
  to show who decided and through which channel, and the buttons disappear.
- **Mirroring**: messages and decisions made in the UI or another channel are posted in the thread
  ("Lucas via UI: ..."). Messages typed in Telegram are not echoed back to Telegram.
- **Long texts**: a message is at most 4096 characters; long analyses are cut cleanly (HTML is never broken)
  and end with a "Full text in UI" link.
- **Rate limits**: sends and edits are limited per group (`messages_per_minute`) and globally (25/s);
  `429 retry_after` responses are honoured and retried.
- The bot token never appears in logs or errors (`***`).
