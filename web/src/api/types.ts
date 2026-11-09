// Hand-written from docs/api.md. When the Go API is stable, these can alias the generated
// OpenAPI types in ./schema.d.ts (`npm run gen:api`).

export type ID = number
export type Timestamp = string
export type Labels = Record<string, string>

export type Role = 'viewer' | 'operator' | 'admin'
export type IncidentStatus = 'open' | 'acknowledged' | 'resolved' | 'closed'
export type Verdict = 'actionable' | 'noise' | 'unknown'
export type InvestigationState = 'none' | 'pending' | 'running' | 'done' | 'failed' | 'skipped'

export interface Incident {
  id: ID
  title: string
  status: IncidentStatus
  severity: string
  cluster: string
  namespace: string
  source: string
  verdict: Verdict
  summary: string
  muted: boolean
  internal: boolean
  labels: Labels
  opened_at: Timestamp
  acknowledged_at?: Timestamp | null
  resolved_at?: Timestamp | null
  last_signal_at: Timestamp
  duration_seconds: number
  signal_count: number
  firing_count: number
  pending_actions: number
  reopen_count: number
  investigation_state: InvestigationState
  investigation_note?: string
  storm_id?: ID | null
  merged_into_id?: ID | null
  is_storm?: boolean
  notify_channels: string[]
  assignee?: { id: ID; name: string } | null
  trace_id: string
  url: string
}

export interface AICostLine {
  calls: number
  prompt_tokens: number
  completion_tokens: number
  usd: number
}

/** AI token cost: investigation (auto/manual/deep), remediation (investigations triggered by actions), chat (agent replies). */
export interface AICost {
  investigation: AICostLine
  remediation: AICostLine
  chat: AICostLine
  total: AICostLine
}

export interface IncidentDetail extends Incident {
  latest_investigation?: Investigation | null
  cost?: AICost | null
}

export interface Signal {
  id: ID
  source: string
  fingerprint: string
  status: 'firing' | 'resolved'
  name: string
  severity: string
  cluster: string
  namespace: string
  labels: Labels
  annotations: Labels
  starts_at: Timestamp
  ends_at?: Timestamp | null
  generator_url: string
  last_seen_at: Timestamp
  fire_count: number
}

export interface Investigation {
  id: ID
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'timed_out'
  trigger: 'auto' | 'manual' | 'action'
  agent: string
  model: string
  analysis: string
  summary: string
  root_cause: string
  verdict: Verdict
  confidence: number
  tool_calls: unknown[] | null
  error: string
  prompt_tokens: number
  completion_tokens: number
  cost_usd?: number
  created_at: Timestamp
  started_at?: Timestamp | null
  finished_at?: Timestamp | null
  trace_id: string
}

export interface Message {
  id: ID
  incident_id: ID
  role: 'user' | 'agent' | 'system'
  author_name: string
  channel: string
  channel_name: string
  content: string
  status: 'done' | 'pending' | 'failed'
  action_ids: ID[]
  created_at: Timestamp
  /** agent replies only */
  model?: string
  prompt_tokens?: number
  completion_tokens?: number
  cost_usd?: number
}

export type ActionStatus = 'proposed' | 'approved' | 'rejected' | 'running' | 'succeeded' | 'failed' | 'expired'

export interface Action {
  id: ID
  incident_id: ID
  type: string
  params: Record<string, unknown>
  rationale: string
  risk: 'low' | 'medium' | 'high'
  proposed_by: 'agent' | 'user' | 'system'
  status: ActionStatus
  summary: string
  decided_by_name: string
  decided_via: string
  decided_at?: Timestamp | null
  expires_at: Timestamp
  started_at?: Timestamp | null
  finished_at?: Timestamp | null
  output: string
  error: string
  created_at: Timestamp
  /** Exact, server-prepared change for generic actions (k8s.patch/apply/delete, kubectl.exec, gitops.change). */
  prepared?: PreparedAction | null
  /** sha256 of the prepared payload; approval must echo it. */
  payload_hash?: string
  requires_approval?: boolean
  superseded_by?: ID | null
}

export interface PreparedAction {
  target: string
  operation: string
  payload: string
  payload_format: 'yaml' | 'json' | 'diff' | 'text'
  dry_run: string
  dry_run_ok: boolean
  effect: string
  rollback: string
  rollback_action?: { type: string; params: Record<string, unknown> } | null
  risk: 'low' | 'medium' | 'high'
  precondition: string
  core_infrastructure: boolean
  /** Argo CD application name when the target is managed by Argo (may be `true` when unknown). */
  argo_managed?: string | boolean | null
  warnings?: string[] | null
}

export interface TimelineEvent {
  id: ID
  incident_id: ID
  type: string
  actor_type: 'user' | 'agent' | 'system'
  actor_name: string
  channel: string
  channel_name: string
  payload: Record<string, unknown>
  trace_id: string
  created_at: Timestamp
}

export type MatcherOp = '=' | '!=' | '=~' | '!~'
export interface Matcher {
  name: string
  op: MatcherOp
  value: string
}

export interface Mute {
  id: ID
  matchers: Matcher[]
  starts_at: Timestamp
  ends_at: Timestamp
  reason: string
  created_by_name: string
  active: boolean
  incident_id?: ID | null
  alertmanager_silence_id: string
}

export interface JSONSchema {
  type?: string | string[]
  title?: string
  description?: string
  properties?: Record<string, JSONSchema>
  required?: string[]
  enum?: unknown[]
  default?: unknown
  minimum?: number
  maximum?: number
  items?: JSONSchema
  [k: string]: unknown
}

export interface ActionType {
  type: string
  description: string
  risk: 'low' | 'medium' | 'high'
  params_schema: JSONSchema
  auto_approve: boolean
}

export interface User {
  id: ID
  email: string
  display_name: string
  role: Role
  disabled: boolean
  auth: ('local' | 'oidc')[]
  last_login_at?: Timestamp | null
  created_at: Timestamp
}

export interface APIToken {
  id: ID
  name: string
  prefix: string
  role: Role
  user_id?: ID | null
  last_used_at?: Timestamp | null
  expires_at?: Timestamp | null
  revoked_at?: Timestamp | null
  created_at: Timestamp
}

export interface CreatedAPIToken extends APIToken {
  token: string
}

export interface AuthConfig {
  local_enabled: boolean
  oidc_enabled: boolean
  oidc_name: string
}

export interface Me {
  id: ID
  email: string
  display_name: string
  role: Role
  auth_method: string
  csrf_token: string
  identities: { channel_type: string; external_name: string }[]
  trace_url_template: string
}

export interface LinkCode {
  code: string
  expires_at: Timestamp
  instructions: string
}

export interface List<T> {
  items: T[]
}

export interface IncidentPage {
  items: Incident[]
  next_cursor?: string | null
  total: number
}

export interface HistogramBucket {
  start: Timestamp
  end: Timestamp
  critical: number
  warning: number
  info: number
  other: number
}

export interface Histogram {
  buckets: HistogramBucket[]
}

// /status is loosely specified in docs/api.md; fields are read defensively.
export interface SourceStatus {
  name: string
  type: string
  last_received_at?: Timestamp | null
  last_reconciled_at?: Timestamp | null
  last_error?: string
  last_error_at?: Timestamp | null
  received_total?: number
}
export interface ChannelStatus {
  name: string
  type: string
  mode?: string
  connected?: boolean
  leader?: boolean
  leader_replica?: string
  last_error?: string
  capabilities?: Record<string, unknown>
}
export interface HeartbeatStatus {
  key: string
  state: 'unknown' | 'ok' | 'silent'
  last_seen_at?: Timestamp | null
  expected_interval_seconds?: number
  last_ping_at?: Timestamp | null
  last_ping_error?: string
  incident_id?: ID | null
}
export interface AgentStatus {
  enabled: boolean
  type?: string
  model?: string
  url?: string
  url_host?: string
  max_concurrent?: number
  max_per_hour?: number
  max_per_day?: number
  deep_model?: string
  calls_last_hour?: number
  calls_today?: number
  budget_paused_until?: Timestamp | null
  storm_threshold?: number
  storm_window_seconds?: number
  cost_last_24h?: AICost | null
}
export interface LeaseStatus {
  name: string
  held?: boolean
  holder?: string
}
export interface Status {
  version: string
  replica: string
  sources: SourceStatus[]
  channels: ChannelStatus[]
  heartbeats: HeartbeatStatus[]
  agent: AgentStatus
  leases: (LeaseStatus | string)[]
}

export interface IncidentFilters {
  status?: string[]
  severity?: string[]
  verdict?: string[]
  cluster?: string
  namespace?: string
  q?: string
  muted?: boolean
  since?: string
  until?: string
}

// SSE payloads
export interface TimelineNotice {
  id: ID
  incident_id: ID
  type: string
}
export interface ProgressNotice {
  incident_id: ID
  kind: 'tool_call' | 'tool_result' | 'message' | 'done'
  tool?: string
  text?: string
}
