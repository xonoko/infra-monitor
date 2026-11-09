// API types are generated from the Go API's OpenAPI spec (src/api/schema.d.ts, `make web-client`);
// CI fails if they are stale. Only UI-side types (filters, SSE notices, JSON Schema) are written here.
import type { components } from './schema'

type S = components['schemas']

export type ID = number
export type Timestamp = string
export type Labels = Record<string, string>

export type Incident = S['Incident']
export type IncidentDetail = S['IncidentDetail']
export type Role = S['User']['role']
export type IncidentStatus = Incident['status']
export type Verdict = Incident['verdict']
export type InvestigationState = Incident['investigation_state']

/** AI token cost: investigation (auto/manual/deep/grouping), remediation (action-triggered), chat (agent replies). */
export type AICost = S['Cost']
export type AICostLine = S['CostLine']

export type Signal = S['Signal']
export type Investigation = S['Investigation']
export type Message = S['Message']
export type Action = S['Action']
export type ActionStatus = Action['status']
/** Exact, server-prepared change for generic actions (k8s.patch/apply/delete, kubectl.exec, gitops.change). */
export type PreparedAction = S['Prepared']
export type TimelineEvent = S['TimelineEvent']
export type Matcher = S['Matcher']
export type MatcherOp = Matcher['op']
export type Mute = S['Mute']
export type User = S['User']
export type APIToken = S['APIToken']
export type CreatedAPIToken = S['CreatedToken']
export type AuthConfig = S['AuthConfigOutputBody']
export type Me = S['Me']
export type LinkCode = S['LinkCodeOutputBody']
export type IncidentPage = S['ListIncidentsOutputBody']
export type HistogramBucket = S['HistogramBucket']
export type Histogram = S['HistogramOutputBody']
export type SourceStatus = S['SourceStatus']
export type ChannelStatus = S['ChannelStatus']
export type HeartbeatStatus = S['HeartbeatStatus']
export type AgentStatus = S['AgentStatus']
export type Status = S['StatusOutputBody']

/** An action type; params_schema is a JSON Schema the forms are built from. */
export type ActionType = Omit<S['Info'], 'params_schema'> & { params_schema: JSONSchema }

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

export interface List<T> {
  items: T[]
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
