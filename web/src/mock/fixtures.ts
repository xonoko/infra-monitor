// Dev-only fixtures for `npm run dev:mock`. Never imported by production builds.
import type { Action, ActionType, APIToken, Incident, Investigation, Me, Message, Mute, Signal, Status, TimelineEvent, User } from '@/api/types'

const now = Date.now()
const ago = (min: number) => new Date(now - min * 60e3).toISOString()
const later = (min: number) => new Date(now + min * 60e3).toISOString()
let seq = 1000
export const nextId = () => ++seq

export const me: Me = {
  id: 1,
  email: 'lucas@example.com',
  display_name: 'Lucas',
  role: 'admin',
  auth_method: 'oidc',
  csrf_token: 'mock-csrf',
  identities: [{ channel_type: 'telegram', external_name: '@lucas' }],
  trace_url_template: 'https://hyperdx.example.com/trace/{trace_id}',
}

function inc(p: Partial<Incident> & Pick<Incident, 'id' | 'title'>): Incident {
  return {
    status: 'open',
    severity: 'warning',
    cluster: 'manager',
    namespace: '',
    source: 'alertmanager',
    verdict: 'unknown',
    summary: '',
    muted: false,
    internal: false,
    labels: {},
    opened_at: ago(30),
    acknowledged_at: null,
    resolved_at: null,
    last_signal_at: ago(2),
    duration_seconds: 1800,
    signal_count: 1,
    firing_count: 1,
    pending_actions: 0,
    reopen_count: 0,
    investigation_state: 'done',
    notify_channels: [],
    assignee: null,
    trace_id: 'a3f1c2d4e5b60718293a4b5c6d7e8f90',
    url: '',
    ...p,
  }
}

export const incidents: Incident[] = [
  inc({
    id: 142,
    title: 'KubePodCrashLooping: api-gateway restarting',
    severity: 'critical',
    cluster: 'prod',
    namespace: 'shop',
    verdict: 'actionable',
    summary: 'api-gateway crashes on start: DB_URL secret rotated, pods still use the old connection string.',
    opened_at: ago(23),
    last_signal_at: ago(1),
    signal_count: 3,
    firing_count: 3,
    pending_actions: 1,
    notify_channels: ['telegram-infra'],
    labels: { alertname: 'KubePodCrashLooping', severity: 'critical', cluster: 'prod', namespace: 'shop', deployment: 'api-gateway', team: 'platform' },
  }),
  inc({
    id: 141,
    title: 'KubeDeploymentReplicasMismatch: payments',
    severity: 'warning',
    cluster: 'prod',
    namespace: 'payments',
    investigation_state: 'running',
    opened_at: ago(4),
    last_signal_at: ago(1),
    labels: { alertname: 'KubeDeploymentReplicasMismatch', severity: 'warning', cluster: 'prod', namespace: 'payments', deployment: 'payments-worker' },
  }),
  inc({
    id: 140,
    title: 'source alertmanager silent for 12m',
    severity: 'critical',
    cluster: 'manager',
    internal: true,
    source: 'infra-monitor',
    verdict: 'actionable',
    summary: 'No Watchdog heartbeat from alertmanager since 11:02. vmalert or Alertmanager may be down.',
    opened_at: ago(12),
    last_signal_at: ago(12),
    investigation_state: 'skipped',
    notify_channels: ['telegram-infra'],
    labels: { alertname: 'HeartbeatSilent', source: 'alertmanager', severity: 'critical' },
  }),
  inc({
    id: 139,
    title: 'PostgresHighConnections: main-db',
    status: 'acknowledged',
    severity: 'warning',
    cluster: 'manager',
    namespace: 'databases',
    verdict: 'actionable',
    summary: 'Connection count at 182/200, driven by budget-api pool after the last deploy.',
    opened_at: ago(95),
    acknowledged_at: ago(80),
    last_signal_at: ago(6),
    signal_count: 2,
    firing_count: 1,
    assignee: { id: 1, name: 'Lucas' },
    notify_channels: ['telegram-infra'],
    labels: { alertname: 'PostgresHighConnections', severity: 'warning', cluster: 'manager', namespace: 'databases', cnpg_cluster: 'main' },
  }),
  inc({
    id: 138,
    title: 'CPUThrottlingHigh: frigate',
    severity: 'info',
    cluster: 'manager',
    namespace: 'frigate',
    verdict: 'noise',
    muted: true,
    summary: 'Expected throttling during nightly re-encode; limits are intentionally low.',
    opened_at: ago(300),
    last_signal_at: ago(40),
    signal_count: 4,
    firing_count: 2,
    labels: { alertname: 'CPUThrottlingHigh', severity: 'info', cluster: 'manager', namespace: 'frigate', container: 'frigate' },
  }),
  inc({
    id: 137,
    title: 'NodeFilesystemAlmostFullFiveMinutes: worker-2 /var',
    status: 'resolved',
    severity: 'critical',
    cluster: 'manager',
    namespace: '',
    verdict: 'actionable',
    summary: 'Container image cache filled /var; kubelet GC freed 14 GiB.',
    opened_at: ago(420),
    resolved_at: ago(380),
    last_signal_at: ago(381),
    firing_count: 0,
    notify_channels: ['telegram-infra'],
    labels: { alertname: 'NodeFilesystemAlmostFull', severity: 'critical', cluster: 'manager', instance: 'worker-2', mountpoint: '/var' },
  }),
  inc({
    id: 136,
    title: 'TargetDown: blackbox-exporter',
    status: 'resolved',
    severity: 'warning',
    cluster: 'manager',
    namespace: 'monitoring',
    verdict: 'noise',
    summary: 'Target flapped during a rolling restart of the exporter.',
    opened_at: ago(600),
    resolved_at: ago(590),
    last_signal_at: ago(591),
    firing_count: 0,
    labels: { alertname: 'TargetDown', severity: 'warning', cluster: 'manager', namespace: 'monitoring', job: 'blackbox' },
  }),
  inc({
    id: 135,
    title: 'CertManagerCertExpirySoon: wildcard-manager',
    status: 'closed',
    severity: 'warning',
    cluster: 'manager',
    namespace: 'traefik-system',
    verdict: 'actionable',
    opened_at: ago(1440 * 2),
    resolved_at: ago(1440 * 2 - 30),
    last_signal_at: ago(1440 * 2 - 31),
    firing_count: 0,
    labels: { alertname: 'CertManagerCertExpirySoon', severity: 'warning', cluster: 'manager', namespace: 'traefik-system' },
  }),
]

// generated history for the histogram
const names = ['KubeJobFailed', 'KubeHpaMaxedOut', 'TargetDown', 'PodNotReady', 'HighLatencyP99', 'DiskPressure', 'OOMKilled']
const sevs = ['critical', 'warning', 'warning', 'info', 'warning', 'critical', 'info']
const nss = ['shop', 'monitoring', 'payments', 'databases', 'frigate', 'argocd']
for (let i = 0; i < 46; i++) {
  const opened = 30 + ((i * 97) % (1440 - 60))
  const n = names[i % names.length]
  incidents.push(
    inc({
      id: 134 - i,
      title: `${n}: ${nss[i % nss.length]}`,
      status: 'resolved',
      severity: sevs[(i * 3) % sevs.length],
      cluster: i % 3 ? 'manager' : 'prod',
      namespace: nss[i % nss.length],
      verdict: i % 4 ? 'noise' : 'actionable',
      opened_at: ago(opened),
      resolved_at: ago(opened - 8 - (i % 30)),
      last_signal_at: ago(opened - 8 - (i % 30)),
      firing_count: 0,
      labels: { alertname: n, severity: sevs[(i * 3) % sevs.length], namespace: nss[i % nss.length] },
    }),
  )
}

export const signals: Record<number, Signal[]> = {
  142: ['api-gateway-7d9f8b6c5-x2kqp', 'api-gateway-7d9f8b6c5-m8wzt', 'api-gateway-7d9f8b6c5-h4jrn'].map((pod, i) => ({
    id: 900 + i,
    source: 'alertmanager',
    fingerprint: `4f3a9c1e8b2d7a6${i}`,
    status: 'firing',
    name: 'KubePodCrashLooping',
    severity: 'critical',
    cluster: 'prod',
    namespace: 'shop',
    labels: { alertname: 'KubePodCrashLooping', severity: 'critical', cluster: 'prod', namespace: 'shop', pod, container: 'gateway', job: 'kube-state-metrics' },
    annotations: {
      summary: `Pod shop/${pod} (gateway) is in waiting state (reason: "CrashLoopBackOff").`,
      runbook_url: 'https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubepodcrashlooping',
    },
    starts_at: ago(23 - i),
    ends_at: null,
    generator_url: 'https://vmalert.manager.example.com/vmalert/alert?group_id=1&alert_id=2',
    last_seen_at: ago(1),
    fire_count: 6 - i,
  })),
}

export const investigations: Record<number, Investigation[]> = {
  142: [
    {
      id: 77,
      status: 'succeeded',
      trigger: 'auto',
      agent: 'holmes',
      model: 'investigate',
      summary: 'api-gateway crashes on start: DB_URL secret rotated, pods still use the old connection string.',
      root_cause:
        'The `shop-db` secret was rotated by External Secrets at **11:14**. `api-gateway` reads `DB_URL` from env at startup, so new pods fail authentication (`FATAL: password authentication failed for user "shop"`) and existing pods were evicted by the 11:16 node drain.',
      analysis:
        '## Findings\n\n1. `kubectl logs shop/api-gateway-7d9f8b6c5-x2kqp --previous` ends with:\n\n```\npanic: pq: password authentication failed for user "shop"\n```\n2. `ExternalSecret shop/shop-db` last synced at 11:14:02 (refreshInterval 1h).\n3. The deployment has no reloader annotation, so the rotation did not trigger a rollout.\n\n## Recommendation\n\n- Restart `deployment/api-gateway` so pods pick up the new secret.\n- Add `reloader.stakater.com/auto: "true"` to the deployment.',
      verdict: 'actionable',
      confidence: 0.86,
      tool_calls: [{ name: 'kubectl_logs' }, { name: 'kubectl_describe' }, { name: 'kubectl_get_events' }],
      error: '',
      prompt_tokens: 18234,
      completion_tokens: 1290,
      created_at: ago(22),
      started_at: ago(22),
      finished_at: ago(20),
      trace_id: '7be1c0aa9f6e4cd1a2b3c4d5e6f70812',
    },
  ],
  139: [
    {
      id: 74,
      status: 'succeeded',
      trigger: 'auto',
      agent: 'holmes',
      model: 'investigate',
      summary: 'Connection count at 182/200, driven by budget-api pool after the last deploy.',
      root_cause: '`budget-api` v2.4 raised `max_conns` from 10 to 40 per replica (4 replicas).',
      analysis: 'Connections by application: budget-api 160, grafana 12, others 10.',
      verdict: 'actionable',
      confidence: 0.71,
      tool_calls: [],
      error: '',
      prompt_tokens: 9100,
      completion_tokens: 640,
      created_at: ago(94),
      started_at: ago(94),
      finished_at: ago(92),
      trace_id: '',
    },
  ],
  140: [
    {
      id: 75,
      status: 'failed',
      trigger: 'auto',
      agent: 'holmes',
      model: 'investigate',
      summary: '',
      root_cause: '',
      analysis: '',
      verdict: 'unknown',
      confidence: 0,
      tool_calls: [],
      error: 'context deadline exceeded after 4m0s',
      prompt_tokens: 0,
      completion_tokens: 0,
      created_at: ago(12),
      started_at: ago(12),
      finished_at: ago(8),
      trace_id: '',
    },
  ],
}

export const actions: Record<number, Action[]> = {
  142: [
    {
      id: 42,
      incident_id: 142,
      type: 'k8s.rollout_restart',
      params: { kind: 'deployment', name: 'api-gateway', namespace: 'shop' },
      rationale: 'Pods read `DB_URL` only at startup; a rollout restart picks up the rotated secret.',
      risk: 'low',
      proposed_by: 'agent',
      status: 'proposed',
      summary: 'rollout restart deployment/api-gateway -n shop',
      decided_by_name: '',
      decided_via: '',
      decided_at: null,
      expires_at: later(38),
      started_at: null,
      finished_at: null,
      output: '',
      error: '',
      created_at: ago(20),
    },
    {
      id: 41,
      incident_id: 142,
      type: 'k8s.delete_pod',
      params: { name: 'api-gateway-7d9f8b6c5-x2kqp', namespace: 'shop' },
      rationale: 'Force a fresh pod.',
      risk: 'medium',
      proposed_by: 'agent',
      status: 'rejected',
      summary: 'delete pod api-gateway-7d9f8b6c5-x2kqp -n shop',
      decided_by_name: 'Lucas',
      decided_via: 'telegram',
      decided_at: ago(18),
      expires_at: ago(-40),
      started_at: null,
      finished_at: null,
      output: '',
      error: '',
      created_at: ago(20),
    },
  ],
}

export const messages: Record<number, Message[]> = {
  142: [
    {
      id: 501,
      incident_id: 142,
      role: 'user',
      author_name: 'Lucas',
      channel: 'telegram',
      channel_name: 'telegram-infra',
      content: 'Did anything change on the secret side?',
      status: 'done',
      action_ids: [],
      created_at: ago(19),
    },
    {
      id: 502,
      incident_id: 142,
      role: 'agent',
      author_name: 'Agent',
      channel: 'system',
      channel_name: '',
      content:
        'Yes — `ExternalSecret shop/shop-db` synced a **new password at 11:14:02**, two minutes before the first crash. The deployment has no reloader annotation, so running pods kept the old value until they were rescheduled.\n\nI proposed a rollout restart (#42).',
      status: 'done',
      action_ids: [42],
      created_at: ago(18),
    },
  ],
}

function ev(incident_id: number, min: number, type: string, p: Partial<TimelineEvent> = {}): TimelineEvent {
  return {
    id: nextId(),
    incident_id,
    type,
    actor_type: 'system',
    actor_name: 'infra-monitor',
    channel: 'system',
    channel_name: '',
    payload: {},
    trace_id: '',
    created_at: ago(min),
    ...p,
  }
}

export const timeline: Record<number, TimelineEvent[]> = {
  142: [
    ev(142, 23, 'incident_opened', { trace_id: '7be1c0aa9f6e4cd1a2b3c4d5e6f70812' }),
    ev(142, 23, 'signal_firing', { payload: { name: 'KubePodCrashLooping', fingerprint: '4f3a9c1e8b2d7a60' } }),
    ev(142, 22, 'signal_firing', { payload: { name: 'KubePodCrashLooping', fingerprint: '4f3a9c1e8b2d7a61' } }),
    ev(142, 22, 'investigation_started', { actor_type: 'agent', actor_name: 'holmes', payload: { trigger: 'auto' } }),
    ev(142, 21, 'signal_firing', { payload: { name: 'KubePodCrashLooping', fingerprint: '4f3a9c1e8b2d7a62' } }),
    ev(142, 20, 'investigation_completed', {
      actor_type: 'agent',
      actor_name: 'holmes',
      payload: { verdict: 'actionable', confidence: 0.86, summary: 'api-gateway crashes on start: DB_URL secret rotated.' },
      trace_id: '7be1c0aa9f6e4cd1a2b3c4d5e6f70812',
    }),
    ev(142, 20, 'action_proposed', { actor_type: 'agent', actor_name: 'holmes', payload: { action_id: 42, type: 'k8s.rollout_restart', summary: 'rollout restart deployment/api-gateway -n shop' } }),
    ev(142, 20, 'action_proposed', { actor_type: 'agent', actor_name: 'holmes', payload: { action_id: 41, type: 'k8s.delete_pod', summary: 'delete pod api-gateway-7d9f8b6c5-x2kqp -n shop' } }),
    ev(142, 20, 'notification_routed', { payload: { channels: ['telegram-infra'], reason: 'verdict actionable' } }),
    ev(142, 20, 'notification_sent', { payload: { channel: 'telegram-infra' } }),
    ev(142, 19, 'message', { actor_type: 'user', actor_name: 'Lucas', channel: 'telegram', channel_name: 'telegram-infra', payload: { message_id: 501, role: 'user', content: 'Did anything change on the secret side?' } }),
    ev(142, 18, 'action_rejected', { actor_type: 'user', actor_name: 'Lucas', channel: 'telegram', payload: { action_id: 41, summary: 'delete pod api-gateway-7d9f8b6c5-x2kqp -n shop', reason: 'restart instead' } }),
    ev(142, 18, 'message', { actor_type: 'agent', actor_name: 'holmes', payload: { message_id: 502, role: 'agent', content: messages[142][1].content } }),
  ],
  139: [
    ev(139, 95, 'incident_opened'),
    ev(139, 94, 'investigation_completed', { actor_type: 'agent', actor_name: 'holmes', payload: { verdict: 'actionable', confidence: 0.71 } }),
    ev(139, 80, 'incident_status_changed', { actor_type: 'user', actor_name: 'Lucas', channel: 'ui', payload: { from: 'open', to: 'acknowledged' } }),
  ],
  140: [
    ev(140, 12, 'incident_opened'),
    ev(140, 12, 'investigation_started', { actor_type: 'agent', actor_name: 'holmes' }),
    ev(140, 8, 'investigation_failed', { actor_type: 'agent', actor_name: 'holmes', payload: { error: 'context deadline exceeded after 4m0s' } }),
    ev(140, 8, 'notification_routed', { payload: { channels: ['telegram-infra'], reason: 'fail open: AI investigation unavailable' } }),
  ],
}

export const actionTypes: ActionType[] = [
  {
    type: 'k8s.rollout_restart',
    description: 'Restart a deployment, statefulset or daemonset (rolling).',
    risk: 'low',
    auto_approve: false,
    params_schema: {
      type: 'object',
      required: ['kind', 'name'],
      properties: {
        kind: { type: 'string', enum: ['deployment', 'statefulset', 'daemonset'], default: 'deployment' },
        name: { type: 'string', description: 'Workload name' },
        namespace: { type: 'string', description: 'Defaults to the incident namespace' },
      },
    },
  },
  {
    type: 'k8s.scale',
    description: 'Scale a deployment or statefulset (bounded).',
    risk: 'medium',
    auto_approve: false,
    params_schema: {
      type: 'object',
      required: ['kind', 'name', 'replicas'],
      properties: {
        kind: { type: 'string', enum: ['deployment', 'statefulset'], default: 'deployment' },
        name: { type: 'string' },
        namespace: { type: 'string' },
        replicas: { type: 'integer', minimum: 0, maximum: 10 },
      },
    },
  },
  {
    type: 'k8s.delete_pod',
    description: 'Delete one pod so its controller recreates it.',
    risk: 'medium',
    auto_approve: false,
    params_schema: { type: 'object', required: ['name'], properties: { name: { type: 'string' }, namespace: { type: 'string' } } },
  },
  {
    type: 'alertmanager.silence',
    description: 'Create an Alertmanager silence.',
    risk: 'low',
    auto_approve: false,
    params_schema: {
      type: 'object',
      properties: {
        matchers: { type: 'array', items: { type: 'object' } },
        duration: { type: 'string', default: '2h' },
        comment: { type: 'string' },
      },
    },
  },
]

export const mutes: Mute[] = [
  {
    id: 12,
    matchers: [
      { name: 'alertname', op: '=', value: 'CPUThrottlingHigh' },
      { name: 'namespace', op: '=', value: 'frigate' },
    ],
    starts_at: ago(60),
    ends_at: later(180),
    reason: 'Nightly re-encode, limits are intentional',
    created_by_name: 'Lucas',
    active: true,
    incident_id: 138,
    alertmanager_silence_id: '',
  },
  {
    id: 11,
    matchers: [{ name: 'namespace', op: '=~', value: 'argocd|argo-.*' }],
    starts_at: ago(2000),
    ends_at: ago(1900),
    reason: 'Argo upgrade',
    created_by_name: 'Lucas',
    active: false,
    incident_id: null,
    alertmanager_silence_id: '0f9a2b7c-5d1e-4a3b-9c8d-7e6f5a4b3c2d',
  },
]

export const users: User[] = [
  { id: 1, email: 'lucas@example.com', display_name: 'Lucas', role: 'admin', disabled: false, auth: ['oidc'], last_login_at: ago(30), created_at: ago(43200) },
  { id: 2, email: 'oncall@example.com', display_name: 'On-call bot', role: 'operator', disabled: false, auth: ['local'], last_login_at: ago(2000), created_at: ago(20000) },
  { id: 3, email: 'intern@example.com', display_name: 'Sam', role: 'viewer', disabled: true, auth: ['local', 'oidc'], last_login_at: null, created_at: ago(9000) },
]

export const tokens: APIToken[] = [
  { id: 1, name: 'ci-silencer', prefix: 'imt_3fa9', role: 'operator', user_id: 1, last_used_at: ago(300), expires_at: later(60 * 24 * 60), revoked_at: null, created_at: ago(20000) },
  { id: 2, name: 'old-script', prefix: 'imt_77c1', role: 'viewer', user_id: 1, last_used_at: ago(30000), expires_at: null, revoked_at: ago(10000), created_at: ago(40000) },
]

export const status: Status = {
  version: 'v0.1.0-mock',
  replica: 'infra-monitor-6f9c7d-abcde',
  sources: [
    { name: 'alertmanager', type: 'alertmanager', last_received_at: ago(0.5), last_reconciled_at: ago(3), last_error: '', received_total: 18233 },
  ],
  channels: [
    {
      name: 'telegram-infra',
      type: 'telegram',
      mode: 'polling',
      connected: true,
      leader: true,
      last_error: '',
      capabilities: { Buttons: true, URLButtons: true, EditMessages: true, Threads: true, Typing: true, SingletonReceiver: true },
    },
  ],
  heartbeats: [
    { key: 'alertmanager', state: 'ok', last_seen_at: ago(0.7), expected_interval_seconds: 60, last_ping_at: ago(0.7), last_ping_error: '' },
  ],
  agent: { enabled: true, type: 'holmes', model: 'investigate', url_host: 'holmes-holmes.holmes.svc.cluster.local', max_concurrent: 2, max_per_hour: 20 },
  leases: [{ name: 'channel:telegram:telegram-infra:poller', held: true }, { name: 'scheduler', held: true }],
}

// ---- scenarios for the unified incident stream ----

// #142: link the completed investigation, plus a flapping run of repeated notifications to collapse.
for (const e of timeline[142]) if (e.type === 'investigation_completed') e.payload.investigation_id = 77
const firstMsg = timeline[142].findIndex((e) => e.type === 'message')
timeline[142].splice(
  firstMsg,
  0,
  ...[17.6, 17.4, 17.2, 17, 16.8, 16.6, 16.4].map((min) => ev(142, min, 'signal_firing', { payload: { name: 'KubePodCrashLooping', fingerprint: '4f3a9c1e8b2d7a60' } })),
)
timeline[142].forEach((e, i) => (e.id = 5000 + i))

// #140: agent refused (hourly cap), shown as a failed system message.
Object.assign(incidents.find((i) => i.id === 140)!, { investigation_note: 'AI investigation timed out after 4m; notified with the raw alert' })
messages[140] = [
  {
    id: 601,
    incident_id: 140,
    role: 'system',
    author_name: '',
    channel: 'system',
    channel_name: '',
    content: 'The agent is not available: hourly AI cap reached (20/20). Try again in 22 min.',
    status: 'failed',
    action_ids: [],
    created_at: ago(5),
  },
]

// #138: skipped because the daily budget was reached, plus flapping signals.
Object.assign(incidents.find((i) => i.id === 138)!, { investigation_state: 'skipped', investigation_note: 'AI budget reached' })
timeline[138] = [
  ev(138, 300, 'incident_opened'),
  ...[300, 240, 180, 120].map((min) => ev(138, min, 'signal_firing', { payload: { name: 'CPUThrottlingHigh' } })),
  ev(138, 299, 'investigation_skipped', { payload: { reason: 'AI budget reached' } }),
  ev(138, 60, 'mute_created', { actor_type: 'user', actor_name: 'Lucas', channel: 'telegram', channel_name: 'telegram-infra', payload: { ends_at: later(180), reason: 'Nightly re-encode' } }),
  ...[50, 45, 40].map((min) => ev(138, min, 'signal_firing', { payload: { name: 'CPUThrottlingHigh' } })),
].sort((a, b) => a.created_at.localeCompare(b.created_at))
timeline[138].forEach((e, i) => (e.id = 6000 + i))

// Alert storm #150 grouping #147-#149: investigated once at the storm level.
const stormMembers = [
  { id: 147, title: 'KubePodNotReady: checkout-7f9c', severity: 'critical' },
  { id: 148, title: 'KubePodNotReady: cart-5d2a', severity: 'critical' },
  { id: 149, title: 'HighErrorRate: storefront', severity: 'warning' },
]
incidents.unshift(
  inc({
    id: 150,
    title: 'Alert storm: 3 incidents in prod/shop within 2m',
    severity: 'critical',
    cluster: 'prod',
    namespace: 'shop',
    is_storm: true,
    verdict: 'actionable',
    summary: 'Node pool prod-b lost 2 nodes; shop pods are rescheduling.',
    opened_at: ago(9),
    last_signal_at: ago(3),
    signal_count: 3,
    firing_count: 3,
    notify_channels: ['telegram-infra'],
    labels: { alertname: 'AlertStorm', cluster: 'prod', namespace: 'shop', severity: 'critical' },
  }),
  ...stormMembers.map((mm) =>
    inc({
      id: mm.id,
      title: mm.title,
      severity: mm.severity,
      cluster: 'prod',
      namespace: 'shop',
      storm_id: 150,
      investigation_state: 'skipped',
      investigation_note: 'part of alert storm #150',
      opened_at: ago(9),
      last_signal_at: ago(4),
      labels: { alertname: mm.title.split(':')[0], cluster: 'prod', namespace: 'shop', severity: mm.severity },
    }),
  ),
)
investigations[150] = [
  {
    ...investigations[142][0],
    id: 80,
    summary: 'Node pool prod-b lost 2 nodes; shop pods are rescheduling.',
    root_cause: 'Nodes `prod-b-3` and `prod-b-4` went `NotReady` at 12:16 (cloud provider maintenance). Pods of `checkout` and `cart` are pending on the remaining nodes.',
    analysis: '## Findings\n\n- 2/4 nodes in pool `prod-b` NotReady since 12:16.\n- 14 pods Pending with `Insufficient cpu`.\n\n## Recommendation\n\nScale the node pool or wait for the maintenance to finish (~15 min).',
    confidence: 0.78,
    created_at: ago(8),
    started_at: ago(8),
    finished_at: ago(7),
  },
]
timeline[150] = [
  ev(150, 9, 'incident_opened'),
  ...stormMembers.map((mm, i) => ev(150, 9 - i * 0.2, 'storm_member', { payload: { incident_id: mm.id, title: mm.title, severity: mm.severity } })),
  ev(150, 8, 'investigation_started', { actor_type: 'agent', actor_name: 'holmes', payload: { trigger: 'auto' } }),
  ev(150, 7, 'investigation_completed', { actor_type: 'agent', actor_name: 'holmes', payload: { investigation_id: 80, verdict: 'actionable', confidence: 0.78 } }),
  ev(150, 7, 'notification_routed', { payload: { channels: ['telegram-infra'], reason: 'storm' } }),
]
for (const mm of stormMembers) {
  timeline[mm.id] = [
    ev(mm.id, 9, 'incident_opened'),
    ev(mm.id, 9, 'signal_firing', { payload: { name: mm.title.split(':')[0] } }),
    ev(mm.id, 9, 'investigation_skipped', { payload: { reason: 'part of alert storm #150' } }),
  ]
}

// ---- AI cost ----
investigations[142][0].cost_usd = 0.0741
investigations[139][0].cost_usd = 0.0369
investigations[150][0].cost_usd = 0.0741
// #137: verification after the fix (remediation)
investigations[137] = [
  {
    ...investigations[139][0],
    id: 79,
    trigger: 'action',
    model: 'triage',
    summary: 'Disk usage back to 61% after image GC; nothing left to do.',
    root_cause: '',
    analysis: '',
    verdict: 'noise',
    confidence: 0.9,
    prompt_tokens: 4200,
    completion_tokens: 210,
    cost_usd: 0.0052,
    created_at: ago(379),
    started_at: ago(379),
    finished_at: ago(378),
  },
  { ...investigations[142][0], id: 78, model: 'investigate', cost_usd: 0.0612, prompt_tokens: 15020, completion_tokens: 1100, created_at: ago(419), started_at: ago(419), finished_at: ago(417) },
]
Object.assign(messages[142][1], { model: 'investigate', prompt_tokens: 21410, completion_tokens: 412, cost_usd: 0.0704 })
