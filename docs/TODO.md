# infra-monitor TODO

Source: MVP brief from the infra-argocd session + decisions taken with Lucas (2026-11-08):
no uber fx, Vue 3 frontend, headless component library, HyperDX-like look, OpenTelemetry from day one,
a single `Channel` interface for every communication system.

Legend: `[ ]` todo, `[x]` done.

## M1 — Skeleton
- [x] Go module `github.com/xonoko/infra-monitor`, layout `cmd/infra-monitor`, `internal/...`, `web/`
- [x] cobra CLI: `serve`, `migrate up|status`, `user create|reset-password`, `token create`, `version`
- [x] viper config: YAML file + env overrides (`INFRA_MONITOR_` prefix), `*_file` secret loading, validation
- [x] slog logger (JSON in prod, text in dev) with `trace_id`/`span_id` injection
- [x] Explicit wiring (no fx): constructors + errgroup lifecycle + graceful shutdown
- [x] OpenTelemetry: tracer provider (OTLP grpc/http, noop when disabled), propagators, meter provider with Prometheus exporter on `/metrics`
- [x] Tracing on: HTTP server (per huma operation), outbound HTTP clients, pgx (covers ent), jobs, channels
- [x] pgx pool shared by ent (stdlib.OpenDBFromPool)
- [x] ent schemas for the whole domain model
- [x] Versioned migrations: Atlas generates SQL from ent; embedded migrator applies them under an advisory lock; `--migrate` flag on serve
- [x] huma v2 on chi under `/api/v1`, `/openapi.json`, docs UI
- [x] `/healthz`, `/readyz`, `/metrics`
- [x] Makefile (build, test, test-integration, lint, generate, migration, web, docker, helm-lint)
- [x] Dockerfile (node build stage + go build + distroless nonroot)
- [x] docker-compose for local Postgres
- [x] GitHub Actions: test + lint + web build + helm lint/template + docker build/push ghcr (sha + semver + latest)
- [x] Helm chart basics

## M2 — Ingestion
- [x] Postgres job queue (`FOR UPDATE SKIP LOCKED`, retries/backoff, unique keys, `traceparent` per job, LISTEN wakeups)
- [x] Jobs: process_signal, investigate_incident, agent_chat_reply, execute_action, deliver_notification, heartbeat_ping, reconcile_source, expire_actions, resolve_incidents, check_heartbeats
- [x] Source interface + registry; webhook `POST /api/v1/sources/{name}/webhook` with per-source bearer/basic auth; respond fast, process async
- [x] Alertmanager source: webhook v4 payload parsing; reconciler (GET /api/v2/alerts) for missed/stale alerts
- [x] Signals upsert by (source, fingerprint)
- [x] Heartbeat short-circuit (Watchdog), heartbeat table, outbound ping, "source silent" internal incident, metrics
- [x] Correlation: configurable group_by, attach/create, reopen window (flapping)
- [x] Debounce before investigation
- [x] Resolve incident when all signals resolved + grace period
- [x] Timeline events written in the same transaction as every mutation (+ trace_id)
- [x] LISTEN/NOTIFY fan-out -> SSE `/api/v1/events` (global + per incident)
- [x] Mutes (matchers, starts/ends) suppress notifications, incident marked muted
- [x] API: incidents list/filter/paginate, detail, timeline, signals; sources/heartbeats status

## M3 — Auth
- [x] Local users: argon2id, login, server-side sessions in Postgres, secure cookies, CSRF (double-submit header)
- [x] OIDC (go-oidc + oauth2, code + PKCE), auto-provision, groups claim -> role mapping
- [x] Both modes at once, local login can be disabled
- [x] Roles viewer/operator/admin enforced on every API op
- [x] API tokens (hashed, Bearer)
- [x] CLI: `user create --password-stdin`, `user reset-password`, `token create`
- [x] Profile: Telegram link code generation; ExternalIdentity
- [x] Admin API: users list/update role/disable, tokens list/create/revoke

## M4 — Agent + actions
- [x] Agent interface (Investigate, Chat)
- [x] HolmesGPT client (`/api/chat`, model key, X-API-Key, response_format json_schema, fenced-JSON fallback, timeouts)
- [x] Fake Holmes server for tests
- [x] Investigations job with limits (max concurrent, per hour, cooldown, no re-investigation on repeats), fail-open timeout
- [x] Per-incident conversation state; chat job; agent replies as messages
- [x] Action registry: key, JSON schema, Validate, Execute; agent can only propose registered types
- [x] Executors: k8s.rollout_restart, k8s.delete_pod, k8s.scale (bounded), alertmanager.silence, agent.reinvestigate
- [x] k8s clients: in-cluster + per-cluster kubeconfigs by `cluster` label; namespace allow/deny (kube-system denied)
- [x] Approval flow (operator+), auto-approve per type (default off), proposal TTL/expiry, idempotency, audit in timeline
- [x] Notification routing policy (matchers, verdicts, severities -> channels), fail open with "AI investigation unavailable"
- [x] API: messages GET/POST, actions GET/POST, approve/reject, ack/resolve/reinvestigate, mutes CRUD

## M5 — Channels (single `Channel` interface)
- [x] `Channel` interface: Name, Type, Capabilities, UpsertCard, Post, Edit, Receive, WebhookHandler
- [x] Neutral message model (title, fields, markdown body, buttons) rendered per adapter
- [x] Core dispatcher: ChannelThread/Delivery persistence, card edit-in-place, timeline replies, mirroring without echo, fallback text for no-button channels
- [x] Inbound handling in core: identity resolution, role check, same service methods as API
- [x] Telegram adapter: HTML rendering + safe truncation (4096), inline keyboards (compact callback_data), answerCallbackQuery, edit after decision, typing action, replies -> chat, commands /link /status /ack /mute /resolve /approve /reject
- [x] Telegram rate limiter + retry_after handling
- [x] Telegram polling (singleton lease) + webhook mode (secret_token header)
- [x] docs/channels/slack.md (design only) and verification that the interface fits

## M6 — HA hardening
- [x] Advisory-lock leases (held session, loss detection) for pollers/reconcilers/schedulers
- [x] Multi-replica integration test (two servers on one DB)
- [x] Helm HA values (PDB, anti-affinity)
- [x] docs: architecture.md, sources.md, channels/slack.md, configuration reference, deployment (env/secret keys)

## UI (Vue 3 + TS + Vite, embedded)
- [x] Reka UI (headless) + TanStack Table + TanStack Vue Query, own CSS variables, no Tailwind
- [x] Generated TS client from huma OpenAPI (openapi-typescript + openapi-fetch)
- [x] HyperDX-inspired: dark default + light theme, dense rows, monospace data, left sidebar, search bar with `key:value` syntax, time range picker, severity histogram
- [x] Login (local + OIDC buttons)
- [x] Incident list (live via SSE, filters, keyboard j/k)
- [x] Incident right-side drawer + full page: overview/AI summary, timeline, signals, actions (approve/reject), chat (live)
- [x] Mutes page; sources/channels/heartbeats status page; profile (link code); admin users/tokens
- [x] "View trace" links (tracing.ui_url_template)

## Helm chart
- [x] Deployment, Service, Ingress, ServiceAccount, RBAC (toggleable), ConfigMap from values.config, existingSecret refs, checksum annotations
- [x] External Postgres (host/secret or DSN secret), optional CNPG Cluster
- [x] ServiceMonitor / VMServiceScrape toggles
- [x] Restricted PSS securityContext, resources on all containers, PDB/anti-affinity
- [x] helm lint + template tests in CI

## Testing
- [x] Unit tests per package
- [x] Integration tests against Postgres (docker-compose), fakes: Holmes, Telegram Bot API, Alertmanager
- [x] End-to-end local run (docker-compose + fake Holmes + webhook replay)

## Deployment (manager cluster, with the infra-argocd session)
- [ ] Image pushed to ghcr
- [ ] Hand-off doc: env/secret keys, chart values
- [ ] infra side: DB, secrets, Authentik provider, Argo app, Alertmanager route, Holmes/LiteLLM, heartbeat check
- [ ] Verify in cluster: webhook ingestion, heartbeat, investigation, Telegram card + buttons + replies, UI login (OIDC), actions

## TLS everywhere (added 2026-11-08)
- [x] App serves HTTPS (`server.tls.cert_file/key_file`, hot reload on renewal), probes and /metrics over HTTPS
- [x] `ca_file`: extra CA trusted by every outbound client (Holmes, LiteLLM, Alertmanager, OIDC, pings)
- [x] Postgres `sslmode` default `require`, `sslrootcert` for verify-full
- [x] Chart: cert-manager Certificate (ca-issuer), Service annotations for Traefik ServersTransport, HTTPS scrapes, NetworkPolicy

## Follow-ups (not blocking the MVP)
- [ ] Switch the UI's hand-written API types to the generated OpenAPI schema (`make web-client`)
- [ ] Slack adapter (design in docs/channels/slack.md)
- [ ] More sources (Grafana, Loki, k8s events, generic webhook)
