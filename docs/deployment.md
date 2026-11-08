# Deployment

infra-monitor is one stateless binary (API + UI + background workers). PostgreSQL is the only state, so
`replicaCount: 1` and `replicaCount: N` both work with no extra infrastructure.

| What | Where |
| --- | --- |
| Image | `ghcr.io/xonoko/infra-monitor` — `latest` + short sha on `master`, `X.Y.Z` / `X.Y` on `vX.Y.Z` tags (linux/amd64, linux/arm64, distroless, uid 65532) |
| Chart | `deploy/helm/infra-monitor` in this repo |
| Port | `8080` (`http`): UI, `/api/v1`, `/healthz` (liveness), `/readyz` (DB reachable), `/metrics` (Prometheus) |
| Config | `values.config` → ConfigMap → `/etc/infra-monitor/config.yaml` (see `config/config.example.yaml` for every key) |
| Secrets | `secrets.existingSecret` mounted at `/etc/infra-monitor/secrets/<key>`, referenced with `*_file` config keys |

## Database

- PostgreSQL 14+ (tested with 17). **No extensions required** (no `CREATE EXTENSION`), so a non-superuser owner role works.
- Migrations run at startup (`database.migrate_on_start`, default true), serialized across replicas by a Postgres
  advisory lock. There is no Helm hook Job (Argo would run it before External Secrets exist).
  `infra-monitor migrate up|status` does the same by hand.
- Modes:
  - external (default): `database.host/port/name/user/sslmode` + password from `secrets.existingSecret` key
    `database.passwordKey` (default `database-password`), or a full DSN with `database.dsnKey`.
  - `cnpg.enabled: true`: renders a CloudNativePG `Cluster` named `<fullname>-db` and reads the password from the
    operator-generated `<fullname>-db-app` secret.

## Secret keys

All keys live in one Secret (`secrets.existingSecret`). Missing keys don't block the pod, but a `*_file` that points to a
missing key makes startup fail with an explicit error. There is **no session secret**: sessions are random tokens stored
(hashed) in Postgres.

| Secret key | Config key that reads it | Needed when |
| --- | --- | --- |
| `database-password` | `database.password_file` (set by the chart) | external DB (default) |
| `database-dsn` | `database.dsn_file` (set by the chart when `database.dsnKey: database-dsn`) | DSN mode |
| `oidc-client-secret` | `auth.oidc.client_secret_file` | OIDC enabled |
| `telegram-bot-token` | `channels[].bot_token_file` | Telegram channel |
| `telegram-webhook-secret` | `channels[].webhook_secret_file` | Telegram in `mode: webhook` |
| `holmes-api-key` | `agent.api_key_file` | Holmes has `HOLMES_API_KEY` set |
| `source-alertmanager-token` | `sources[].auth.bearer_token_file` | always (webhook auth) |
| `heartbeat-ping-url` | `heartbeat.ping_url_file` | external dead man's switch (healthchecks.io style) |

Every config key can also be overridden with `INFRA_MONITOR_<SECTION>_<KEY>` env vars (`extraEnv`).

## Chart values that matter

| Key | Default | Notes |
| --- | --- | --- |
| `replicaCount` | 1 | >1 adds a PDB (`podDisruptionBudget`) and pod anti-affinity (`podAntiAffinity: soft|hard|none`) |
| `image.repository` / `image.tag` | ghcr.io/xonoko/infra-monitor / appVersion | |
| `imagePullSecrets` | [] | e.g. `[{name: ghcr-pull-secret}]` if the package is private |
| `ingress.*` | disabled | `className`, `annotations`, `hosts`, `tls` fully values-driven |
| `secrets.existingSecret` | "" | Secret with the keys above; `secrets.create` + `secrets.values` for tests |
| `database.*` | | `host`, `port`, `name`, `user`, `sslmode`, `maxConns`, `passwordKey`, `dsnKey`, `migrateOnStart` |
| `cnpg.enabled` | false | optional CloudNativePG cluster (`instances`, `storage`, `resources`, `parameters`) |
| `rbac.create` / `rbac.clusterWide` / `rbac.namespaces` | true / true / [] | executor permissions: apps workloads get/list/watch/patch, `*/scale` get/update/patch, pods get/list/watch/delete. `clusterWide: false` creates a Role per namespace in `rbac.namespaces`. `rbac.rules` overrides the rules |
| `serviceAccount.automountToken` | true | needed by the in-cluster executors |
| `metrics.serviceMonitor.enabled` / `metrics.vmServiceScrape.enabled` | false | scrape `/metrics` |
| `resources` | 50m/96Mi → 1/384Mi | requests and limits are always set |
| `extraEnv`, `extraEnvFrom`, `extraVolumes`, `extraVolumeMounts`, `extraArgs` | | e.g. mount kubeconfigs for `config.actions.kubeconfigs` |
| `config` | see values.yaml | the infra-monitor config; `config.database` is generated from `database`/`cnpg` |

The pod passes restricted Pod Security: non-root uid/gid 65532, `fsGroup` 65532, seccomp `RuntimeDefault`, no privilege
escalation, all capabilities dropped, read-only root filesystem (`/tmp` is an emptyDir). A `checksum/config` annotation
rolls the pods when the config changes.

## Example: manager cluster

Namespace `infra-monitor`, release name `infra-monitor` (so the Service is `infra-monitor.infra-monitor.svc:8080`).

```yaml
image:
  repository: ghcr.io/xonoko/infra-monitor
  tag: latest            # pin a sha or version tag in practice
imagePullSecrets: []     # [{name: ghcr-pull-secret}] if the package is private

replicaCount: 1

secrets:
  existingSecret: infra-monitor   # ExternalSecret with the keys from the table above

database:
  host: main-db-rw.databases.svc.cluster.local
  port: 5432
  name: infra_monitor
  user: infra_monitor
  sslmode: require
  passwordKey: database-password

ingress:
  enabled: true
  className: traefik
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    traefik.ingress.kubernetes.io/router.middlewares: traefik-system-security-headers@kubernetescrd
  hosts:
    - host: infra-monitor.manager.xonoko.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: infra-monitor-tls
      hosts: [infra-monitor.manager.xonoko.com]

metrics:
  vmServiceScrape:
    enabled: true      # scrapes /metrics over HTTPS with the internal CA

# TLS everywhere: the app serves HTTPS itself with a cert from the ca-issuer ClusterIssuer.
# Different secret name than the ingress (Let's Encrypt) certificate.
tls:
  enabled: true
  secretName: infra-monitor-internal-tls
  certificate:
    create: true
    issuerRef: { name: ca-issuer, kind: ClusterIssuer, group: cert-manager.io }
  trustCA: true        # outbound calls to Holmes/LiteLLM trust the ca.crt of this secret

service:
  annotations:
    traefik.ingress.kubernetes.io/service.serversscheme: https
    traefik.ingress.kubernetes.io/service.serverstransport: infra-monitor-infra-monitor@kubernetescrd

networkPolicy:
  enabled: true
  allowedNamespaces: [traefik-system, monitoring]   # Traefik, VMAlertmanager, vmagent

config:
  env: production
  log_level: info
  log_format: json
  server:
    listen: ":8080"
    public_url: https://infra-monitor.manager.xonoko.com
    trust_proxy: true
  auth:
    local:
      enabled: true          # keep for the bootstrap admin; can be disabled once OIDC works
    oidc:
      enabled: true
      name: Authentik
      issuer: https://auth.manager.xonoko.com/application/o/infra-monitor/
      client_id: infra-monitor
      client_secret_file: /etc/infra-monitor/secrets/oidc-client-secret
      # redirect_url defaults to https://infra-monitor.manager.xonoko.com/api/v1/auth/oidc/callback
      scopes: [openid, profile, email]
      groups_claim: groups
      admin_groups: [infra-admin]
      operator_groups: []
      default_role: viewer
  sources:
    - name: alertmanager
      type: alertmanager
      auth:
        bearer_token_file: /etc/infra-monitor/secrets/source-alertmanager-token
      alertmanager_url: http://vmalertmanager-vm-stack.monitoring.svc:9093
      reconcile_interval: 5m
      cluster_label: cluster
      namespace_label: namespace
      default_cluster: manager
  heartbeat:
    enabled: true
    matchers: ["alertname=Watchdog"]
    expected_interval: 1m
    silent_after: 10m
    ping_url_file: /etc/infra-monitor/secrets/heartbeat-ping-url
    ping_interval: 1m
  correlation:
    group_by: [alertname, cluster, namespace]
    reopen_window: 30m
    debounce: 45s
    resolve_grace: 2m
  agent:
    enabled: true
    type: holmes
    url: https://holmes-holmes.holmes.svc.cluster.local   # https with a ca-issuer cert; confirm the port
    api_key_file: /etc/infra-monitor/secrets/holmes-api-key
    model: claude-haiku-5-5        # key of Holmes' modelList
    deep_model: claude-sonnet-5-5  # explicit "dig deeper" only
    timeout: 5m
    notify_timeout: 4m
    max_concurrent: 1
    max_per_hour: 10
    max_per_day: 50
    investigate_severities: [critical, warning]
    storm: { enabled: true, threshold: 5, window: 10m }
    budget_pause: 1h
  actions:
    enabled: [k8s.rollout_restart, k8s.delete_pod, k8s.scale, alertmanager.silence, agent.reinvestigate]
    auto_approve: []
    namespace_deny: [kube-system]
    in_cluster: true
    in_cluster_name: manager
    proposal_ttl: 1h
  channels:
    - name: infra
      type: telegram
      bot_token_file: /etc/infra-monitor/secrets/telegram-bot-token
      chat_id: -5054234212   # confirm with getUpdates once the bot is in the group
      mode: polling
  # Empty = built-in default: notify when the verdict is actionable, when it is unknown (AI
  # unavailable: fail open) for critical/warning alerts, or when severity is critical.
  routing: []
  retention:
    resolved_incidents: 90d
```

## Alertmanager (VMAlertmanager) wiring

Send everything except the camera alerts (which keep their direct Telegram route) to infra-monitor, with resolved
notifications. Route `Watchdog` with a short `repeat_interval` so it works as a heartbeat.

```yaml
receivers:
  - name: infra-monitor
    webhook_configs:
      - url: https://infra-monitor.infra-monitor.svc:8080/api/v1/sources/alertmanager/webhook
        send_resolved: true
        max_alerts: 0
        http_config:
          tls_config:
            # CA of the ca-issuer ClusterIssuer (ca.crt of any certificate it issued)
            ca_file: /etc/vm/secrets/internal-ca/ca.crt
            server_name: infra-monitor.infra-monitor.svc
          authorization:
            type: Bearer
            # either inline (from a secret-templated config) ...
            credentials: <source-alertmanager-token>
            # ... or a file mounted into the Alertmanager pod:
            # credentials_file: /etc/vm/secrets/infra-monitor/token

route:
  receiver: infra-monitor
  routes:
    - matchers: ['alertname="Watchdog"']
      receiver: infra-monitor
      group_wait: 0s
      group_interval: 1m
      repeat_interval: 1m
    - matchers: ['channel="cameras"']
      receiver: telegram-cameras   # existing route, unchanged
    - receiver: infra-monitor
      continue: false
```

With the VictoriaMetrics operator, the same goes into a `VMAlertmanagerConfig` (`webhook_configs[].http_config.authorization.credentials`
can reference a Secret key: `{name: infra-monitor-am, key: token}`).

## After the first deploy

```sh
# bootstrap a local admin (until OIDC is used)
kubectl -n infra-monitor exec -i deploy/infra-monitor -- \
  /infra-monitor user create --config /etc/infra-monitor/config.yaml --email lucas@example.com --role admin --password-stdin <<< '...'

# check the webhook path end to end
kubectl -n infra-monitor port-forward svc/infra-monitor 8080:8080
curl -s localhost:8080/readyz
```

Then in the UI profile page, generate a link code and send `/link CODE` to the bot to connect your Telegram account
(unlinked Telegram users can read but cannot approve actions or chat).

## Approved generic actions and GitOps commits

With `approvedActions.enabled: true` the chart creates a second ServiceAccount (`<fullname>-approved`) bound to a
restricted edit role (`approvedActions.rules`: workload writes; no Secrets, no ServiceAccount writes or tokens, no RBAC)
plus read-only access to cluster-scoped objects. Do NOT set `clusterRole: edit`: the built-in role grants Secrets,
ServiceAccount writes, tokens and pods under any ServiceAccount (about cluster-admin). infra-monitor also refuses
payloads that set `serviceAccountName`/`automountServiceAccountToken` or touch Secrets, ServiceAccounts or RBAC.
Action outputs, payloads, dry-runs and agent replies are redacted (Secret data, credentials, tokens) before storage
or display. infra-monitor impersonates it **only**
after a human approved a precise proposal (`k8s.patch`, `k8s.apply`, `k8s.delete`, `kubectl.exec`). Automatic actions
keep the main account. These types are never auto-approved, whatever `actions.auto_approve` says.

Approval is bound to the payload hash. Before approval and at execution the target's resourceVersion (or file SHA)
is re-checked; if it changed, the action is refused and re-proposed.

`gitops.change` commits to a GitHub repository (Contents read/write fine-grained PAT):

| Secret key | Config key |
| --- | --- |
| `github-token` | `actions.gitops.token_file: /etc/infra-monitor/secrets/github-token` |

```yaml
approvedActions:
  enabled: true
  clusterRead: true
  exec: true            # kubectl.exec proposals
config:
  agent:
    model: claude-haiku-5-5        # first automatic diagnostic
    deep_model: claude-sonnet-5-5  # "dig deeper"
    chat_model: claude-sonnet-5-5  # chat replies (default: deep_model)
  actions:
    gitops:
      enabled: true
      repo: lucasgalton/infra-argocd
      branch: main
      token_file: /etc/infra-monitor/secrets/github-token
```
