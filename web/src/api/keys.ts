import type { IncidentFilters } from './types'

export const qk = {
  me: ['me'] as const,
  authConfig: ['auth-config'] as const,
  incidents: (f?: IncidentFilters) => (f ? (['incidents', f] as const) : (['incidents'] as const)),
  histogram: (f?: IncidentFilters) => (f ? (['histogram', f] as const) : (['histogram'] as const)),
  incident: (id: number) => ['incident', id] as const,
  timeline: (id: number) => ['timeline', id] as const,
  signals: (id: number) => ['signals', id] as const,
  investigations: (id: number) => ['investigations', id] as const,
  messages: (id: number) => ['messages', id] as const,
  actions: (id: number) => ['actions', id] as const,
  actionTypes: ['action-types'] as const,
  mutes: ['mutes'] as const,
  status: ['status'] as const,
  users: ['users'] as const,
  tokens: ['tokens'] as const,
}
