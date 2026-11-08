# Architecture

infra-monitor is one Go binary (`infra-monitor serve`) that runs the HTTP API, the embedded Vue UI and all
background workers. Every replica is identical and stateless: PostgreSQL is the only shared state, so
`replicas: 1` and `replicas: N` work without Redis or the Kubernetes API.

```
 vmalert ──► Alertmanager ──webhook──►  /api/v1/sources/{name}/webhook ─┐
                    ▲                                                     │ upsert signals (+ job, same tx)
                    └── reconcile (GET /api/v2/alerts, every 5m) ─────────┤
                                                                          ▼
                                     ┌──────────────── PostgreSQL ─────────────────┐
                                     │ signals · incidents · timeline_events ·     │
                                     │ investigations · actions · messages · mutes │
                                     │ jobs (queue) · sessions · channel_threads   │
                                     └──────┬──────────────┬───────────────┬──────┘
                       SKIP LOCKED workers  │   LISTEN/NOTIFY fan-out      │ advisory-lock leases
                                            ▼              ▼               ▼
   process_signal → correlate → debounce → investigate (Holmes) → route → channel_sync (Telegram…)
                                            │              │               └ telegram poller, scheduler
                                            ▼              ▼
                                  execute_action (k8s,   SSE /api/v1/events → UI (all replicas)
                                  silence) after approval
```

## Packages

| Package | Role |
| --- | --- |
| `cmd/infra-monitor`, `internal/cli` | cobra CLI: `serve`, `migrate up/status`, `user create/reset-password/list`, `token create`, `version`, `openapi` |
| `internal/app` | explicit wiring (no DI container) and the errgroup lifecycle; the scheduler loop |
| `internal/config` | viper: YAML + `INFRA_MONITOR_*` env, `*_file` secrets, validation |
| `internal/telemetry`, `internal/logger` | OpenTelemetry traces/metrics (Prometheus `/metrics`, optional OTLP), slog with `trace_id` |
| `internal/db` | pgx pool (traced with otelpgx) shared by ent, embedded migrations under an advisory lock |
| `internal/db/ent/schema` | the domain model |
| `internal/queue` | Postgres job queue: `FOR UPDATE SKIP LOCKED`, retries with backoff, snooze, unique keys, traceparent per job, NOTIFY wakeups |
| `internal/lease` | singletons via session advisory locks on a held connection, with loss detection |
| `internal/pubsub` | one LISTEN connection per replica, fanned out to SSE clients and workers |
| `internal/incident` | the core service (see below) |
| `internal/source/alertmanager` | webhook v4 parser + Alertmanager v2 client (alerts, silences) |
| `internal/agent`, `internal/agent/holmes` | the `Agent` interface and the HolmesGPT client |
| `internal/actions`, `.../k8s`, `.../amsilence` | executor registry (JSON schema + policy), k8s and silence executors |
| `internal/channel`, `.../telegram`, `.../dispatch` | the single `Channel` interface, the Telegram adapter, and the dispatcher |
| `internal/auth` | argon2id users, DB sessions, OIDC (PKCE), API tokens, chat identity linking |
| `internal/api` | huma v2 on chi, auth/CSRF/role middleware, SSE, source and channel webhooks, SPA |
| `web/` | Vue 3 + TS UI, built into `web/dist` and embedded with `go:embed` |

## The core service

Every mutation goes through `incident.Service` with a `domain.Actor` (user, agent or system, plus the channel of
origin) and writes a `TimelineEvent` in the same transaction. Roles are checked there, so the API, the UI and
Telegram share one permission model and one audit trail.

Writing a timeline event also enqueues a `channel_sync` job (unique per channel+incident) for every channel the
incident was routed to. Jobs run after commit, so the fan-out never sees uncommitted data.

### Pipeline

1. **Ingest**: webhook → parse → upsert `signals` by `(source, fingerprint)`. Only transitions (new, re-fire,
   resolved) enqueue `process_signal`; Alertmanager's repeat notifications only refresh `last_seen_at`.
2. **Heartbeat**: signals matching `heartbeat.matchers` (Watchdog) never create incidents. They refresh the
   source heartbeat and queue a ping to `heartbeat.ping_url`. A heartbeat silent for `silent_after` opens an internal
   critical incident ("source X silent") that resolves itself when the heartbeat comes back.
3. **Correlate** (under a per-key transaction advisory lock): attach to the open incident with the same grouping key
   (`correlation.group_by`), else reopen one resolved within `reopen_window`, else create one. Active mutes apply.
4. **Debounce**: `investigate_incident` runs after `correlation.debounce`; `notify_failopen` runs after
   debounce + `agent.notify_timeout`.
5. **Investigate**: limits are enforced globally through the database (max concurrent, max per hour,
   cooldown on reopen). The agent's analysis, verdict and proposals are stored; proposals are validated against the
   action registry (unknown types are dropped and logged on the timeline).
6. **Route**: rules from `routing` (or the default) decide the channels from labels, verdict and severity. Muted
   incidents and incidents resolved before any notification stay in the UI. If the investigation fails or takes too
   long, `notify_failopen` routes anyway and cards show "AI investigation unavailable".
7. **Resolve**: when every signal is resolved, `resolve_incident` runs after `resolve_grace` and resolves unless
   something re-fired. Pending proposals expire.

### Cost controls

An alert storm must never produce a large LLM bill (LiteLLM also enforces budgets on its side):

- Automatic investigations only for `agent.investigate_severities` (default critical, warning).
- **Storm mode**: when more than `agent.storm.threshold` incidents open within `agent.storm.window`, new incidents
  are attached to one "Alert storm" incident (`incidents.storm_id`). Only the storm is investigated (once, with the
  list of grouped alerts) and notified; it resolves when every grouped incident is resolved.
- **Hard caps** on every agent call, investigations and chat replies alike: `max_concurrent`, `max_per_hour`,
  `max_per_day`, plus `cooldown` against automatic re-investigation of reopened incidents.
- **Budget errors are final**: a budget or rate-limit error (HTTP 429, LiteLLM budget_exceeded, Holmes 5204) is not
  retried. The incident fails open ("AI budget reached") and all agent calls pause for `agent.budget_pause`.
- The default model is cheap (`agent.model`, first analysis only). `agent.deep_model` (Sonnet) is used for "dig deeper",
  chat replies (`chat_model`) and the final check before any action runs (`check_model`).
- **Final check**: right before an approved action runs (human or auto-approved), the agent re-reads the live state and
  answers proceed only if the action is still needed, correct and safe, with no clearly better option (e.g. a GitOps
  change for an Argo-managed object). Otherwise the action is canceled with the reason. If the check can't run (caps,
  budget, agent down), the approved action proceeds and the timeline says so. No model ever executes anything: the
  executor applies exactly the hash-approved payload.
- Metrics: `infra_monitor_investigations{outcome}`, `infra_monitor_investigations_skipped{reason}`
  (storm, cap, budget, severity, state), `infra_monitor_agent_tokens{type,model}`, `infra_monitor_agent_cost_usd`.
  `/api/v1/status` shows calls in the last hour / 24h and the budget pause.

### Actions

Agents and users can only propose. Proposals are schema-validated and policy-checked (namespace deny/allow lists,
bounds) at proposal time and again at approval. Approval needs `operator`; `actions.auto_approve` lists types that
skip it (default none). Namespaces override that list with annotations
(`infra-monitor.xonoko.com/auto-approve: "agent.reinvestigate,k8s.delete_pod"` or `"none"`,
`infra-monitor.xonoko.com/max-auto-per-hour: "1"`), read through the executors' cluster client and cached for a minute.
Loop guards: at most `actions.max_auto_per_hour` automatic executions per incident per hour, and an investigation
triggered by an action can't propose another re-investigation (`allow_reinvestigate_chain: false`). `execute_action` has a single attempt and runs only from `approved`, so a crash can't run an
action twice. A run lost to a crashed worker is marked failed by the scheduler.

## Channels

`channel.Channel` is the only interface for communication systems. The core builds a neutral `Card` / `Message`
model; adapters only render and deliver. The dispatcher stores `channel_threads` (incident ↔ external thread, card
message id, card hash, event cursor) and `channel_messages` (every delivered message, so replies and button presses
map back to an incident and proposals can be edited after a decision).

`channel_sync` is idempotent: it edits the card only when its hash changed and posts timeline events after the
thread's cursor, updating the cursor after each message. Messages that originated in the same channel instance are
not echoed. Inbound events (replies, buttons, commands) resolve the external user through `external_identities`,
then call the same service methods as the UI.

Receivers that need exactly one consumer (Telegram `getUpdates`, Slack Socket Mode) declare
`SingletonReceiver` and run under the lease `channel:<name>:receiver`. Telegram webhook mode lets every replica
receive; `setWebhook` runs once under a lease.

## High availability

| Concern | Mechanism |
| --- | --- |
| Background work | job queue with `SKIP LOCKED`, safe with N replicas |
| Exactly-one work (poller, scheduler, reconcile, heartbeat checks) | session advisory locks held on a dedicated connection; the lease is dropped and the work cancelled if the connection dies |
| Live UI updates | triggers `pg_notify` on timeline inserts; every replica LISTENs and pushes to its SSE clients |
| Sessions, OIDC state, link codes | stored in Postgres |
| Migrations | applied at startup under `pg_advisory_lock` (or `infra-monitor migrate up`) |

`internal/app/app_test.go` runs two replicas on one database and checks cross-replica sessions, SSE fan-out and
that only one replica holds the scheduler lease.

## Observability

- Traces: HTTP server spans named after the chi route, client spans for every outbound call, a span per SQL query
  (otelpgx), one span per job. Jobs carry the `traceparent` of their producer, so webhook → correlate → investigate
  → Telegram is one trace. Timeline events and incidents store `trace_id`; set `telemetry.tracing.ui_url_template`
  to get "View trace" links in the UI.
- Logs: JSON slog with `trace_id` / `span_id`.
- Metrics (`/metrics`): `infra_monitor_signals_received`, `_incidents_opened`, `_open_incidents`,
  `_investigations`, `_actions`, `_notifications_routed`, `_jobs_processed`, `_job_duration_seconds`,
  `_jobs_pending`, `_lease_held`, plus Go/process metrics.
