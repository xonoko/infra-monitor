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
- [x] Image pushed to ghcr (private, ghcr-pull-secret), Argo app manager-infra-monitor (ApplicationSet), ns infra-monitor
- [x] Hand-off doc: env/secret keys, chart values (docs/deployment.md)
- [x] infra side: CNPG DB, ExternalSecrets from Vault, Authentik OIDC, Alertmanager route (https), Holmes + LiteLLM (TLS), healthchecks.io ping
- [x] TLS everywhere: app HTTPS (ca-issuer), Traefik ServersTransport, Postgres sslmode=require, Holmes over HTTPS
- [x] Verified: webhook ingestion + heartbeat, SSO login (admin), Telegram card / /link / buttons / replies, Holmes investigation (Haiku, k8s tools)
- [x] Verified: auto-approve executes in plain namespaces, waits for a human in namespaces annotated `none` (Kyverno)
- [x] Alertmanager switch-over: infra-monitor owns all non-camera notifications
- [x] Chat follow-up with Holmes (thinking blocks stripped from replayed history)

## Auto-remediation (added 2026-11-08)
- [x] auto_approve: agent.reinvestigate, k8s.delete_pod, k8s.rollout_restart (global config)
- [x] Per-namespace override with annotations (infra-monitor.xonoko.com/auto-approve, /max-auto-per-hour)
- [x] Loop guards: max_auto_per_hour per incident, no re-investigation chains

## TLS everywhere (added 2026-11-08)
- [x] App serves HTTPS (`server.tls.cert_file/key_file`, hot reload on renewal), probes and /metrics over HTTPS
- [x] `ca_file`: extra CA trusted by every outbound client (Holmes, LiteLLM, Alertmanager, OIDC, pings)
- [x] Postgres `sslmode` default `require`, `sslrootcert` for verify-full
- [x] Chart: cert-manager Certificate (ca-issuer), Service annotations for Traefik ServersTransport, HTTPS scrapes, NetworkPolicy

## Cost controls (added 2026-11-08)
- [x] Severity gate, storm mode (one investigation per storm), hard caps (concurrent/hour/day) incl. chat, cooldown
- [x] Budget/rate-limit errors final + pause, fail open "AI budget reached"
- [x] Haiku by default, Sonnet only on explicit "dig deeper" (UI menu, `/reinvestigate deep`)
- [x] Metrics: skipped by reason, tokens, cost estimate; usage on /status

## Precise approval of any change (added 2026-11-08)
- [x] Generic actions: k8s.patch / k8s.apply / k8s.delete / kubectl.exec, enabled in prod (restricted approved SA, Kyverno guard)
- [x] Prepared proposals: exact target, full payload, server dry-run, effect, rollback, risk, core-infra and Argo flags
- [x] Approval bound to payload hash; target re-checked at approval and execution (desired-state fingerprint); re-proposed if changed
- [x] Never auto-approved; identity guard (no service accounts, secrets, RBAC); redaction of secrets everywhere
- [x] Every proposal posted to Telegram with Approve/Reject (+ attachment when long); compact, highlighted payload
- [x] Final review by Sonnet before every action (auto included): reviews the first analysis + proposal + fresh dry-run, no re-investigation; fail closed (approve again to retry)
- [x] Agent proposals failing the dry-run are never shown; the agent gets one correction attempt
- [x] Models: Haiku first analysis; Sonnet for dig deeper, chat and the final review
- [x] AI token cost per incident (investigation / remediation / chat / total) and 24h on /status, in the UI
- [x] Verified in prod: Argo-managed flag, Telegram approvals (#16, #17), final review cancel (#16), target-changed re-proposal (#15)
- [x] Unified chat + timeline incident view (verified by Lucas)
- [x] gitops.change enabled in prod: GitHub App token via ESO (infra-argocd only), path prefix gitops/, always needs approval
- [x] Live k8s.* proposals on Argo-managed objects are sent back to the agent for a gitops.change
- [x] The agent gets the Argo CD source of the incident namespace (Application, chart values, helm valueFiles with content), verified locally on real data
- [x] Fallback without Argo CD read access: values files named after the namespace in the repo (values-<ns>.yaml, <ns>/values.yaml)
- [x] Prod: `rbac.argocdNamespace: argocd` (infra-argocd b7b8392): infra-monitor reads Applications (get/list only), exact Argo CD source used instead of the file-name fallback
- [x] End-to-end verified in prod (#9, action #29): demo OOM at 64Mi -> agent proposed gitops.change to 512Mi -> approved from Telegram -> Sonnet review ok -> commit aa8c09a -> Argo sync -> demo-api Running
- [x] OOM and crash-loop alert rules fire within ~1-2 min on new pods (infra-argocd 851b1cf)
- [x] CNPGDeadlocks rule fixed (rate-based), incident #1 resolved by itself
- [x] Recurrence: same alert within 24h reopens the incident and reuses the analysis (verified in prod, #17)
- [x] AI caps count only calls that reached the model
- [x] LiteLLM admin UI behind Authentik only (infra session)

## AI grouping and reliability (added 2026-11-09)
- [x] AI grouping: a new alert in a namespace with open incidents is checked by the cheap model (no tools); same problem -> merged into the older incident (one card), else separate; fail open
- [x] Merged alerts that fire again go to the incident they were merged into
- [x] Transient LLM errors (connection, 5xx) retried twice before failing open
- [x] Reopen reuses the analysis only if it succeeded (else investigates again)
- [x] Reopened incidents get the grouping check too; a reopen after a fix ran investigates again (the analysis is stale)
- [x] Verified in prod: demo re-broken (64Mi), ReplicasMismatch (#8) merged into the crash-loop incident (#18) by Haiku ($0.0017), same root cause
- [x] Second GitOps fix verified (#30, commit e077cf7): approved from Telegram, synced by Argo, #9 and #18 resolved at 09:04
- [x] Telegram recurrence line says what happens: analysis reused, investigating, or "the fix didn't hold"
- [x] Daily AI cap raised to 150 (infra-argocd e1b7dd0, Lucas's choice)

## Follow-ups (not blocking the MVP)
- [x] UI API types generated from the OpenAPI schema (`make web-client`), CI fails when stale; API lists never null
- [ ] Slack adapter (design in docs/channels/slack.md)
- [ ] More sources (Grafana, Loki, k8s events, generic webhook)
