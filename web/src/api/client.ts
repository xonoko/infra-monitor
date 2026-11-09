import type {
  Action,
  ActionType,
  APIToken,
  AuthConfig,
  CreatedAPIToken,
  Histogram,
  Incident,
  IncidentDetail,
  IncidentFilters,
  IncidentPage,
  Investigation,
  LinkCode,
  List,
  Matcher,
  Me,
  Message,
  Mute,
  Role,
  Signal,
  Status,
  TimelineEvent,
  User,
} from './types'

export const API_BASE = '/api/v1'

export class ApiError extends Error {
  status: number
  detail: string
  errors: { message?: string; location?: string }[]
  constructor(status: number, title: string, detail: string, errors: { message?: string; location?: string }[] = []) {
    const extra = errors
      .map((e) => [e.location, e.message].filter(Boolean).join(': '))
      .filter(Boolean)
      .join('; ')
    super(detail || extra || title || `HTTP ${status}`)
    this.status = status
    this.detail = [detail, extra].filter(Boolean).join(' — ')
    this.errors = errors
  }
}

let csrfToken = ''
export function setCsrfToken(t: string) {
  csrfToken = t
}

type Query = Record<string, string | number | boolean | undefined | null | string[]>

function qs(query?: Query): string {
  if (!query) return ''
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === null || v === '') continue
    if (Array.isArray(v)) {
      if (v.length) p.set(k, v.join(','))
    } else p.set(k, String(v))
  }
  const s = p.toString()
  return s ? `?${s}` : ''
}

let redirecting = false
function toLogin() {
  if (redirecting) return
  const here = window.location.pathname + window.location.search
  if (window.location.pathname.startsWith('/login')) return
  redirecting = true
  window.location.assign(`/login?redirect=${encodeURIComponent(here)}`)
}

interface RequestOpts {
  query?: Query
  body?: unknown
  /** do not redirect to /login on 401 */
  noRedirect?: boolean
}

export async function request<T>(method: string, path: string, opts: RequestOpts = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const unsafe = method !== 'GET' && method !== 'HEAD'
  if (unsafe && csrfToken) headers['X-CSRF-Token'] = csrfToken
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch(API_BASE + path + qs(opts.query), {
    method,
    headers,
    credentials: 'same-origin',
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let data: unknown = undefined
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = text
    }
  }
  if (!res.ok) {
    if (res.status === 401 && !opts.noRedirect) toLogin()
    const p = (typeof data === 'object' && data ? data : {}) as {
      title?: string
      detail?: string
      errors?: { message?: string; location?: string }[]
    }
    throw new ApiError(res.status, p.title ?? res.statusText, p.detail ?? (typeof data === 'string' ? data : ''), p.errors ?? [])
  }
  return data as T
}

const get = <T>(path: string, query?: Query, noRedirect = false) => request<T>('GET', path, { query, noRedirect })
const post = <T>(path: string, body?: unknown) => request<T>('POST', path, { body: body ?? {} })
const patch = <T>(path: string, body: unknown) => request<T>('PATCH', path, { body })
const del = <T>(path: string) => request<T>('DELETE', path)

function filterQuery(f: IncidentFilters): Query {
  return {
    status: f.status,
    severity: f.severity,
    verdict: f.verdict,
    cluster: f.cluster,
    namespace: f.namespace,
    q: f.q,
    muted: f.muted,
    since: f.since,
    until: f.until,
  }
}

export const api = {
  // auth
  authConfig: () => get<AuthConfig>('/auth/config', undefined, true),
  login: (email: string, password: string) =>
    request<Me>('POST', '/auth/login', { body: { email, password }, noRedirect: true }),
  logout: () => post<void>('/auth/logout'),
  oidcLoginURL: (redirect: string) => `${API_BASE}/auth/oidc/login?redirect=${encodeURIComponent(redirect)}`,
  me: () => get<Me>('/me', undefined, true),
  linkCode: () => post<LinkCode>('/me/link-code'),
  unlinkIdentity: (channelType: string) => del<void>(`/me/identities/${encodeURIComponent(channelType)}`),

  // incidents
  incidents: (f: IncidentFilters, cursor?: number, limit = 50) =>
    get<IncidentPage>('/incidents', { ...filterQuery(f), cursor, limit }),
  histogram: (f: IncidentFilters, buckets = 60) => get<Histogram>('/incidents/histogram', { ...filterQuery(f), buckets }),
  incident: (id: number) => get<IncidentDetail>(`/incidents/${id}`),
  timeline: (id: number, afterId?: number) => get<List<TimelineEvent>>(`/incidents/${id}/timeline`, { after_id: afterId }),
  signals: (id: number) => get<List<Signal>>(`/incidents/${id}/signals`),
  investigations: (id: number) => get<List<Investigation>>(`/incidents/${id}/investigations`),
  messages: (id: number) => get<List<Message>>(`/incidents/${id}/messages`),
  postMessage: (id: number, content: string) => post<Message>(`/incidents/${id}/messages`, { content }),
  actions: (id: number) => get<List<Action>>(`/incidents/${id}/actions`),
  proposeAction: (id: number, body: { type: string; params: Record<string, unknown>; rationale?: string }) =>
    post<Action>(`/incidents/${id}/actions`, body),
  ack: (id: number) => post<Incident>(`/incidents/${id}/ack`),
  resolve: (id: number, reason?: string) => post<Incident>(`/incidents/${id}/resolve`, { reason }),
  reopen: (id: number) => post<Incident>(`/incidents/${id}/reopen`),
  reinvestigate: (id: number, deep = false) => post<Incident>(`/incidents/${id}/reinvestigate`, deep ? { deep: true } : undefined),
  muteIncident: (id: number, duration: string, reason?: string) => post<Mute>(`/incidents/${id}/mute`, { duration, reason }),

  // actions
  /** For prepared actions, payloadHash binds the approval to the exact payload (409 if it changed). */
  approve: (id: number, payloadHash?: string) => post<Action>(`/actions/${id}/approve`, payloadHash ? { payload_hash: payloadHash } : undefined),
  reject: (id: number, reason?: string) => post<Action>(`/actions/${id}/reject`, { reason }),
  actionTypes: () => get<List<ActionType>>('/action-types'),

  // mutes
  mutes: (active?: boolean) => get<List<Mute>>('/mutes', { active }),
  createMute: (body: {
    matchers: Matcher[]
    duration?: string
    ends_at?: string
    starts_at?: string
    reason: string
    mirror_to_alertmanager?: boolean
    source?: string
  }) => post<Mute>('/mutes', body),
  expireMute: (id: number) => del<void>(`/mutes/${id}`),

  // status
  status: () => get<Status>('/status'),

  // admin
  users: () => get<List<User>>('/users'),
  createUser: (body: { email: string; display_name?: string; role: Role; password?: string }) => post<User>('/users', body),
  updateUser: (id: number, body: { role?: Role; disabled?: boolean; display_name?: string; password?: string }) =>
    patch<User>(`/users/${id}`, body),
  tokens: () => get<List<APIToken>>('/tokens'),
  createToken: (body: { name: string; role: Role; expires_in?: string }) => post<CreatedAPIToken>('/tokens', body),
  revokeToken: (id: number) => del<void>(`/tokens/${id}`),
}

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}
