# infra-monitor

Self-hosted alert and incident hub for Kubernetes: Alertmanager alerts become incidents, an AI agent
(HolmesGPT) investigates them and proposes remediations, a human approves, infra-monitor executes, and the whole
story lives on one timeline that you can follow and act on from the web UI or from Telegram.

- **Ingestion**: Alertmanager webhook (v4) + periodic reconcile, correlation into incidents, flapping protection,
  Watchdog heartbeat with an outbound dead-man's-switch ping.
- **AI investigation**: HolmesGPT over HTTP, structured verdict (actionable / noise), root cause, proposed actions,
  follow-up chat. Fails open: if the AI is slow or down, you are still notified.
- **Actions**: registered executors only (`k8s.rollout_restart`, `k8s.delete_pod`, `k8s.scale`,
  `alertmanager.silence`, `agent.reinvestigate`), JSON-schema validated, namespace policy, human approval, audited.
- **Channels**: one `Channel` interface. Telegram today: an incident card edited in place, approve/reject buttons,
  replies become chat with the agent, commands (`/status`, `/ack`, `/mute 1h`...). Slack is designed
  ([docs/channels/slack.md](docs/channels/slack.md)).
- **UI**: Vue 3, dense dark UI inspired by HyperDX, live over SSE.
- **Auth**: local users (argon2id) and OIDC (PKCE, groups → roles), API tokens, roles viewer / operator / admin.
- **Operations**: one binary, PostgreSQL only, 1 or N replicas, OpenTelemetry traces, Prometheus metrics, HTTPS
  everywhere, Helm chart that passes restricted Pod Security.

## Quick start (local)

```sh
make db-up                                  # Postgres on localhost:55432 (docker compose)
cp config/config.example.yaml config/config.yaml
make web build                              # build the UI, then the binary (UI embedded)
./infra-monitor serve --config config/config.yaml
echo -n 'change-me-please' | ./infra-monitor user create --config config/config.yaml \
  --email you@example.com --role admin --password-stdin
open http://localhost:8080
```

Send a test alert:

```sh
curl -X POST localhost:8080/api/v1/sources/alertmanager/webhook \
  -H 'Authorization: Bearer <sources[0].auth token>' -H 'Content-Type: application/json' \
  -d '{"version":"4","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"Test","severity":"critical","cluster":"dev","namespace":"default"},"annotations":{"summary":"Test alert"},"startsAt":"2026-01-01T00:00:00Z"}]}'
```

`go run ./cmd/fakeholmes` starts a fake HolmesGPT (`agent.url: http://localhost:9099`) to try investigations,
chat and actions without an LLM.

## Development

| Command | What it does |
| --- | --- |
| `make test` | all tests (integration tests need `make db-up`) |
| `make lint` | golangci-lint |
| `make generate` | regenerate ent code after editing `internal/db/ent/schema` |
| `make migration name=add_x` | write a versioned migration (Atlas diff against a scratch DB) |
| `make web` / `make web-dev` | build the UI / run Vite with the API proxied to :8080 (`npm run dev:mock` in `web/` for fixtures) |
| `make openapi web-client` | dump the OpenAPI spec and regenerate the UI client types |
| `make helm-lint` | lint and render the chart with every `ci/*.yaml` values file |

## Docs

- [Architecture](docs/architecture.md): pipeline, packages, HA model, observability
- [HTTP API](docs/api.md): endpoints, auth, SSE (live spec at `/api/v1/docs`)
- [Deployment](docs/deployment.md): Helm chart, secrets, example values, Alertmanager wiring
- [Sources](docs/sources.md): Alertmanager and how to add a source type
- [Telegram](docs/channels/telegram.md) · [Slack design](docs/channels/slack.md)
- [Example configuration](config/config.example.yaml)
- [TODO / status](docs/TODO.md)
