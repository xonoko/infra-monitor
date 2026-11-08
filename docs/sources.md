# Sources

A source is a configured data source instance (`sources[]` in the config). Today the only type is `alertmanager`
(VMAlertmanager / Prometheus Alertmanager).

## Alertmanager

```yaml
sources:
  - name: alertmanager            # used in the webhook path
    type: alertmanager
    auth:
      bearer_token_file: /etc/infra-monitor/secrets/source-alertmanager-token
      # or basic_username + basic_password_file
    alertmanager_url: http://vmalertmanager-vm-stack.monitoring.svc:9093   # reconcile + silences (optional)
    reconcile_interval: 5m
    cluster_label: cluster        # label giving the cluster of each alert
    namespace_label: namespace
    default_cluster: ""           # used when the label is missing
```

- **Webhook**: `POST /api/v1/sources/<name>/webhook` with the v4 payload. Returns `202` after the signals are
  stored; correlation runs asynchronously. Configure `send_resolved: true`.
- **Reconcile** (when `alertmanager_url` is set): every `reconcile_interval`, `GET /api/v2/alerts` re-ingests
  firing alerts a webhook may have missed and resolves signals that are no longer active (not seen for two
  intervals). Heartbeat alerts are ignored by reconcile, so a broken webhook route still shows up as a silent
  heartbeat.
- **Silences**: the `alertmanager.silence` action and mutes with `mirror_to_alertmanager` create silences through
  the same URL.

## Adding a source type

1. **Parse**: write a package `internal/source/<type>` with a function
   `func ParseWebhook(body []byte, sourceName string) ([]domain.Signal, error)`. Each `domain.Signal` needs a
   stable `Fingerprint` (same alert ⇒ same fingerprint; use `alertmanager.Fingerprint(labels)` if the source has
   none), `Status` (`firing`/`resolved`), `Labels` (put the alert name in `alertname` and the severity in
   `severity`), `Annotations` (`summary`/`description` become the incident title), `StartsAt`, optional
   `EndsAt`, `GeneratorURL` and `Raw` (the original JSON, kept for debugging).
2. **Register the parser** in `internal/app/services.go`: `d.Parsers["<type>"] = yourpkg.ParseWebhook`.
3. **Allow the type** in `config.Validate` (`internal/config/config.go`) and document its specific keys.
4. **Optional pull**: if the source can list active alerts, call `incident.Service.ReconcileSource(ctx, name,
   active, staleAfter)` from the scheduler loop (`runScheduler` in `internal/app/services.go`), like Alertmanager.
5. **Test** the parser with real payloads in `testdata/` and add a case to the app integration test.

The rest (correlation, heartbeats, investigation, routing, channels) is source-agnostic: it only sees signals.
