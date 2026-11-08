# infra-monitor TODO

Source: MVP brief from the infra-argocd session + decisions taken with Lucas (2026-11-08):
no uber fx, Vue 3 frontend, headless component library, HyperDX-like look, OpenTelemetry from day one,
a single `Channel` interface for every communication system.

Legend: `[ ]` todo, `[x]` done.

## M1 — Skeleton
- [ ] Go module `github.com/xonoko/infra-monitor`, layout `cmd/infra-monitor`, `internal/...`, `web/`
- [ ] cobra CLI: `serve`, `migrate up|status`, `user create|reset-password`, `token create`, `version`
- [ ] viper config: YAML file + env overrides (`INFRA_MONITOR_` prefix), `*_file` secret loading, validation
- [ ] slog logger (JSON in prod, text in dev) with `trace_id`/`span_id` injection
- [ ] Explicit wiring (no fx): constructors + errgroup lifecycle + graceful shutdown
- [ ] OpenTelemetry: tracer provider (OTLP grpc/http, noop when disabled), propagators, meter provider with Prometheus exporter on `/metrics`
- [ ] Tracing on: HTTP server (per huma operation), outbound HTTP clients, pgx (covers ent), jobs, channels
- [ ] pgx pool shared by ent (stdlib.OpenDBFromPool)
- [ ] ent schemas for the whole domain model
- [ ] Versioned migrations: Atlas generates SQL from ent; embedded migrator applies them under an advisory lock; `--migrate` flag on serve
- [ ] huma v2 on chi under `/api/v1`, `/openapi.json`, docs UI
- [ ] `/healthz`, `/readyz`, `/metrics`
- [ ] Makefile (build, test, test-integration, lint, generate, migration, web, docker, helm-lint)
- [ ] Dockerfile (node build stage + go build + distroless nonroot)
- [ ] docker-compose for local Postgres
- [ ] GitHub Actions: test + lint + web build + helm lint/template + docker build/push ghcr (sha + semver + latest)
- [ ] Helm chart basics

## M2 — Ingestion
- [ ] Postgres job queue (`FOR UPDATE SKIP LOCKED`, retries/backoff, unique keys, `traceparent` per job, LISTEN wakeups)
- [ ] Jobs: process_signal, investigate_incident, agent_chat_reply, execute_action, deliver_notification, heartbeat_ping, reconcile_source, expire_actions, resolve_incidents, check_heartbeats
- [ ] Source interface + registry; webhook `POST /api/v1/sources/{name}/webhook` with per-source bearer/basic auth; respond fast, process async
- [ ] Alertmanager source: webhook v4 payload parsing; reconciler (GET /api/v2/alerts) for missed/stale alerts
- [ ] Signals upsert by (source, fingerprint)
- [ ] Heartbeat short-circuit (Watchdog), heartbeat table, outbound ping, "source silent" internal incident, metrics
- [ ] Correlation: configurable group_by, attach/create, reopen window (flapping)
- [ ] Debounce before investigation
- [ ] Resolve incident when all signals resolved + grace period
- [ ] Timeline events written in the same transaction as every mutation (+ trace_id)
- [ ] LISTEN/NOTIFY fan-out -> SSE `/api/v1/events` (global + per incident)
- [ ] Mutes (matchers, starts/ends) suppress notifications, incident marked muted
- [ ] API: incidents list/filter/paginate, detail, timeline, signals; sources/heartbeats status

## M3 — Auth
- [ ] Local users: argon2id, login, server-side sessions in Postgres, secure cookies, CSRF (double-submit header)
- [ ] OIDC (go-oidc + oauth2, code + PKCE), auto-provision, groups claim -> role mapping
- [ ] Both modes at once, local login can be disabled
- [ ] Roles viewer/operator/admin enforced on every API op
- [ ] API tokens (hashed, Bearer)
- [ ] CLI: `user create --password-stdin`, `user reset-password`, `token create`
- [ ] Profile: Telegram link code generation; ExternalIdentity
- [ ] Admin API: users list/update role/disable, tokens list/create/revoke

## M4 — Agent + actions
- [ ] Agent interface (Investigate, Chat)
- [ ] HolmesGPT client (`/api/chat`, model key, X-API-Key, response_format json_schema, fenced-JSON fallback, timeouts)
- [ ] Fake Holmes server for tests
- [ ] Investigations job with limits (max concurrent, per hour, cooldown, no re-investigation on repeats), fail-open timeout
- [ ] Per-incident conversation state; chat job; agent replies as messages
- [ ] Action registry: key, JSON schema, Validate, Execute; agent can only propose registered types
- [ ] Executors: k8s.rollout_restart, k8s.delete_pod, k8s.scale (bounded), alertmanager.silence, agent.reinvestigate
- [ ] k8s clients: in-cluster + per-cluster kubeconfigs by `cluster` label; namespace allow/deny (kube-system denied)
- [ ] Approval flow (operator+), auto-approve per type (default off), proposal TTL/expiry, idempotency, audit in timeline
- [ ] Notification routing policy (matchers, verdicts, severities -> channels), fail open with "AI investigation unavailable"
- [ ] API: messages GET/POST, actions GET/POST, approve/reject, ack/resolve/reinvestigate, mutes CRUD

## M5 — Channels (single `Channel` interface)
- [ ] `Channel` interface: Name, Type, Capabilities, UpsertCard, Post, Edit, Receive, WebhookHandler
- [ ] Neutral message model (title, fields, markdown body, buttons) rendered per adapter
- [ ] Core dispatcher: ChannelThread/Delivery persistence, card edit-in-place, timeline replies, mirroring without echo, fallback text for no-button channels
- [ ] Inbound handling in core: identity resolution, role check, same service methods as API
- [ ] Telegram adapter: HTML rendering + safe truncation (4096), inline keyboards (compact callback_data), answerCallbackQuery, edit after decision, typing action, replies -> chat, commands /link /status /ack /mute /resolve /approve /reject
- [ ] Telegram rate limiter + retry_after handling
- [ ] Telegram polling (singleton lease) + webhook mode (secret_token header)
- [ ] docs/channels/slack.md (design only) and verification that the interface fits

## M6 — HA hardening
- [ ] Advisory-lock leases (held session, loss detection) for pollers/reconcilers/schedulers
- [ ] Multi-replica integration test (two servers on one DB)
- [ ] Helm HA values (PDB, anti-affinity)
- [ ] docs: architecture.md, sources.md, channels/slack.md, configuration reference, deployment (env/secret keys)

## UI (Vue 3 + TS + Vite, embedded)
- [ ] Reka UI (headless) + TanStack Table + TanStack Vue Query, own CSS variables, no Tailwind
- [ ] Generated TS client from huma OpenAPI (openapi-typescript + openapi-fetch)
- [ ] HyperDX-inspired: dark default + light theme, dense rows, monospace data, left sidebar, search bar with `key:value` syntax, time range picker, severity histogram
- [ ] Login (local + OIDC buttons)
- [ ] Incident list (live via SSE, filters, keyboard j/k)
- [ ] Incident right-side drawer + full page: overview/AI summary, timeline, signals, actions (approve/reject), chat (live)
- [ ] Mutes page; sources/channels/heartbeats status page; profile (link code); admin users/tokens
- [ ] "View trace" links (tracing.ui_url_template)

## Helm chart
- [ ] Deployment, Service, Ingress, ServiceAccount, RBAC (toggleable), ConfigMap from values.config, existingSecret refs, checksum annotations
- [ ] External Postgres (host/secret or DSN secret), optional CNPG Cluster
- [ ] ServiceMonitor / VMServiceScrape toggles
- [ ] Restricted PSS securityContext, resources on all containers, PDB/anti-affinity
- [ ] helm lint + template tests in CI

## Testing
- [ ] Unit tests per package
- [ ] Integration tests against Postgres (docker-compose), fakes: Holmes, Telegram Bot API, Alertmanager
- [ ] End-to-end local run (docker-compose + fake Holmes + webhook replay)

## Deployment (manager cluster, with the infra-argocd session)
- [ ] Image pushed to ghcr
- [ ] Hand-off doc: env/secret keys, chart values
- [ ] infra side: DB, secrets, Authentik provider, Argo app, Alertmanager route, Holmes/LiteLLM, heartbeat check
- [ ] Verify in cluster: webhook ingestion, heartbeat, investigation, Telegram card + buttons + replies, UI login (OIDC), actions
