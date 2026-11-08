// Dev-only mock backend for `npm run dev:mock`: a fetch shim and a fake EventSource
// backed by in-memory fixtures. Loaded through a dynamic import guarded by
// import.meta.env.DEV, so it never reaches production bundles.
import * as fx from './fixtures'
import type { Action, HistogramBucket, Incident, TimelineEvent } from '@/api/types'
import { sevKey } from '@/lib/format'

type Handler = (m: RegExpMatchArray, url: URL, body: any) => unknown
const routes: [string, RegExp, Handler][] = []
const route = (method: string, path: string, h: Handler) => routes.push([method, new RegExp(`^/api/v1${path}$`), h])

class HttpError extends Error {
  constructor(
    public status: number,
    msg: string,
  ) {
    super(msg)
  }
}

// ---- fake EventSource ----
const sources = new Set<FakeEventSource>()
class FakeEventSource extends EventTarget {
  url: string
  readyState = 0
  withCredentials = true
  onopen: ((e: Event) => void) | null = null
  onmessage: ((e: MessageEvent) => void) | null = null
  onerror: ((e: Event) => void) | null = null
  constructor(url: string) {
    super()
    this.url = url
    sources.add(this)
    setTimeout(() => {
      this.readyState = 1
      this.dispatchEvent(new Event('open'))
    }, 50)
  }
  close() {
    this.readyState = 2
    sources.delete(this)
  }
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
}

function emit(event: 'timeline' | 'progress', data: { incident_id: number } & Record<string, unknown>) {
  for (const s of sources) {
    const u = new URL(s.url, location.origin)
    const only = u.searchParams.get('incident_id')
    if (only && Number(only) !== data.incident_id) continue
    s.dispatchEvent(new MessageEvent(event, { data: JSON.stringify(data) }))
  }
}

function addEvent(incident_id: number, type: string, p: Partial<TimelineEvent> = {}) {
  const e: TimelineEvent = {
    id: fx.nextId(),
    incident_id,
    type,
    actor_type: 'system',
    actor_name: 'infra-monitor',
    channel: 'system',
    channel_name: '',
    payload: {},
    trace_id: '',
    created_at: new Date().toISOString(),
    ...p,
  }
  ;(fx.timeline[incident_id] ??= []).push(e)
  emit('timeline', { id: e.id, incident_id, type })
  return e
}

const userActor = () => ({ actor_type: 'user' as const, actor_name: fx.me.display_name, channel: 'ui', actor_id: fx.me.id })
const find = (id: number) => {
  const i = fx.incidents.find((x) => x.id === id)
  if (!i) throw new HttpError(404, `incident ${id} not found`)
  return i
}
type CostLine = { calls: number; prompt_tokens: number; completion_tokens: number; usd: number }
const line = (): CostLine => ({ calls: 0, prompt_tokens: 0, completion_tokens: 0, usd: 0 })
const add = (l: CostLine, p = 0, c = 0, u = 0) => {
  l.calls++
  l.prompt_tokens += p
  l.completion_tokens += c
  l.usd += u
}
/** Same aggregation as the Go API: investigation / remediation (action-triggered) / chat (agent replies). */
function costFor(ids: number[], since = 0) {
  const cost = { investigation: line(), remediation: line(), chat: line(), total: line() }
  for (const id of ids) {
    for (const inv of fx.investigations[id] ?? []) {
      if (!inv.cost_usd && !inv.prompt_tokens) continue
      if (new Date(inv.created_at).getTime() < since) continue
      add(inv.trigger === 'action' ? cost.remediation : cost.investigation, inv.prompt_tokens, inv.completion_tokens, inv.cost_usd)
      add(cost.total, inv.prompt_tokens, inv.completion_tokens, inv.cost_usd)
    }
    for (const msg of fx.messages[id] ?? []) {
      if (msg.role !== 'agent' || !msg.cost_usd) continue
      if (new Date(msg.created_at).getTime() < since) continue
      add(cost.chat, msg.prompt_tokens, msg.completion_tokens, msg.cost_usd)
      add(cost.total, msg.prompt_tokens, msg.completion_tokens, msg.cost_usd)
    }
  }
  return cost
}
const detail = (i: Incident) => ({
  ...i,
  latest_investigation: fx.investigations[i.id]?.[0] ?? null,
  duration_seconds: (Date.now() - new Date(i.opened_at).getTime()) / 1000,
  cost: costFor([i.id]),
})
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))
const syncPending = (id: number) => {
  const i = find(id)
  i.pending_actions = (fx.actions[id] ?? []).filter((a) => a.status === 'proposed').length
}

function filtered(url: URL) {
  const p = url.searchParams
  const list = (k: string) => (p.get(k) ? p.get(k)!.split(',') : null)
  const st = list('status')
  const sev = list('severity')
  const ver = list('verdict')
  const since = p.get('since') ? new Date(p.get('since')!).getTime() : 0
  const until = p.get('until') ? new Date(p.get('until')!).getTime() : Infinity
  const q = (p.get('q') ?? '').toLowerCase()
  return fx.incidents
    .filter((i) => {
      if (st && !st.includes(i.status)) return false
      if (sev && !sev.includes(i.severity)) return false
      if (ver && !ver.includes(i.verdict)) return false
      if (p.get('cluster') && i.cluster !== p.get('cluster')) return false
      if (p.get('namespace') && i.namespace !== p.get('namespace')) return false
      if (p.get('muted') && String(i.muted) !== p.get('muted')) return false
      const opened = new Date(i.opened_at).getTime()
      const last = new Date(i.last_signal_at).getTime()
      if (last < since || opened > until) return false
      if (q && !`${i.title} ${i.summary} ${Object.values(i.labels).join(' ')}`.toLowerCase().includes(q)) return false
      return true
    })
    .sort((a, b) => {
      const ao = a.status === 'open' || a.status === 'acknowledged' ? 0 : 1
      const bo = b.status === 'open' || b.status === 'acknowledged' ? 0 : 1
      return ao - bo || new Date(b.last_signal_at).getTime() - new Date(a.last_signal_at).getTime()
    })
}

// ---- routes ----
route('GET', '/auth/config', () => ({ local_enabled: true, oidc_enabled: true, oidc_name: 'Authentik' }))
route('POST', '/auth/login', (_m, _u, b) => {
  if (!b?.password) throw new HttpError(401, 'invalid email or password')
  return fx.me
})
route('POST', '/auth/logout', () => undefined)
route('GET', '/me', () => fx.me)
route('POST', '/me/link-code', () => ({
  code: Math.random().toString(36).slice(2, 8).toUpperCase(),
  expires_at: new Date(Date.now() + 10 * 60e3).toISOString(),
  instructions: 'Send this to @infra_monitor_bot in the infra group or a private chat.',
}))
route('DELETE', '/me/identities/([^/]+)', (m) => {
  fx.me.identities = fx.me.identities.filter((i) => i.channel_type !== m[1])
})

route('GET', '/incidents', (_m, url) => {
  const all = filtered(url)
  const limit = Number(url.searchParams.get('limit') ?? 50)
  const start = Number(url.searchParams.get('cursor') ?? 0)
  const items = all.slice(start, start + limit).map((i) => ({ ...i, duration_seconds: (Date.now() - new Date(i.opened_at).getTime()) / 1000 }))
  return { items, total: all.length, next_cursor: start + limit < all.length ? String(start + limit) : null }
})
route('GET', '/incidents/histogram', (_m, url) => {
  const n = Number(url.searchParams.get('buckets') ?? 60)
  const since = new Date(url.searchParams.get('since') ?? Date.now() - 86400e3).getTime()
  const until = url.searchParams.get('until') ? new Date(url.searchParams.get('until')!).getTime() : Date.now()
  const w = (until - since) / n
  const buckets: HistogramBucket[] = Array.from({ length: n }, (_, k) => ({
    start: new Date(since + k * w).toISOString(),
    end: new Date(since + (k + 1) * w).toISOString(),
    critical: 0,
    warning: 0,
    info: 0,
    other: 0,
  }))
  const p = new URLSearchParams(url.searchParams)
  p.delete('since')
  p.delete('until')
  for (const i of filtered(new URL(`${url.pathname}?${p}`, location.origin))) {
    const t = new Date(i.opened_at).getTime()
    if (t < since || t >= until) continue
    buckets[Math.floor((t - since) / w)][sevKey(i.severity)]++
  }
  return { buckets }
})
route('GET', '/incidents/(\\d+)', (m) => detail(find(Number(m[1]))))
route('GET', '/incidents/(\\d+)/timeline', (m) => ({ items: fx.timeline[Number(m[1])] ?? [ ] }))
route('GET', '/incidents/(\\d+)/signals', (m) => {
  const i = find(Number(m[1]))
  return {
    items:
      fx.signals[i.id] ??
      [
        {
          id: i.id * 10,
          source: i.source,
          fingerprint: `${i.id.toString(16)}a9c3e1f07b2d`,
          status: i.firing_count ? 'firing' : 'resolved',
          name: i.labels.alertname ?? i.title.split(':')[0],
          severity: i.severity,
          cluster: i.cluster,
          namespace: i.namespace,
          labels: i.labels,
          annotations: { summary: i.summary || i.title },
          starts_at: i.opened_at,
          ends_at: i.resolved_at,
          generator_url: '',
          last_seen_at: i.last_signal_at,
          fire_count: 1,
        },
      ],
  }
})
route('GET', '/incidents/(\\d+)/investigations', (m) => ({ items: fx.investigations[Number(m[1])] ?? [] }))
route('GET', '/incidents/(\\d+)/messages', (m) => ({ items: fx.messages[Number(m[1])] ?? [] }))
route('GET', '/incidents/(\\d+)/actions', (m) => ({ items: fx.actions[Number(m[1])] ?? [] }))

route('POST', '/incidents/(\\d+)/messages', (m, _u, b) => {
  const id = Number(m[1])
  find(id)
  const msg = {
    id: fx.nextId(),
    incident_id: id,
    role: 'user' as const,
    author_name: fx.me.display_name,
    channel: 'ui',
    channel_name: '',
    content: String(b?.content ?? ''),
    status: 'done' as const,
    action_ids: [],
    created_at: new Date().toISOString(),
  }
  ;(fx.messages[id] ??= []).push(msg)
  addEvent(id, 'message', { ...userActor(), payload: { message_id: msg.id, role: 'user', content: msg.content } })
  void (async () => {
    const steps = [
      { kind: 'tool_call', tool: 'kubectl_get_pods', text: `-n ${find(id).namespace || 'default'}` },
      { kind: 'tool_result', tool: 'kubectl_get_pods', text: '3 pods, 3 in CrashLoopBackOff' },
      { kind: 'tool_call', tool: 'kubectl_logs', text: '--previous --tail=50' },
      { kind: 'message', text: 'The logs point at database authentication…' },
    ]
    for (const s of steps) {
      await sleep(700)
      emit('progress', { incident_id: id, ...s })
    }
    await sleep(800)
    const reply = {
      ...msg,
      id: fx.nextId(),
      role: 'agent' as const,
      author_name: 'Agent',
      channel: 'system',
      content: `Looking at it now: the last 50 log lines of the previous container all end with \`password authentication failed\`. That matches the secret rotation at 11:14.\n\n- Restarting the deployment should fix it (proposal **#42**).\n- Longer term, add the reloader annotation.`,
      created_at: new Date().toISOString(),
      model: 'investigate',
      prompt_tokens: 23180,
      completion_tokens: 236,
      cost_usd: 0.0731,
    }
    fx.messages[id].push(reply)
    emit('progress', { incident_id: id, kind: 'done' })
    addEvent(id, 'message', { actor_type: 'agent', actor_name: 'holmes', payload: { message_id: reply.id, role: 'agent', content: reply.content } })
  })()
  return msg
})

function setStatus(id: number, to: Incident['status'], reason?: string) {
  const i = find(id)
  const from = i.status
  i.status = to
  const t = new Date().toISOString()
  if (to === 'acknowledged') i.acknowledged_at = t
  if (to === 'resolved') {
    i.resolved_at = t
    i.firing_count = 0
  }
  if (to === 'open') i.resolved_at = null
  addEvent(id, 'incident_status_changed', { ...userActor(), payload: { from, to, reason } })
  return detail(i)
}
route('POST', '/incidents/(\\d+)/ack', (m) => setStatus(Number(m[1]), 'acknowledged'))
route('POST', '/incidents/(\\d+)/resolve', (m, _u, b) => setStatus(Number(m[1]), 'resolved', b?.reason))
route('POST', '/incidents/(\\d+)/reopen', (m) => setStatus(Number(m[1]), 'open'))
route('POST', '/incidents/(\\d+)/reinvestigate', (m, _u, b) => {
  const id = Number(m[1])
  const i = find(id)
  i.investigation_state = 'running'
  addEvent(id, 'investigation_started', { ...userActor(), payload: { trigger: 'manual', deep: !!b?.deep } })
  void (async () => {
    for (const s of [
      { kind: 'tool_call', tool: 'kubectl_describe', text: 'deployment' },
      { kind: 'tool_call', tool: 'prometheus_query', text: 'rate(container_restarts_total[5m])' },
      { kind: 'tool_result', tool: 'prometheus_query', text: '0.4/s' },
    ]) {
      await sleep(900)
      emit('progress', { incident_id: id, ...s })
    }
    await sleep(900)
    i.investigation_state = 'done'
    i.verdict = 'actionable'
    const inv = { ...(fx.investigations[142][0] ?? {}), id: fx.nextId(), trigger: 'manual' as const, model: b?.deep ? 'deep' : 'investigate', created_at: new Date().toISOString(), started_at: new Date(Date.now() - 3000).toISOString(), finished_at: new Date().toISOString() }
    ;(fx.investigations[id] ??= []).unshift(inv)
    i.summary = inv.summary
    emit('progress', { incident_id: id, kind: 'done' })
    addEvent(id, 'investigation_completed', { actor_type: 'agent', actor_name: 'holmes', payload: { investigation_id: inv.id, verdict: 'actionable', confidence: inv.confidence, summary: inv.summary } })
  })()
  return detail(i)
})
route('POST', '/incidents/(\\d+)/mute', (m, _u, b) => {
  const id = Number(m[1])
  const i = find(id)
  const ms = parseDur(b?.duration ?? '1h')
  const mute = {
    id: fx.nextId(),
    matchers: ['alertname', 'cluster', 'namespace'].filter((k) => i.labels[k]).map((k) => ({ name: k, op: '=' as const, value: i.labels[k] })),
    starts_at: new Date().toISOString(),
    ends_at: new Date(Date.now() + ms).toISOString(),
    reason: b?.reason ?? '',
    created_by_name: fx.me.display_name,
    active: true,
    incident_id: id,
    alertmanager_silence_id: '',
  }
  fx.mutes.unshift(mute)
  i.muted = true
  addEvent(id, 'mute_created', { ...userActor(), payload: { mute_id: mute.id, ends_at: mute.ends_at } })
  return mute
})
route('POST', '/incidents/(\\d+)/actions', (m, _u, b) => {
  const id = Number(m[1])
  const a: Action = {
    id: fx.nextId(),
    incident_id: id,
    type: b.type,
    params: b.params ?? {},
    rationale: b.rationale ?? '',
    risk: fx.actionTypes.find((t) => t.type === b.type)?.risk ?? 'medium',
    proposed_by: 'user',
    status: 'proposed',
    summary: `${b.type} ${Object.values(b.params ?? {}).join(' ')}`,
    decided_by_name: '',
    decided_via: '',
    expires_at: new Date(Date.now() + 3600e3).toISOString(),
    output: '',
    error: '',
    created_at: new Date().toISOString(),
  }
  ;(fx.actions[id] ??= []).unshift(a)
  syncPending(id)
  addEvent(id, 'action_proposed', { ...userActor(), payload: { action_id: a.id, type: a.type, summary: a.summary } })
  return a
})

function findAction(id: number): Action {
  for (const list of Object.values(fx.actions)) {
    const a = list.find((x) => x.id === id)
    if (a) return a
  }
  throw new HttpError(404, 'action not found')
}
route('POST', '/actions/(\\d+)/approve', (m) => {
  const a = findAction(Number(m[1]))
  if (a.status !== 'proposed') throw new HttpError(409, `action is ${a.status}`)
  a.status = 'approved'
  a.decided_by_name = fx.me.display_name
  a.decided_via = 'ui'
  a.decided_at = new Date().toISOString()
  syncPending(a.incident_id)
  addEvent(a.incident_id, 'action_approved', { ...userActor(), payload: { action_id: a.id, type: a.type, summary: a.summary } })
  void (async () => {
    await sleep(600)
    a.status = 'running'
    a.started_at = new Date().toISOString()
    addEvent(a.incident_id, 'action_started', { payload: { action_id: a.id, summary: a.summary } })
    await sleep(1500)
    a.status = 'succeeded'
    a.finished_at = new Date().toISOString()
    a.output = 'deployment.apps/api-gateway restarted\nWaiting for rollout: 3 of 3 updated replicas are available'
    addEvent(a.incident_id, 'action_succeeded', { payload: { action_id: a.id, summary: a.summary, output: a.output } })
  })()
  return a
})
route('POST', '/actions/(\\d+)/reject', (m, _u, b) => {
  const a = findAction(Number(m[1]))
  a.status = 'rejected'
  a.decided_by_name = fx.me.display_name
  a.decided_via = 'ui'
  a.decided_at = new Date().toISOString()
  syncPending(a.incident_id)
  addEvent(a.incident_id, 'action_rejected', { ...userActor(), payload: { action_id: a.id, summary: a.summary, reason: b?.reason } })
  return a
})
route('GET', '/action-types', () => ({ items: fx.actionTypes }))

route('GET', '/mutes', (_m, url) => {
  const now = Date.now()
  for (const m of fx.mutes) m.active = new Date(m.ends_at).getTime() > now
  return { items: url.searchParams.get('active') === 'true' ? fx.mutes.filter((x) => x.active) : fx.mutes }
})
route('POST', '/mutes', (_m, _u, b) => {
  const mute = {
    id: fx.nextId(),
    matchers: b.matchers,
    starts_at: new Date().toISOString(),
    ends_at: new Date(Date.now() + parseDur(b.duration ?? '2h')).toISOString(),
    reason: b.reason ?? '',
    created_by_name: fx.me.display_name,
    active: true,
    incident_id: null,
    alertmanager_silence_id: b.mirror_to_alertmanager ? crypto.randomUUID() : '',
  }
  fx.mutes.unshift(mute)
  return mute
})
route('DELETE', '/mutes/(\\d+)', (m) => {
  const mute = fx.mutes.find((x) => x.id === Number(m[1]))
  if (mute) {
    mute.ends_at = new Date().toISOString()
    mute.active = false
  }
})
route('GET', '/status', () => ({
  ...fx.status,
  sources: fx.status.sources.map((s) => ({ ...s, last_received_at: new Date(Date.now() - 20e3).toISOString() })),
  agent: {
    ...fx.status.agent,
    cost_last_24h: costFor(
      fx.incidents.map((i) => i.id),
      Date.now() - 86400e3,
    ),
  },
}))
route('GET', '/users', () => ({ items: fx.users }))
route('POST', '/users', (_m, _u, b) => {
  if (fx.users.some((u) => u.email === b.email)) throw new HttpError(409, 'a user with this email already exists')
  const u = { id: fx.nextId(), email: b.email, display_name: b.display_name ?? '', role: b.role, disabled: false, auth: b.password ? ['local' as const] : [], last_login_at: null, created_at: new Date().toISOString() }
  fx.users.push(u)
  return u
})
route('PATCH', '/users/(\\d+)', (m, _u, b) => {
  const u = fx.users.find((x) => x.id === Number(m[1]))
  if (!u) throw new HttpError(404, 'user not found')
  Object.assign(u, b)
  return u
})
route('GET', '/tokens', () => ({ items: fx.tokens }))
route('POST', '/tokens', (_m, _u, b) => {
  const token = `imt_${crypto.randomUUID().replace(/-/g, '')}`
  const t = {
    id: fx.nextId(),
    name: b.name,
    prefix: token.slice(0, 8),
    role: b.role,
    user_id: 1,
    last_used_at: null,
    expires_at: b.expires_in ? new Date(Date.now() + parseDur(b.expires_in)).toISOString() : null,
    revoked_at: null,
    created_at: new Date().toISOString(),
  }
  fx.tokens.unshift(t)
  return { ...t, token }
})
route('DELETE', '/tokens/(\\d+)', (m) => {
  const t = fx.tokens.find((x) => x.id === Number(m[1]))
  if (t) t.revoked_at = new Date().toISOString()
})

function parseDur(s: string): number {
  const m = /^(\d+)([smhd])$/.exec(s)
  if (!m) return 3600e3
  return Number(m[1]) * { s: 1e3, m: 60e3, h: 3600e3, d: 86400e3 }[m[2] as 's' | 'm' | 'h' | 'd']
}

export function installMock() {
  const realFetch = window.fetch.bind(window)
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, location.origin)
    if (!url.pathname.startsWith('/api/v1/')) return realFetch(input, init)
    const method = (init?.method ?? 'GET').toUpperCase()
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    await sleep(120 + Math.random() * 180)
    for (const [m, re, h] of routes) {
      if (m !== method) continue
      const match = url.pathname.match(re)
      if (!match) continue
      try {
        const out = h(match, url, body)
        return out === undefined ? new Response(null, { status: 204 }) : Response.json(out)
      } catch (e) {
        const status = e instanceof HttpError ? e.status : 500
        return Response.json({ title: 'Error', status, detail: (e as Error).message }, { status, headers: { 'Content-Type': 'application/problem+json' } })
      }
    }
    return Response.json({ title: 'Not Found', status: 404, detail: `mock: no route for ${method} ${url.pathname}` }, { status: 404 })
  }
  ;(window as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  // ambient activity: a new signal on the running investigation every 20s
  setInterval(() => {
    const i = fx.incidents.find((x) => x.id === 141)
    if (!i) return
    i.last_signal_at = new Date().toISOString()
    addEvent(141, 'signal_firing', { payload: { name: 'KubeDeploymentReplicasMismatch' } })
    emit('progress', { incident_id: 141, kind: 'tool_call', tool: 'kubectl_get_deployment', text: 'payments-worker' })
  }, 20000)
  console.info('[infra-monitor] mock backend installed')
}
